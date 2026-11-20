package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// 這個檔案放備份與還原需要的資料庫操作（OPS-R1、R2、R3）：一致的快照、schema 版本、原子寫檔。

// snapshotTo 以 VACUUM INTO 把 db 的內容存成 dest（0600）：先寫到暫存名稱，完成後才 rename，
// 所以 dest 不會出現寫到一半的檔案。db 不能是 query_only 的連線。dest 已存在時會被取代。
func snapshotTo(ctx context.Context, db *sql.DB, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp")
	// VACUUM INTO 只接受不存在或空的檔案：先以 0600 建好空檔，內容就不會以較寬鬆的權限出現。
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := syncFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// SnapshotTo 把目前的資料庫存成 dest 的一致快照，serve 執行中也可以做（OPS-R1）。
// 用另一條沒有 query_only 的連線執行 VACUUM INTO：讀取連線池是唯讀的，寫入連線則不該被長時間佔用。
func (s *Store) SnapshotTo(ctx context.Context, dest string) error {
	db, err := openDB(s.path, false)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return snapshotTo(ctx, db, dest)
}

// SnapshotFile 把 path 的資料庫（沒有任何程序使用它）存成 dest 的快照；開啟時 SQLite 會先處理遺留的 WAL。
func SnapshotFile(ctx context.Context, path, dest string) error {
	db, err := openDB(path, false)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return snapshotTo(ctx, db, dest)
}

const versionQuery = `SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`

// SchemaVersion 回傳資料庫目前的 schema 版本（goose 版本表中最新已套用的版本）。
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	err := s.r.QueryRowContext(ctx, versionQuery).Scan(&v)
	return v, err
}

// VerifyFile 以唯讀方式開啟 path 的資料庫，確認它是完整的 SQLite（quick_check）並回傳 schema 版本。
// 沒有 goose 版本表的資料庫版本為 0。
func VerifyFile(ctx context.Context, path string) (int64, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)"}}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return 0, fmt.Errorf("不是有效的資料庫：%w", err)
	}
	if check != "ok" {
		return 0, fmt.Errorf("資料庫檢查沒有通過：%s", check)
	}
	var v int64
	if err := db.QueryRowContext(ctx, versionQuery).Scan(&v); err != nil {
		// 沒有版本表：不是 Notesteme 的資料庫（或版本 0）。
		return 0, nil
	}
	return v, nil
}

var migrationFile = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// LatestSchemaVersion 回傳 sub（根目錄直接放 *.sql；nil 時用內建的）中最新的 migration 版本。
func LatestSchemaVersion(sub fs.FS) (int64, error) {
	if sub == nil {
		sub = MigrationsFS()
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		return 0, err
	}
	var latest int64
	for _, e := range entries {
		if m := migrationFile.FindStringSubmatch(e.Name()); m != nil {
			v, _ := strconv.ParseInt(m[1], 10, 64)
			latest = max(latest, v)
		}
	}
	return latest, nil
}

// WriteFileAtomic 把 r 的內容寫成 dst（0600）：先寫同目錄的暫存檔，完整寫完並 fsync 才 rename 蓋過目標。
// 中途失敗時目標維持原樣，也不留下暫存檔。dst 的上層目錄必須已存在。
func WriteFileAtomic(dst string, r io.Reader) error {
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	// CreateTemp 建立的檔案是 0600，仍明確設定一次，不依賴標準函式庫的實作細節。
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := io.Copy(f, r); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
