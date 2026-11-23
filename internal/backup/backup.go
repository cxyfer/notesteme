// Package backup 定義備份檔的格式與讀寫（OPS-R1、R2）。
//
// 備份是一個 tar.gz，有下列項目（依序）：
//   - manifest.json：schema_version、created_at、notesteme_version；有上傳檔時另有 uploads 清單
//     （每個檔案的 path、size、sha256）。
//   - notesteme.db：用 VACUUM INTO 產生的一致快照。
//   - config/notesteme.yaml：有設定檔時才放。
//   - uploads/<id>/<name>：上傳的原始檔（一般檔案），有上傳檔時才放；沒有上傳時不會有這類項目。
//
// 不含 master key，也不含 /data/artifacts 與 /data/media。
// 寫出（Archive.WriteTo）以串流進行，不把備份讀進記憶體；讀入（Extract）對檔案內容完全不信任：
// 只接受已知名稱的一般檔案，單一項目有大小上限；上傳檔必須出現在 manifest 中，大小與 sha256 都要相符。
// 沒有 uploads 的舊格式備份仍然可以讀入。
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/cxyfer/notesteme/internal/store"
)

// 備份中的項目名稱。
const (
	ManifestName = "manifest.json"
	DBName       = store.DBFileName
	ConfigName   = "config/notesteme.yaml"
)

// LatestName 是指向最新一份備份的 symlink（Windows 為複本）。
const LatestName = "notesteme-backup-latest.tar.gz"

// timeLayout 是檔名中的 UTC 時間，不含冒號（檔案系統與 URL 都不必跳脫）。
const timeLayout = "20060102T150405Z"

var fileNamePattern = regexp.MustCompile(`^notesteme-backup-\d{8}T\d{6}Z\.tar\.gz$`)

// FileName 回傳 t 的備份檔名，例如 notesteme-backup-20261112T100000Z.tar.gz。
func FileName(t time.Time) string {
	return "notesteme-backup-" + t.UTC().Format(timeLayout) + ".tar.gz"
}

// ValidFileName 回報 name 是不是 FileName 產生的格式；不信任的檔名（例如 server 回報的）要先通過它才能拿來寫檔。
func ValidFileName(name string) bool { return fileNamePattern.MatchString(name) }

// Stamp 回傳 t 在檔名中的時間字串，供 pre-restore 之類的檔名使用。
func Stamp(t time.Time) string { return t.UTC().Format(timeLayout) }

// Manifest 是備份的說明。
type Manifest struct {
	SchemaVersion    int64     `json:"schema_version"`
	CreatedAt        time.Time `json:"created_at"`
	NotestemeVersion string    `json:"notesteme_version"`
}

