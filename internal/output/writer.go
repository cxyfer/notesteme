package output

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// Records 是輸出紀錄的存取（OUT-R11）；*store.Store 的 Read 與 Write 實作它（見 StoreRecords）。
type Records interface {
	// Lookup 回傳 notesteme 最後一次在 path 寫入的內容雜湊；沒有紀錄時 ok 為 false。
	Lookup(ctx context.Context, itemID, path string) (sha string, ok bool, err error)
	// Record 記錄（或更新）一個寫出的檔案；影音已不存在時回傳 store.ErrNotFound。
	Record(ctx context.Context, o store.Output) error
}

// StoreRecords 把 *store.Store 接成 Records。
type StoreRecords struct{ St *store.Store }

// Lookup 實作 Records。
func (s StoreRecords) Lookup(ctx context.Context, itemID, path string) (string, bool, error) {
	o, err := s.St.Read().Output(ctx, itemID, path)
	if errors.Is(err, store.ErrNotFound) {
		return "", false, nil
	}
	return o.SHA256, err == nil, err
}

// Record 實作 Records。
func (s StoreRecords) Record(ctx context.Context, o store.Output) error {
	return s.St.Tx(ctx, func(r *store.Repo) error { return r.RecordOutput(ctx, o) })
}

// Writer 把渲染好的檔案寫進目錄，並維持「不覆蓋使用者改過的檔案」的不變式。
type Writer struct {
	Records Records
	// Now 是注入的時鐘（NFR-TEST-2）；另存新檔的日期取自它（UTC）。
	Now func() time.Time
}

// File 是要寫出的一個檔案。
type File struct {
	ItemID string
	// Dir 是已解析、已通過路徑防護的資料夾（絕對路徑）；寫入限制在這個資料夾之內（os.Root）。
	Dir string
	// Name 是檔名（不含目錄），例如 lecture.en.srt。
	Name string
	// Data 是內容（Write 用；WriteFrom 的內容由 render 寫出，不使用 Data）。
	Data   []byte
	Target string
	Format string
	Lang   string
	// Policy 是目標檔案被改過時的處理方式（PolicySaveAsNew 或 PolicySkip）。
	Policy string

	// Root 不是 nil 時（vault 目標，VAULT-R4）：Dir 是相對於 Root 的資料夾（以 / 分隔），寫入前在 Root 之內建立；
	// 紀錄與結果的路徑也是相對於 Root 的路徑。Root 由呼叫端開啟與關閉。
	Root *os.Root
	// Kind、Vault 寫進輸出紀錄（ADR-0020 第 8 點）；Kind 空字串是 subtitle。
	Kind  string
	Vault string
}

// recordPath 是寫進紀錄與結果的路徑：一般目標是絕對路徑，vault 目標是相對於 vault 根目錄的路徑。
func (f File) recordPath(name string) string {
	if f.Root != nil {
		return path.Join(f.Dir, name)
	}
	return filepath.Join(f.Dir, name)
}

// openDir 開啟寫入的資料夾：一般目標直接開啟 Dir；vault 目標在 Root 之內建立並開啟 Dir（不會離開 Root）。
func (f File) openDir() (*os.Root, error) {
	if f.Root == nil {
		return os.OpenRoot(f.Dir)
	}
	if f.Dir == "" || f.Dir == "." {
		return f.Root.OpenRoot(".")
	}
	if err := f.Root.MkdirAll(f.Dir, 0o755); err != nil {
		return nil, err
	}
	return f.Root.OpenRoot(f.Dir)
}

// Outcome 是寫出的結果。
type Outcome string

const (
	Written   Outcome = "written"   // 寫入了（新檔或覆蓋 notesteme 先前寫的）
	Unchanged Outcome = "unchanged" // 目標已經是這個內容，沒有寫入
	SavedNew  Outcome = "saved_new" // 原檔被改過，另存成 Path
	Blocked   Outcome = "blocked"   // 原檔被改過而且政策是 skip：沒有寫入
)

// Result 是 Write 的結果；Path 是寫出的檔案，Blocked 時是被改過、沒有寫入的原檔。
type Result struct {
	Outcome Outcome
	Path    string
}

// maxAltTries 是另存新檔時最多嘗試的檔名數。
const maxAltTries = 99

// maxHashBytes 是會去比對雜湊的檔案大小上限；更大的檔案不可能是我們寫的字幕，視為使用者的檔案。
const maxHashBytes = 64 << 20

