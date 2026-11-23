// Package postgres 是 Hub 的 Postgres backend（ADR-0030 第 2 點的正式環境），語意與 sqlite 參考 backend 相同，
// 兩者通過同一組 hubstore/storetest。
//
// 連線用 pgx 的連線池；migration 是 goose（嵌入 binary，版本號與 sqlite/migrations 一對一），以 advisory lock
// 避免多個實例同時套用。查詢由 sqlc 的 postgresql engine 產生（queries/*.sql 的註解用英文）。修改 migration 或 queries 後：
//
//	go generate ./internal/hub/hubstore/postgres/
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff -f internal/hub/hubstore/postgres/sqlc.yaml
//	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 vet -f internal/hub/hubstore/postgres/sqlc.yaml
//
// 與 SQLite 的差異（SQLite 的寫入只有一條連線，交易天然序列化；Postgres 是 READ COMMITTED 的並行交易）：
//   - 讀了再寫的交易先鎖住相關的列（版本、帳號），等鎖之後的下一句查詢才會看到別人已提交的資料；
//   - 交易遇到序列化失敗、deadlock 或唯一鍵衝突時整筆重試（重試時會走到「已存在」的分支）；
//   - 時間寫入前截到毫秒（SQLite 存 Unix 毫秒），讀回時轉成 UTC；ID 與排序用的文字欄位是 COLLATE "C"。
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres/sqlcdb"
)

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrationsFS 回傳內建的 migration（根目錄直接放 *.sql）。
func MigrationsFS() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(fmt.Errorf("hubstore/postgres: 取得內建 migration 失敗：%w", err))
	}
	return sub
}

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

// ErrNoDatabase 表示資料庫還沒有 Hub 的 schema（NoMigrate 時）。
var ErrNoDatabase = errors.New("Hub 的 Postgres 資料庫還沒有 schema，請先執行 notesteme-hub migrate。")

// SchemaTooNewError 表示資料庫的 schema 比程式新（降版執行），拒絕啟動。
type SchemaTooNewError struct{ DB, Program int64 }

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("Hub 資料庫的 schema 版本 %d 比這個程式支援的 %d 新，請改用較新的 notesteme-hub。", e.DB, e.Program)
}

// 連線的預設值（DSN 沒有指定時套用）。
const (
	defaultMaxConns         = 10
	defaultConnectTimeout   = 10 * time.Second
	defaultStatementTimeout = "30s"
	// migrationLockID 是 goose 的 advisory lock ID（與其他用 goose 的程式不同，避免互相等待）。
	migrationLockID int64 = 0x6e6f7465_68756221 // "notehub!"
)

// Options 是 Open 的選項。
type Options struct {
	// NoMigrate 為 true 時不套用 migration、不建立任何資料表，只確認 schema 存在且不比程式新（notesteme-hub doctor 用）；
	// 還沒有 schema 時回 ErrNoDatabase。
	NoMigrate bool
}

// Store 是 Postgres 的 hubstore.Store。
type Store struct {
	pool  *pgxpool.Pool
	q     *sqlcdb.Queries
	clock clock.Clock
}

var _ hubstore.Store = (*Store)(nil)

// ErrDSN 是 DSN 無法解析時的錯誤；不帶 DSN 的內容（可能含密碼）。
var ErrDSN = errors.New("Postgres 的連線字串（store.dsn 或 NOTESTEME_HUB_STORE_DSN）格式錯誤")

// poolConfig 解析 dsn 並補上預設值。錯誤訊息不含 dsn。
func poolConfig(dsn string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrDSN
	}
	if !strings.Contains(dsn, "pool_max_conns") {
		cfg.MaxConns = defaultMaxConns
	}
	cc := cfg.ConnConfig
	if cc.ConnectTimeout == 0 {
		cc.ConnectTimeout = defaultConnectTimeout
	}
	if _, ok := cc.RuntimeParams["statement_timeout"]; !ok {
		cc.RuntimeParams["statement_timeout"] = defaultStatementTimeout
	}
	if _, ok := cc.RuntimeParams["application_name"]; !ok {
		cc.RuntimeParams["application_name"] = "notesteme-hub"
	}
	// 時間一律以 time.Time 往返；session 的時區不影響 timestamptz，但固定成 UTC 讓 log 與手動查詢一致。
	cc.RuntimeParams["timezone"] = "UTC"
	return cfg, nil
}

