// Package store 是 SQLite 儲存層（modernc.org/sqlite，WAL）。
//
// 設計重點：
//   - 所有寫入走同一個寫入連線（NFR-REL-4）；讀取用另一個唯讀的連線池。
//   - migration 以 goose 執行，SQL 檔嵌入 binary。
//   - ID 一律 UUIDv7；時間存 Unix 毫秒，一律取自注入的 clock。
//
// 查詢由 sqlc 產生：SQL 寫在 queries/*.sql，schema 直接讀 migrations/ 的 goose 檔，
// 產生的程式碼在 sqlcdb/（已提交，不要手改）。sqlc 要求的 Go 版本比本專案新，所以不加進 go.mod，
// 一律以固定版本從 module 外執行。修改 migration 或 queries 後：
//
//	go generate ./internal/store/                                                          # 重新產生
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff -f internal/store/sqlc.yaml     # 檢查產生的程式碼是否最新
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 vet -f internal/store/sqlc.yaml      # 檢查查詢
//
// 注意：sqlc 的 sqlite parser 會被 queries/*.sql 內的非 ASCII 字元（含註解）弄錯位移，
// 所以那些檔案的註解一律用英文。
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // 註冊 "sqlite" driver

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

// DBFileName 是資料目錄下的資料庫檔名。
const DBFileName = "notesteme.db"

// ErrNotFound 表示查不到資料。
var ErrNotFound = errors.New("not found")

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store 持有 SQLite 的寫入連線與讀取連線池。
type Store struct {
	w     *sql.DB // 唯一的寫入連線
	r     *sql.DB // 唯讀連線池
	wq    *sqlcdb.Queries
	rq    *sqlcdb.Queries
	clock clock.Clock
	// sink 接收步驟狀態變更的事件（PIPE-R14）；沒有設定時不產生事件。見 SetEventSink。
	sink atomic.Pointer[func(events.Event)]
}

// Open 開啟 dir/notesteme.db 並套用 migration。dir 必須已存在。
// DB 與 WAL／SHM 的權限一律為 0600（NFR-SEC-5）。
func Open(ctx context.Context, dir string, clk clock.Clock) (*Store, error) {
	// 一律用絕對路徑：file: URI 遇到相對路徑會把第一段當成 host（file://data/x.db），資料目錄是相對路徑時就開不起來。
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	path := filepath.Join(dir, DBFileName)

	// NFR-SEC-5：DB 與 WAL／SHM 一律 0600，不受 umask 影響。SQLite 建立 WAL／SHM 時沿用 DB 檔的權限，
	// 所以先以 0600 建好 DB（已存在的也收緊），開啟後再確認一次。
	if err := ensurePrivateFiles(path, true); err != nil {
		return nil, err
	}

	w, err := openDB(path, false)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)

	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, w, sub)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("初始化 migration 失敗：%w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("執行 migration 失敗：%w", err)
	}

	r, err := openDB(path, true)
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)

	if err := ensurePrivateFiles(path, false); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, err
	}
	return &Store{w: w, r: r, wq: sqlcdb.New(w), rq: sqlcdb.New(r), clock: clk}, nil
}

// ensurePrivateFiles 把 DB 與 -wal、-shm 的權限設為 0600。create 為 true 時 DB 不存在會先以 0600 建立；
// -wal、-shm 只在存在時處理。
func ensurePrivateFiles(path string, create bool) error {
	if create {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return fmt.Errorf("建立資料庫檔失敗：%w", err)
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("設定 %s 的權限失敗：%w", p, err)
		}
	}
	return nil
}

func openDB(path string, readOnly bool) (*sql.DB, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("開啟資料庫失敗：%w", err)
	}
	return db, nil
}

// Close 關閉所有連線（SQLite 會在最後一個連線關閉時做 WAL checkpoint）。
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

// CheckWritable 確認寫入連線可以取得寫入鎖（供 /readyz 使用）；不會寫入任何資料。
func (s *Store) CheckWritable(ctx context.Context) error {
	conn, err := s.w.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "ROLLBACK")
	return err
}

// nowMs 回傳注入 clock 的目前時間（Unix 毫秒）。
func (s *Store) nowMs() int64 { return s.clock.Now().UnixMilli() }

func toMs(t time.Time) int64    { return t.UnixMilli() }
func fromMs(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// GetSetting 讀取 settings 的值；不存在時 ok 為 false。
func (s *Store) GetSetting(ctx context.Context, key string) (value string, ok bool, err error) {
	value, err = s.rq.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetSetting 寫入（或覆寫）settings 的值。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.wq.SetSetting(ctx, sqlcdb.SetSettingParams{Key: key, Value: value, UpdatedAt: s.nowMs()})
}

// SetSettingIfAbsent 只在 key 不存在時寫入；回傳是否寫入。
func (s *Store) SetSettingIfAbsent(ctx context.Context, key, value string) (bool, error) {
	n, err := s.wq.SetSettingIfAbsent(ctx, sqlcdb.SetSettingIfAbsentParams{Key: key, Value: value, UpdatedAt: s.nowMs()})
	return n > 0, err
}
