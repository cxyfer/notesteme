// Package sqlite 是 Hub 的 SQLite backend（ADR-0030 第 2 點的參考 backend）。
//
// 連線設定與 Client 相同（internal/sqlitedb）：寫入走單一連線、讀取用唯讀連線池。migration 以 goose 的
// Provider（不用全域狀態，Client 與 Hub 在同一個程序內也互不影響）執行，SQL 檔嵌入 binary。
// 查詢由 sqlc 產生（queries/*.sql 的註解用英文，原因同 internal/store）。修改 migration 或 queries 後：
//
//	go generate ./internal/hub/hubstore/sqlite/
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff -f internal/hub/hubstore/sqlite/sqlc.yaml
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 vet -f internal/hub/hubstore/sqlite/sqlc.yaml
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite/sqlcdb"
	"github.com/cxyfer/notesteme/internal/sqlitedb"
)

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

// DBFileName 是資料目錄下的資料庫檔名。
const DBFileName = "notesteme-hub.db"

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationsFS 回傳內建的 migration（根目錄直接放 *.sql）。
func MigrationsFS() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(fmt.Errorf("hubstore/sqlite: 取得內建 migration 失敗：%w", err))
	}
	return sub
}

// ErrNoDatabase 表示資料庫還不存在（NoMigrate 時）。
var ErrNoDatabase = errors.New("Hub 資料庫還不存在，請先執行 notesteme-hub migrate。")

// LatestVersion 回傳內建 migration 的最新版本。
func LatestVersion() int64 {
	var latest int64
	entries, _ := fs.Glob(MigrationsFS(), "*.sql")
	for _, e := range entries {
		var v int64
		if _, err := fmt.Sscanf(e, "%d_", &v); err == nil {
			latest = max(latest, v)
		}
	}
	return latest
}

// SchemaTooNewError 表示資料庫的 schema 比程式新（降版執行），拒絕啟動。
type SchemaTooNewError struct{ DB, Program int64 }

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("Hub 資料庫的 schema 版本 %d 比這個程式支援的 %d 新，請改用較新的 notesteme-hub。", e.DB, e.Program)
}

// Store 是 SQLite 的 hubstore.Store。
type Store struct {
	w, r   *sql.DB
	wq, rq *sqlcdb.Queries
	clock  clock.Clock
}

var _ hubstore.Store = (*Store)(nil)

// Options 是 Open 的選項。
type Options struct {
	// NoMigrate 為 true 時不套用 migration、不建立任何檔案，只確認資料庫存在且不比程式新（notesteme-hub doctor 用）；
	// 資料庫不存在時回 ErrNoDatabase。
	NoMigrate bool
}

// Open 開啟 dir/notesteme-hub.db 並套用 migration；dir 不存在時以 0700 建立。
func Open(ctx context.Context, dir string, clk clock.Clock, opts ...Options) (*Store, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if o.NoMigrate {
		if _, err := os.Stat(filepath.Join(abs, DBFileName)); err != nil {
			return nil, ErrNoDatabase
		}
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("建立 Hub 資料目錄失敗：%w", err)
	}
	path := filepath.Join(abs, DBFileName)
	if err := sqlitedb.EnsurePrivateFiles(path, true); err != nil {
		return nil, err
	}
	w, err := sqlitedb.Open(path, false)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := migrate(ctx, w, !o.NoMigrate); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sqlitedb.Open(path, true)
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := sqlitedb.EnsurePrivateFiles(path, false); err != nil {
		_ = errors.Join(w.Close(), r.Close())
		return nil, err
	}
	return &Store{w: w, r: r, wq: sqlcdb.New(w), rq: sqlcdb.New(r), clock: clk}, nil
}

// migrate 檢查版本並（up 為 true 時）套用待執行的 migration。
// Hub 沒有 Client 的「migration 前快照」（OPS-R3）：正式環境是 Postgres，備份由資料庫負責。
func migrate(ctx context.Context, w *sql.DB, up bool) error {
	p, err := goose.NewProvider(goose.DialectSQLite3, w, MigrationsFS())
	if err != nil {
		return fmt.Errorf("初始化 Hub 的 migration 失敗：%w", err)
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("讀取 Hub 的 schema 版本失敗：%w", err)
	}
	var latest int64
	for _, s := range p.ListSources() {
		latest = max(latest, s.Version)
	}
	if cur > latest {
		return &SchemaTooNewError{DB: cur, Program: latest}
	}
	if !up {
		return nil
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("套用 Hub 的 migration 失敗：%w", err)
	}
	return nil
}

// Close 關閉所有連線。
func (s *Store) Close() error { return errors.Join(s.r.Close(), s.w.Close()) }

// CheckWritable 實作 hubstore.Store。
func (s *Store) CheckWritable(ctx context.Context) error { return sqlitedb.CheckWritable(ctx, s.w) }

// CountUsers 實作 hubstore.Store。
func (s *Store) CountUsers(ctx context.Context) (int64, error) { return s.rq.CountUsers(ctx) }

// SchemaVersion 實作 hubstore.Store。
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	p, err := goose.NewProvider(goose.DialectSQLite3, s.r, MigrationsFS())
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}