// connectError 把連線錯誤轉成不含密碼的訊息（pgconn 的錯誤只帶主機與使用者，這裡再保險地移除密碼字串）。
func connectError(cfg *pgxpool.Config, err error) error {
	msg := err.Error()
	if pw := cfg.ConnConfig.Password; pw != "" {
		msg = strings.ReplaceAll(msg, pw, "******")
	}
	return fmt.Errorf("連線到 Postgres 失敗：%s", msg)
}

// Open 連到 dsn 並套用 migration。時間取自 clk（nil 時用真實時間）。
func Open(ctx context.Context, dsn string, clk clock.Clock, opts ...Options) (*Store, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if clk == nil {
		clk = clock.Real{}
	}
	cfg, err := poolConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, connectError(cfg, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, connectError(cfg, err)
	}
	if err := migrate(ctx, pool, !o.NoMigrate); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, q: sqlcdb.New(pool), clock: clk}, nil
}

func newProvider(db *sql.DB, locked bool) (*goose.Provider, error) {
	var popts []goose.ProviderOption
	if locked {
		locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(migrationLockID))
		if err != nil {
			return nil, err
		}
		popts = append(popts, goose.WithSessionLocker(locker))
	}
	return goose.NewProvider(goose.DialectPostgres, db, MigrationsFS(), popts...)
}

// migrate 檢查版本並（up 為 true 時）套用待執行的 migration。
func migrate(ctx context.Context, pool *pgxpool.Pool, up bool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	if !up {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('goose_db_version') IS NOT NULL`).Scan(&exists); err != nil {
			return fmt.Errorf("讀取 Hub 的 schema 版本失敗：%w", err)
		}
		if !exists {
			return ErrNoDatabase
		}
	}
	p, err := newProvider(db, up)
	if err != nil {
		return fmt.Errorf("初始化 Hub 的 migration 失敗：%w", err)
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("讀取 Hub 的 schema 版本失敗：%w", err)
	}
	if latest := LatestVersion(); cur > latest {
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

// Close 關閉連線池。
func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

// CheckWritable 實作 hubstore.Store：確認連到的是可以寫入的 primary（不是唯讀的 replica 或唯讀交易）。
func (s *Store) CheckWritable(ctx context.Context) error {
	var writable bool
	err := s.pool.QueryRow(ctx, `SELECT NOT pg_is_in_recovery() AND current_setting('transaction_read_only') = 'off'`).Scan(&writable)
	if err != nil {
		return err
	}
	if !writable {
		return errors.New("Postgres 目前是唯讀的（replica 或 default_transaction_read_only）")
	}
	return nil
}

// CountUsers 實作 hubstore.Store。
func (s *Store) CountUsers(ctx context.Context) (int64, error) { return s.q.CountUsers(ctx) }

// SchemaVersion 實作 hubstore.Store。
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	db := stdlib.OpenDBFromPool(s.pool)
	defer db.Close()
	p, err := newProvider(db, false)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

// maxTxAttempts 是交易遇到可重試的錯誤時最多執行的次數。
const maxTxAttempts = 10

// retryable 回報交易是否可以整筆重試：序列化失敗、deadlock，以及並行插入造成的唯一鍵衝突
// （重試時會讀到對方已提交的列，走「已存在」的分支，結果與 SQLite 序列化執行相同）。
func retryable(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Code {
	case "40001", "40P01", "23505":
		return true
	}
	return false
}

// inTx 在一個交易中執行 fn；fn 回傳錯誤或 panic 時 rollback，可重試的錯誤整筆重試。
// fn 可能被呼叫多次：輸出值要在 fn 一開始重設。
func (s *Store) inTx(ctx context.Context, fn func(q *sqlcdb.Queries) error) error {
	var err error
	for attempt := range maxTxAttempts {
		if err = s.tryTx(ctx, fn); err == nil || !retryable(err) {
			return err
		}
		// 退避一小段隨機時間，避免並行的交易每次都撞在一起。
		d := time.Duration(1+rand.IntN(5*(attempt+1))) * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return err
}

func (s *Store) tryTx(ctx context.Context, fn func(q *sqlcdb.Queries) error) (err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		if v := recover(); v != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(v)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