// Write 寫出一個檔案（OUT-R11、R12）：
//   - 目標不存在：直接寫入（不覆蓋任何東西：硬連結成功才算數，競態中別人先建立的檔案視為改過）。
//   - 目標是一般檔案、目前的 sha256 與紀錄相同：覆蓋。
//   - 其他（沒有紀錄、雜湊不同、不是一般檔案如 symlink 與目錄）一律視為使用者的檔案：不動它，
//     policy 為 skip 時回傳 Blocked，否則另存為 `<名稱> (notesteme YYYY-MM-DD).<ext>`。
//
// 內容已經和目標相同時不寫入（Unchanged），並把目標記為 notesteme 的輸出。
// 檔案寫完立刻記錄：寫完後、記錄前當機時，下次沒有紀錄會被當成使用者的檔案，是安全的方向。
func (w *Writer) Write(ctx context.Context, f File) (Result, error) {
	if err := validName(f.Name); err != nil {
		return Result{}, err
	}
	root, err := f.openDir()
	if err != nil {
		return Result{}, fmt.Errorf("開啟輸出資料夾：%w", err)
	}
	defer root.Close()
	sum := sha256.Sum256(f.Data)
	return w.write(ctx, root, f, &payload{data: f.Data, hash: hex.EncodeToString(sum[:]), size: int64(len(f.Data))})
}

// WriteFrom 同 Write，但內容由 render 寫出，不整份放在記憶體中（NFR-RES-3）：render 寫進目標資料夾裡的暫存檔，
// 同時計算 sha256 與大小；寫完並 fsync 之後才做 Write 的衝突判斷（OUT-R11、R12、VAULT-R6），新檔以 hard link、
// 覆蓋以 rename 放到目標（NFR-REL-2）。內容與目標相同、或目標被改過而擋下時，暫存檔直接刪除。
// render 回傳錯誤時不寫任何東西（暫存檔刪除），回傳該錯誤。
func (w *Writer) WriteFrom(ctx context.Context, f File, render func(io.Writer) error) (Result, error) {
	if err := validName(f.Name); err != nil {
		return Result{}, err
	}
	root, err := f.openDir()
	if err != nil {
		return Result{}, fmt.Errorf("開啟輸出資料夾：%w", err)
	}
	defer root.Close()
	tmp, hash, size, err := writeTempFrom(root, render)
	if err != nil {
		return Result{}, err
	}
	// 新檔以 hard link 放到目標之後、或另存與擋下時，暫存檔都還在；覆蓋時已經 rename 掉（刪除失敗不影響結果）。
	defer func() { _ = root.Remove(tmp) }()
	return w.write(ctx, root, f, &payload{tmp: tmp, hash: hash, size: size})
}

// payload 是要寫出的內容：Write 的 data（需要時才寫成暫存檔），或 WriteFrom 已經寫好的暫存檔 tmp。
type payload struct {
	data []byte
	tmp  string
	hash string
	size int64
}

// stage 回傳內容完整、已 fsync 的暫存檔名；owned 為 true 代表這次才寫出的暫存檔，由呼叫端負責刪除。
// WriteFrom 的暫存檔由 WriteFrom 刪除：另存新檔時下一個檔名還要用它。
func (pl *payload) stage(root *os.Root) (tmp string, owned bool, err error) {
	if pl.tmp != "" {
		return pl.tmp, false, nil
	}
	tmp, err = writeTemp(root, pl.data)
	return tmp, err == nil, err
}

// write 是 Write 與 WriteFrom 共用的衝突判斷與寫入（見 Write）。
func (w *Writer) write(ctx context.Context, root *os.Root, f File, pl *payload) (Result, error) {
	name := f.Name
	date := w.Now().UTC().Format("2006-01-02")
	for try := 0; try <= maxAltTries; try++ {
		out, err := w.attempt(ctx, root, f, name, pl)
		if err != nil {
			return Result{}, err
		}
		if out != "" {
			if name != f.Name && out == Written {
				out = SavedNew
			}
			return Result{Outcome: out, Path: f.recordPath(name)}, nil
		}
		// 改過的檔案。
		if f.Policy == PolicySkip {
			return Result{Outcome: Blocked, Path: f.recordPath(name)}, nil
		}
		name = alternateName(f.Name, date, try+1)
	}
	return Result{}, fmt.Errorf("另存新檔的檔名都被佔用了：%s", f.Name)
}