// Upload 是備份中的一個上傳原始檔，記在 manifest 的 uploads。
type Upload struct {
	// Path 是 tar 中的項目名稱 uploads/<id>/<name>。
	Path string `json:"path"`
	// Size 與 SHA256（小寫十六進位）是內容的大小與雜湊；還原時兩者都要相符。
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// manifestFile 是 manifest.json 的內容：Manifest 的欄位加上 uploads（沒有上傳檔時省略，與舊格式相同）。
type manifestFile struct {
	Manifest
	Uploads []Upload `json:"uploads,omitempty"`
}

// 各項目的大小上限；Extract 在讀取內容之前就依標頭的大小拒絕。
const (
	// manifest 含每個上傳檔的清單（每筆約 200 位元組），上限要容得下很多上傳檔。
	maxManifestBytes = 16 << 20
	maxConfigBytes   = 1 << 20
	maxDBBytes       = 64 << 30
	maxUploadBytes   = 64 << 30
	// MaxUploadNameBytes 是上傳檔名的長度上限（位元組）。
	MaxUploadNameBytes = 255
	// UploadsPrefix 是上傳檔在備份與資料目錄中的目錄名稱。
	UploadsPrefix = "uploads"
)

// uuidName 是影音 ID（UUID）的字串格式。
var uuidName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// shaPattern 是 sha256 的十六進位字串。
var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseUploadPath 檢查 uploads/<uuid>/<name> 並回傳 id 與檔名：
// 剛好三段；id 是小寫 UUID；name 非空、不是 . 或 ..、不含 / 與 \、沒有控制字元、不超過 255 位元組。
func ParseUploadPath(p string) (id, name string, ok bool) {
	segs := strings.Split(p, "/")
	if len(segs) != 3 || segs[0] != UploadsPrefix || !uuidName.MatchString(segs[1]) {
		return "", "", false
	}
	name = segs[2]
	if name == "" || name == "." || name == ".." || len(name) > MaxUploadNameBytes {
		return "", "", false
	}
	for _, r := range name {
		if r == '\\' || unicode.IsControl(r) {
			return "", "", false
		}
	}
	return segs[1], name, true
}

// Limits 是 Extract 對每個項目的大小上限（位元組）；零值使用預設。
type Limits struct {
	Manifest, Config, DB int64
	// Upload 是單一上傳檔的大小上限。
	Upload int64
	// FreeSpace 回傳 dir 的剩餘位元組數；nil 時用平台的實作（unix 為 statfs）。
	// 回傳 ErrSpaceUnknown 表示無法得知，Extract 會略過空間檢查。
	FreeSpace func(dir string) (uint64, error)
}

// ErrSpaceUnknown 表示這個平台無法查詢剩餘空間。
var ErrSpaceUnknown = errors.New("無法得知剩餘空間")

// ErrInsufficientSpace 表示目的目錄的剩餘空間不夠解開備份的資料庫。
var ErrInsufficientSpace = errors.New("磁碟剩餘空間不足")

func (l Limits) withDefaults() Limits {
	if l.Manifest <= 0 {
		l.Manifest = maxManifestBytes
	}
	if l.Config <= 0 {
		l.Config = maxConfigBytes
	}
	if l.DB <= 0 {
		l.DB = maxDBBytes
	}
	if l.Upload <= 0 {
		l.Upload = maxUploadBytes
	}
	if l.FreeSpace == nil {
		l.FreeSpace = freeSpace
	}
	return l
}

// Archive 是要寫出的備份內容。
type Archive struct {
	Manifest Manifest
	// DBPath 是資料庫快照（已經完整寫好，內容不會再變）。
	DBPath string
	// Config 是設定檔內容；HasConfig 為 false 時備份不含設定檔。
	Config    []byte
	HasConfig bool
	// UploadsDir 是 <data>/uploads；Uploads 是要放進備份的上傳檔（Path 為 uploads/<id>/<name>），
	// 大小與 sha256 在寫出前就要決定（manifest 在最前面）。寫出時重新計算雜湊，與清單不符就失敗。
	UploadsDir string
	Uploads    []Upload
}

// countWriter 計算寫出的位元組數。
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// WriteTo 把備份以 tar.gz 寫到 w，回傳寫出的位元組數。實作 io.WriterTo。
func (a *Archive) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	zw := gzip.NewWriter(cw)
	tw := tar.NewWriter(zw)

	for _, u := range a.Uploads {
		if _, _, ok := ParseUploadPath(u.Path); !ok {
			return 0, fmt.Errorf("上傳檔的路徑 %q 不正確", u.Path)
		}
	}
	manifest, err := json.MarshalIndent(manifestFile{Manifest: a.Manifest, Uploads: a.Uploads}, "", "  ")
	if err != nil {
		return 0, err
	}
	mod := a.Manifest.CreatedAt
	put := func(name string, size int64, body io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: size, ModTime: mod}); err != nil {
			return err
		}
		n, err := io.Copy(tw, body)
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("%s 的大小在寫出時改變了（%d，預期 %d）", name, n, size)
		}
		return nil
	}

	if err := put(ManifestName, int64(len(manifest)), bytes.NewReader(manifest)); err != nil {
		return cw.n, err
	}
	db, err := os.Open(a.DBPath)
	if err != nil {
		return cw.n, err
	}
	defer db.Close()
	fi, err := db.Stat()
	if err != nil {
		return cw.n, err
	}
	if err := put(DBName, fi.Size(), db); err != nil {
		return cw.n, err
	}
	if a.HasConfig {
		if err := put(ConfigName, int64(len(a.Config)), bytes.NewReader(a.Config)); err != nil {
			return cw.n, err
		}
	}
	if err := a.writeUploads(put); err != nil {
		return cw.n, err
	}
	if err := tw.Close(); err != nil {
		return cw.n, err
	}
	if err := zw.Close(); err != nil {
		return cw.n, err
	}
	return cw.n, nil
}

