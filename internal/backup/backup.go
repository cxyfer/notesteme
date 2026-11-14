// Package backup 定義備份檔的格式與讀寫（OPS-R1、R2）。
//
// 備份是一個 tar.gz，只有三種項目：
//   - manifest.json：schema_version、created_at、notesteme_version。
//   - notesteme.db：用 VACUUM INTO 產生的一致快照。
//   - config/notesteme.yaml：有設定檔時才放。
//
// 不含 master key，也不含 /data/artifacts 與 /data/media。
// 寫出（Archive.WriteTo）以串流進行，不把備份讀進記憶體；讀入（Extract）對檔案內容完全不信任：
// 只接受已知名稱的一般檔案，單一項目有大小上限。
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

// 各項目的大小上限；Extract 在讀取內容之前就依標頭的大小拒絕。
const (
	maxManifestBytes = 64 << 10
	maxConfigBytes   = 1 << 20
	maxDBBytes       = 64 << 30
)

// Limits 是 Extract 對每個項目的大小上限（位元組）；零值使用預設。
type Limits struct{ Manifest, Config, DB int64 }

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

	manifest, err := json.MarshalIndent(a.Manifest, "", "  ")
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
	if err := tw.Close(); err != nil {
		return cw.n, err
	}
	if err := zw.Close(); err != nil {
		return cw.n, err
	}
	return cw.n, nil
}

// Contents 是 Extract 讀出的備份內容。
type Contents struct {
	Manifest Manifest
	// DBPath 是解到 dir 的資料庫檔。
	DBPath string
	// Config 是設定檔內容；HasConfig 為 false 時備份不含設定檔。
	Config    []byte
	HasConfig bool
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

// Extract 讀出 r 的備份：資料庫解到 dir/notesteme.db（0600，dir 必須已存在），其他內容讀進記憶體。
// 備份檔不受信任：項目必須是已知名稱的一般檔案（拒絕 ../、絕對路徑、symlink、目錄與未知名稱），
// 不可重複，大小不可超過上限，manifest.json 與 notesteme.db 都必須有。失敗時不會留下 dir/notesteme.db。
func Extract(r io.Reader, dir string, lim Limits) (c *Contents, err error) {
	lim = lim.withDefaults()
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, badf("不是 gzip（%v）", err)
	}
	defer zr.Close()
	zr.Multistream(false)

	dbPath := filepath.Join(dir, DBName)
	defer func() {
		if err != nil {
			_ = os.Remove(dbPath)
		}
	}()

	tr := tar.NewReader(zr)
	c = &Contents{DBPath: dbPath}
	seen := map[string]bool{}
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
		switch h.Name {
		case ManifestName:
			limit = lim.Manifest
		case DBName:
			limit = lim.DB
		case ConfigName:
			limit = lim.Config
		default:
			return nil, badf("未知的項目 %q", h.Name)
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
		switch h.Name {
		case DBName:
			if err := writeNew(dbPath, tr); err != nil {
				return nil, err
			}
		case ManifestName:
			b, err := io.ReadAll(tr)
			if err != nil {
				return nil, badf("讀取 %s 失敗（%v）", h.Name, err)
			}
			if err := json.Unmarshal(b, &c.Manifest); err != nil {
				return nil, badf("%s 不是正確的 JSON（%v）", h.Name, err)
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
	return c, nil
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
	tmp := filepath.Join(dir, "."+LatestName+".tmp")
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(name, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, latest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