// attempt 嘗試寫入 name：寫入或確認內容相同時回傳結果；目標是使用者的檔案時回傳空字串。
func (w *Writer) attempt(ctx context.Context, root *os.Root, f File, name string, pl *payload) (Outcome, error) {
	path, hash := f.recordPath(name), pl.hash
	// 另存的新檔記為 saved_as_new（VAULT-R5）。
	state := store.OutputStateWritten
	if name != f.Name {
		state = store.OutputStateSavedAsNew
	}
	st, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		created, err := createNew(root, name, pl)
		if err != nil {
			return "", err
		}
		if !created {
			return "", nil // 競態中別人先建立了檔案：視為使用者的
		}
		if err := w.record(ctx, f, path, pl, state); err != nil {
			_ = root.Remove(name)
			return "", err
		}
		return Written, nil
	case err != nil:
		return "", err
	case !st.Mode().IsRegular() || st.Size() > maxHashBytes:
		return "", nil // symlink、目錄等：不跟隨，也不覆蓋
	}
	cur, err := hashFile(root, name)
	if err != nil {
		return "", err
	}
	rec, ok, err := w.Records.Lookup(ctx, f.ItemID, path)
	if err != nil {
		return "", err
	}
	switch {
	case cur == hash:
		// 內容已經相同：不需要寫；沒有（或過時的）紀錄就補上，下次才認得這是我們的輸出。
		if !ok || rec != cur {
			if err := w.record(ctx, f, path, pl, store.OutputStateUnchanged); err != nil {
				return "", err
			}
		}
		return Unchanged, nil
	case ok && rec == cur:
		if err := replace(root, name, pl); err != nil {
			return "", err
		}
		if err := w.record(ctx, f, path, pl, state); err != nil {
			return "", err
		}
		return Written, nil
	}
	return "", nil
}

func (w *Writer) record(ctx context.Context, f File, path string, pl *payload, state string) error {
	return w.Records.Record(ctx, store.Output{
		ItemID: f.ItemID, Path: path, Target: f.Target, Format: f.Format, Lang: f.Lang, SHA256: pl.hash, Size: pl.size,
		Kind: f.Kind, Vault: f.Vault, State: state,
	})
}

// validName 確認檔名是資料夾裡的單一檔名：不含目錄分隔、不是 . 與 ..、不是空的。
func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("輸出的檔名 %q 不正確", name)
	}
	return nil
}

// alternateName 組出另存新檔的檔名：`<名稱> (notesteme YYYY-MM-DD).<ext>`；同一天再次另存時加上序號。
func alternateName(name, date string, n int) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if n <= 1 {
		return fmt.Sprintf("%s (notesteme %s)%s", base, date, ext)
	}
	return fmt.Sprintf("%s (notesteme %s %d)%s", base, date, n, ext)
}

func hashFile(root *os.Root, name string) (string, error) {
	f, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxHashBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeTemp 在同一個資料夾寫好暫存檔（權限 0644），回傳暫存檔名；失敗時不留下暫存檔。
func writeTemp(root *os.Root, data []byte) (string, error) {
	return createTemp(root, func(f io.Writer) error {
		_, err := f.Write(data)
		return err
	})
}

// countWriter 計算寫過的位元組數。
type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// writeTempFrom 同 writeTemp，內容由 render 寫出，同時計算 sha256 與大小。
func writeTempFrom(root *os.Root, render func(io.Writer) error) (tmp, hash string, size int64, err error) {
	h := sha256.New()
	var n countWriter
	tmp, err = createTemp(root, func(f io.Writer) error { return render(io.MultiWriter(f, h, &n)) })
	if err != nil {
		return "", "", 0, err
	}
	return tmp, hex.EncodeToString(h.Sum(nil)), n.n, nil
}

// createTemp 建立暫存檔、交給 fill 寫入內容，fsync、關閉並設定權限 0644（不受 umask 影響）；失敗時刪除暫存檔。
func createTemp(root *os.Root, fill func(io.Writer) error) (string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	tmp := ".notesteme-" + hex.EncodeToString(rnd[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	err = fill(f)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Chmod(tmp, 0o644)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// replace 以「寫暫存檔再 rename」覆蓋 name，不會留下寫到一半的檔案。
func replace(root *os.Root, name string, pl *payload) error {
	tmp, owned, err := pl.stage(root)
	if err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		if owned {
			_ = root.Remove(tmp)
		}
		return err
	}
	return nil
}

// createNew 建立不存在的 name：先寫暫存檔，再用硬連結放到目標（目標已存在時失敗，不會覆蓋）。
// 檔案已經存在時回傳 created 為 false；檔案系統不支援硬連結時退回 rename（先再確認一次目標不存在）。
func createNew(root *os.Root, name string, pl *payload) (created bool, err error) {
	tmp, owned, err := pl.stage(root)
	if err != nil {
		return false, err
	}
	if owned {
		defer func() { _ = root.Remove(tmp) }()
	}
	err = root.Link(tmp, name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrExist):
		return false, nil
	}
	if _, serr := root.Lstat(name); serr == nil {
		return false, nil
	}
	if err := root.Rename(tmp, name); err != nil {
		return false, err
	}
	return true, nil
}
