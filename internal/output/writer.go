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
	Name   string
	Data   []byte
	Target string
	Format string
	Lang   string
	// Policy 是目標檔案被改過時的處理方式（PolicySaveAsNew 或 PolicySkip）。
	Policy string
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
	root, err := os.OpenRoot(f.Dir)
	if err != nil {
		return Result{}, fmt.Errorf("開啟輸出資料夾：%w", err)
	}
	defer root.Close()
	sum := sha256.Sum256(f.Data)
	hash := hex.EncodeToString(sum[:])

	name := f.Name
	date := w.Now().UTC().Format("2006-01-02")
	for try := 0; try <= maxAltTries; try++ {
		out, err := w.attempt(ctx, root, f, name, hash)
		if err != nil {
			return Result{}, err
		}
		if out != "" {
			if name != f.Name && out == Written {
				out = SavedNew
			}
			return Result{Outcome: out, Path: filepath.Join(f.Dir, name)}, nil
		}
		// 改過的檔案。
		if f.Policy == PolicySkip {
			return Result{Outcome: Blocked, Path: filepath.Join(f.Dir, name)}, nil
		}
		name = alternateName(f.Name, date, try+1)
	}
	return Result{}, fmt.Errorf("另存新檔的檔名都被佔用了：%s", f.Name)
}

// attempt 嘗試寫入 name：寫入或確認內容相同時回傳結果；目標是使用者的檔案時回傳空字串。
func (w *Writer) attempt(ctx context.Context, root *os.Root, f File, name, hash string) (Outcome, error) {
	path := filepath.Join(f.Dir, name)
	st, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		created, err := createNew(root, name, f.Data)
		if err != nil {
			return "", err
		}
		if !created {
			return "", nil // 競態中別人先建立了檔案：視為使用者的
		}
		if err := w.record(ctx, f, path, hash); err != nil {
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
			if err := w.record(ctx, f, path, hash); err != nil {
				return "", err
			}
		}
		return Unchanged, nil
	case ok && rec == cur:
		if err := replace(root, name, f.Data); err != nil {
			return "", err
		}
		if err := w.record(ctx, f, path, hash); err != nil {
			return "", err
		}
		return Written, nil
	}
	return "", nil
}

func (w *Writer) record(ctx context.Context, f File, path, hash string) error {
	return w.Records.Record(ctx, store.Output{
		ItemID: f.ItemID, Path: path, Target: f.Target, Format: f.Format, Lang: f.Lang, SHA256: hash, Size: int64(len(f.Data)),
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
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	tmp := ".notesteme-" + hex.EncodeToString(rnd[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Chmod(tmp, 0o644) // 不受 umask 影響
	}
	if err != nil {
		_ = root.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// replace 以「寫暫存檔再 rename」覆蓋 name，不會留下寫到一半的檔案。
func replace(root *os.Root, name string, data []byte) error {
	tmp, err := writeTemp(root, data)
	if err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// createNew 建立不存在的 name：先寫暫存檔，再用硬連結放到目標（目標已存在時失敗，不會覆蓋）。
// 檔案已經存在時回傳 created 為 false；檔案系統不支援硬連結時退回 rename（先再確認一次目標不存在）。
func createNew(root *os.Root, name string, data []byte) (created bool, err error) {
	tmp, err := writeTemp(root, data)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Remove(tmp) }()
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