// writeUploads 依 manifest 的清單把上傳檔寫出。用 os.OpenRoot 讀取：路徑不能逃出 uploads 目錄，
// 而且只收一般檔案（symlink、FIFO 等一律拒絕）；大小與 sha256 必須與清單相符。
func (a *Archive) writeUploads(put func(name string, size int64, body io.Reader) error) error {
	if len(a.Uploads) == 0 {
		return nil
	}
	root, err := os.OpenRoot(a.UploadsDir)
	if err != nil {
		return fmt.Errorf("開啟上傳目錄 %s 失敗：%w", a.UploadsDir, err)
	}
	defer root.Close()
	for _, u := range a.Uploads {
		rel := strings.TrimPrefix(u.Path, UploadsPrefix+"/")
		// 先用 Lstat 確認型別，再開啟：開啟 FIFO 會卡住。
		if fi, err := root.Lstat(rel); err != nil {
			return fmt.Errorf("讀取上傳檔 %s 失敗：%w", u.Path, err)
		} else if !fi.Mode().IsRegular() {
			return fmt.Errorf("上傳檔 %s 不是一般檔案", u.Path)
		}
		f, err := root.Open(rel)
		if err != nil {
			return fmt.Errorf("開啟上傳檔 %s 失敗：%w", u.Path, err)
		}
		err = func() error {
			defer f.Close()
			if fi, err := f.Stat(); err != nil {
				return err
			} else if !fi.Mode().IsRegular() {
				return fmt.Errorf("上傳檔 %s 不是一般檔案", u.Path)
			}
			h := sha256.New()
			if err := put(u.Path, u.Size, io.TeeReader(f, h)); err != nil {
				return err
			}
			if got := hex.EncodeToString(h.Sum(nil)); got != u.SHA256 {
				return fmt.Errorf("上傳檔 %s 的內容與備份開始時記錄的不同（sha256 %s，預期 %s）", u.Path, got, u.SHA256)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// Contents 是 Extract 讀出的備份內容。
type Contents struct {
	Manifest Manifest
	// DBPath 是解到 dir 的資料庫檔。
	DBPath string
	// Config 是設定檔內容；HasConfig 為 false 時備份不含設定檔。
	Config    []byte
	HasConfig bool
	// Uploads 是備份中的上傳檔（已驗證大小與 sha256），UploadsDir 是它們解到的目錄（dir/uploads，結構是 <id>/<name>）；
	// 備份沒有上傳檔時 Uploads 為空、UploadsDir 為空字串。
	Uploads    []Upload
	UploadsDir string
}

// 讀取備份時的錯誤。
var (
	// ErrUnsafeEntry 表示項目名稱不安全（絕對路徑、含 ..、含反斜線等）。
	ErrUnsafeEntry = errors.New("備份檔含有不安全的項目")
	// ErrBadArchive 表示備份檔的內容不符合格式。
	ErrBadArchive = errors.New("不是有效的備份檔")
)

func badf(format string, a ...any) error {
	return fmt.Errorf("%w：%s", ErrBadArchive, fmt.Sprintf(format, a...))
}

// unsafeName 回報 tar 項目名稱是否可能逃出目的目錄。
func unsafeName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return true
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// Extract 讀出 r 的備份：資料庫解到 dir/notesteme.db（0600，dir 必須已存在），上傳檔解到 dir/uploads/<id>/<name>，
// 其他內容讀進記憶體。
// 備份檔不受信任：項目必須是已知名稱的一般檔案（拒絕 ../、絕對路徑、symlink、目錄與未知名稱；
// 上傳檔的名稱必須是 uploads/<uuid>/<name>），不可重複，大小不可超過上限，manifest.json 與 notesteme.db 都必須有。
// 上傳檔必須列在 manifest 的 uploads（manifest 要在它們之前），大小與 sha256 都要相符，manifest 列出的也都要有。
// 剩餘空間檢查包含上傳檔。失敗時不會留下 dir/notesteme.db 與 dir/uploads。
func Extract(r io.Reader, dir string, lim Limits) (c *Contents, err error) {
	lim = lim.withDefaults()
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, badf("不是 gzip（%v）", err)
	}
	defer zr.Close()
	zr.Multistream(false)

	dbPath := filepath.Join(dir, DBName)
	uploadsDir := filepath.Join(dir, UploadsPrefix)
	defer func() {
		if err != nil {
			_ = os.Remove(dbPath)
			_ = os.RemoveAll(uploadsDir)
		}
	}()

	tr := tar.NewReader(zr)
	c = &Contents{DBPath: dbPath}
	seen := map[string]bool{}
	// 檢查 dir 是否放得下 need 位元組；平台不支援查詢時略過。
	checkSpace := func(need uint64, what string) error {
		free, ferr := lim.FreeSpace(dir)
		if errors.Is(ferr, ErrSpaceUnknown) {
			return nil
		}
		if ferr != nil {
			return fmt.Errorf("查詢 %s 的剩餘空間失敗：%w", dir, ferr)
		}
		if need >= free {
			return fmt.Errorf("%w：%s需要 %d 位元組，%s 只剩 %d", ErrInsufficientSpace, what, need, dir, free)
		}
		return nil
	}
	var (
		wantUploads   = map[string]Upload{}
		uploadsTotal  uint64
		uploadsSpaced bool
	)
	for {
		h, nerr := tr.Next()
		if errors.Is(nerr, io.EOF) {
			break
		}
		if nerr != nil {
			return nil, badf("讀取項目失敗（%v）", nerr)
		}
		if unsafeName(h.Name) {
			return nil, fmt.Errorf("%w：%q", ErrUnsafeEntry, h.Name)
		}
		var limit int64
		isUpload := false
		switch h.Name {
		case ManifestName:
			limit = lim.Manifest
		case DBName:
			limit = lim.DB
		case ConfigName:
			limit = lim.Config
		default:
			if !strings.HasPrefix(h.Name, UploadsPrefix+"/") {
				return nil, badf("未知的項目 %q", h.Name)
			}
			if _, _, ok := ParseUploadPath(h.Name); !ok {
				return nil, fmt.Errorf("%w：%q（上傳檔的名稱必須是 uploads/<uuid>/<名稱>）", ErrUnsafeEntry, h.Name)
			}
			limit, isUpload = lim.Upload, true
		}
		if h.Typeflag != tar.TypeReg {
			return nil, badf("項目 %q 不是一般檔案（symlink、目錄與特殊檔案一律拒絕）", h.Name)
		}
		if seen[h.Name] {
			return nil, badf("項目 %q 重複", h.Name)
		}
		seen[h.Name] = true
		if h.Size > limit {
			return nil, badf("項目 %q 太大（%d 位元組，上限 %d）", h.Name, h.Size, limit)
		}
		if isUpload {
			want, ok := wantUploads[h.Name]
			switch {
			case !seen[ManifestName]:
				return nil, badf("上傳檔 %q 出現在 %s 之前", h.Name, ManifestName)
			case !ok:
				return nil, badf("上傳檔 %q 不在 %s 的清單中", h.Name, ManifestName)
			case h.Size != want.Size:
				return nil, badf("上傳檔 %q 的大小（%d）與 %s 記錄的（%d）不符", h.Name, h.Size, ManifestName, want.Size)
			}
			// 第一個上傳檔之前，先確認放得下全部的上傳檔（資料庫已經寫出，剩餘空間是現在的）。
			if !uploadsSpaced {
				uploadsSpaced = true
				if err := checkSpace(uploadsTotal, "上傳檔"); err != nil {
					return nil, err
				}
			}
			id, name, _ := ParseUploadPath(h.Name)
			sum, werr := writeUpload(filepath.Join(uploadsDir, id), name, tr)
			if werr != nil {
				return nil, werr
			}
			if sum != want.SHA256 {
				return nil, badf("上傳檔 %q 的 sha256（%s）與 %s 記錄的（%s）不符", h.Name, sum, ManifestName, want.SHA256)
			}
			c.Uploads = append(c.Uploads, want)
			continue
		}
		switch h.Name {
		case DBName:
			// 標頭宣告的大小不可信，但足以先擋下明顯放不下的備份，避免寫到一半把磁碟塞滿。
			// manifest 已經讀過時，上傳檔的大小一併算進去。
			if err := checkSpace(uint64(h.Size)+uploadsTotal, "資料庫與上傳檔"); err != nil {
				return nil, err
			}
			if err := writeNew(dbPath, tr); err != nil {
				return nil, err
			}
		case ManifestName:
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, badf("讀取 %s 失敗（%v）", h.Name, err)
			}
			var mf manifestFile
			if err := json.Unmarshal(b, &mf); err != nil {
				return nil, badf("%s 不是正確的 JSON（%v）", h.Name, err)
			}
			c.Manifest = mf.Manifest
			for _, u := range mf.Uploads {
				if _, _, ok := ParseUploadPath(u.Path); !ok {
					return nil, fmt.Errorf("%w：%s 的上傳檔路徑 %q", ErrUnsafeEntry, ManifestName, u.Path)
				}
				if _, dup := wantUploads[u.Path]; dup {
					return nil, badf("%s 的上傳檔 %q 重複", ManifestName, u.Path)
				}
				if u.Size < 0 || u.Size > lim.Upload || !shaPattern.MatchString(u.SHA256) {
					return nil, badf("%s 的上傳檔 %q 的大小或 sha256 不正確", ManifestName, u.Path)
				}
				wantUploads[u.Path] = u
				uploadsTotal += uint64(u.Size)
			}
		case ConfigName:
			if c.Config, err = io.ReadAll(tr); err != nil {
				return nil, badf("讀取 %s 失敗（%v）", h.Name, err)
			}
			c.HasConfig = true
		}
	}
	// 讀到 gzip 結尾才會驗證 CRC；結尾的多餘內容只容許 tar 的補齊，過多視為損毀。
	if n, derr := io.Copy(io.Discard, io.LimitReader(zr, 1<<20)); derr != nil {
		return nil, badf("備份檔已損毀（%v）", derr)
	} else if n >= 1<<20 {
		return nil, badf("結尾有多餘的內容")
	}
	if !seen[ManifestName] {
		return nil, badf("缺少 %s", ManifestName)
	}
	if !seen[DBName] {
		return nil, badf("缺少 %s", DBName)
	}
	if c.Manifest.SchemaVersion < 1 {
		return nil, badf("%s 的 schema_version 不正確", ManifestName)
	}
	for p := range wantUploads {
		if !seen[p] {
			return nil, badf("缺少 %s 記錄的上傳檔 %q", ManifestName, p)
		}
	}
	if len(c.Uploads) > 0 {
		c.UploadsDir = uploadsDir
	}
	return c, nil
}

// writeUpload 把一個上傳檔寫到 dir/name（dir 不存在時建立，0700；檔案 0600），回傳內容的 sha256。
func writeUpload(dir, name string, r io.Reader) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), r); err != nil {
		_ = f.Close()
		return "", badf("寫出上傳檔 %s 失敗（%v）", name, err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeNew 以 0600 建立新檔 path 並寫入 r；檔案已存在視為錯誤。
func writeNew(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return badf("寫出 %s 失敗（%v）", filepath.Base(path), err)
	}
	return f.Close()
}

// SaveTo 把備份串流 r 存成 dir/name（0600，先寫暫存名稱再 rename），並更新 dir 中的 latest 指向它。
// dir 不存在時建立（0700）。name 必須是 FileName 的格式，不能夾帶路徑。
func SaveTo(dir, name string, r io.Reader) (path string, err error) {
	if !ValidFileName(name) {
		return "", fmt.Errorf("備份檔名 %q 不正確", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path = filepath.Join(dir, name)
	if err := store.WriteFileAtomic(path, r); err != nil {
		return "", fmt.Errorf("寫入 %s 失敗：%w", path, err)
	}
	if err := updateLatest(dir, name, runtime.GOOS); err != nil {
		return path, fmt.Errorf("備份已存成 %s，但更新 %s 失敗：%w", path, LatestName, err)
	}
	return path, nil
}

// updateLatest 讓 dir/LatestName 指向 name：相對 symlink（先建暫存名稱再 rename，不會有找不到的空檔期）；
// Windows 不能假設可以建立 symlink，改複製一份。
func updateLatest(dir, name, goos string) error {
	latest := filepath.Join(dir, LatestName)
	if goos == "windows" {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		defer f.Close()
		return store.WriteFileAtomic(latest, f)
	}
	// 暫存名稱每次不同：手動備份與每日備份同時寫入同一個目錄時不會互相刪掉或蓋掉對方的暫存 symlink。
	for range 5 {
		tmp := filepath.Join(dir, "."+LatestName+"."+randomSuffix()+".tmp")
		err := os.Symlink(name, tmp)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.Rename(tmp, latest); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	return errors.New("無法建立不重複的暫存名稱")
}

// randomSuffix 回傳暫存檔名用的隨機字串。
func randomSuffix() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SaveWith 同 SaveTo，但內容由 write 直接寫進暫存檔（serve 自己的每日備份用，不經過 HTTP 串流）。
// write 失敗、寫入或 rename 失敗時刪除暫存檔，不留下看似完整的檔案，也不更新 latest。
func SaveWith(dir, name string, write func(io.Writer) error) (path string, err error) {
	if !ValidFileName(name) {
		return "", fmt.Errorf("備份檔名 %q 不正確", name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path = filepath.Join(dir, name)
	if err := writeAtomic(path, write); err != nil {
		return "", err
	}
	if err := updateLatest(dir, name, runtime.GOOS); err != nil {
		return path, fmt.Errorf("備份已存成 %s，但更新 %s 失敗：%w", path, LatestName, err)
	}
	return path, nil
}

// writeAtomic 以 0600 的暫存檔寫入 dst 所在的目錄，sync 後 rename 成 dst；任何失敗都刪除暫存檔。
func writeAtomic(dst string, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if err := write(f); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	ok = true
	return nil
}

// FreeSpace 回傳 dir 所在檔案系統一般使用者可用的剩餘位元組數；無法得知時回傳 ErrSpaceUnknown。
func FreeSpace(dir string) (uint64, error) { return freeSpace(dir) }
