package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/storetest"
)

// EnvTestDSN 是測試用的 Postgres 連線字串（URL 格式）；沒有設定時這個套件需要真實資料庫的測試一律略過。
// 本機：initdb 一個 cluster；CI：postgres:16 的 service container（.github/workflows/ci.yml）。
const EnvTestDSN = "NOTESTEME_TEST_POSTGRES_DSN"

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(EnvTestDSN)
	if dsn == "" {
		t.Skipf("沒有設定 %s，略過需要 Postgres 的測試", EnvTestDSN)
	}
	return dsn
}

// withParam 在 URL 格式的 DSN 加上一個參數（pgx 把不認得的參數當成 session 的 runtime 參數）。
func withParam(t *testing.T, dsn, k, v string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		t.Fatalf("%s 要是 URL 格式（postgres://…）", EnvTestDSN)
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String()
}

// freshSchema 建立一個隨機名稱的 schema，回傳 search_path 只有它的 DSN；測試結束時連同資料刪除。
func freshSchema(t *testing.T) string {
	t.Helper()
	dsn := testDSN(t)
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "hubtest_" + hex.EncodeToString(b)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("連線到測試用的 Postgres 失敗：%v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	return withParam(t, dsn, "search_path", schema)
}

func openFresh(t *testing.T, clk clock.Clock) *Store {
	t.Helper()
	s, err := Open(context.Background(), freshSchema(t), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 共用的 store 案例（ADR-0030 第 2 點）：與 SQLite 跑同一組。
func TestStore(t *testing.T) {
	testDSN(t)
	storetest.Run(t, func(t *testing.T, clk clock.Clock) hubstore.Store { return openFresh(t, clk) })
}

// DSN 解析失敗時錯誤訊息不帶 DSN（可能含密碼）。
func TestBadDSNDoesNotLeakPassword(t *testing.T) {
	for _, dsn := range []string{
		"postgres://hub:s3cr3t-pw@[::1/hub?sslmode=disable",
		"host=localhost password=s3cr3t-pw port=notaport",
		"postgres://hub:s3cr3t-pw@localhost/hub?pool_max_conns=abc",
	} {
		_, err := Open(context.Background(), dsn, nil)
		if err == nil {
			t.Fatalf("Open(%q) 預期失敗", dsn)
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("錯誤訊息洩漏密碼：%v", err)
		}
	}
}

// 連線失敗（密碼錯誤）時錯誤訊息不含密碼。
func TestWrongPasswordDoesNotLeak(t *testing.T) {
	dsn := testDSN(t)
	u, _ := url.Parse(dsn)
	u.User = url.UserPassword(u.User.Username(), "wr0ng-s3cr3t")
	_, err := Open(context.Background(), u.String(), nil)
	if err == nil {
		t.Fatal("密碼錯誤時預期連線失敗")
	}
	if strings.Contains(err.Error(), "wr0ng-s3cr3t") {
		t.Errorf("錯誤訊息洩漏密碼：%v", err)
	}
}

func TestPoolConfigDefaults(t *testing.T) {
	cfg, err := poolConfig("postgres://u:p@localhost:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	rp := cfg.ConnConfig.RuntimeParams
	if cfg.MaxConns != defaultMaxConns || cfg.ConnConfig.ConnectTimeout != defaultConnectTimeout ||
		rp["statement_timeout"] != defaultStatementTimeout || rp["application_name"] != "notesteme-hub" || rp["timezone"] != "UTC" {
		t.Errorf("預設值 = %d %v %v", cfg.MaxConns, cfg.ConnConfig.ConnectTimeout, rp)
	}
	// DSN 指定的值優先。
	cfg, err = poolConfig("postgres://u:p@localhost:5432/db?pool_max_conns=3&connect_timeout=2&statement_timeout=5s&application_name=x")
	if err != nil {
		t.Fatal(err)
	}
	rp = cfg.ConnConfig.RuntimeParams
	if cfg.MaxConns != 3 || cfg.ConnConfig.ConnectTimeout.Seconds() != 2 || rp["statement_timeout"] != "5s" || rp["application_name"] != "x" {
		t.Errorf("DSN 的值 = %d %v %v", cfg.MaxConns, cfg.ConnConfig.ConnectTimeout, rp)
	}
}

// doctor 用的 NoMigrate：還沒有 schema 時回 ErrNoDatabase 且不建立任何資料表；schema 比程式新時拒絕。
func TestNoMigrateAndSchemaTooNew(t *testing.T) {
	ctx := context.Background()
	dsn := freshSchema(t)
	if _, err := Open(ctx, dsn, nil, Options{NoMigrate: true}); !errors.Is(err, ErrNoDatabase) {
		t.Fatalf("沒有 schema 時 = %v，預期 ErrNoDatabase", err)
	}
	s, err := Open(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != LatestVersion() {
		t.Fatalf("SchemaVersion = %d, %v，預期 %d", v, err, LatestVersion())
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (999, true)`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	_, err = Open(ctx, dsn, nil, Options{NoMigrate: true})
	var tn *SchemaTooNewError
	if !errors.As(err, &tn) || tn.DB != 999 || !strings.Contains(err.Error(), "999") {
		t.Fatalf("err = %v，預期 SchemaTooNewError", err)
	}
	if _, err := Open(ctx, dsn, nil); !errors.As(err, &tn) {
		t.Fatalf("套用 migration 時 err = %v，預期 SchemaTooNewError", err)
	}
}

// 唯讀的連線（replica 或 default_transaction_read_only）不能當成可寫入。
func TestCheckWritableRejectsReadOnly(t *testing.T) {
	ctx := context.Background()
	dsn := freshSchema(t)
	s, err := Open(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckWritable(ctx); err != nil {
		t.Fatalf("CheckWritable：%v", err)
	}
	_ = s.Close()
	ro, err := Open(ctx, withParam(t, dsn, "default_transaction_read_only", "on"), nil, Options{NoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := ro.CheckWritable(ctx); err == nil {
		t.Fatal("唯讀的連線 CheckWritable 應該失敗")
	}
}

// migration 00005 在 Postgres 以 ALTER 修改 group_members 與 audit_log：既有資料保留、新的 CHECK 生效、Down 可以還原。
func TestCommunityMigrationKeepsData(t *testing.T) {
	ctx := context.Background()
	dsn := freshSchema(t)
	cfg, err := poolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	p, err := newProvider(db, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users (id, display_name, created_at) VALUES ('u1', 'root', now())`,
		`INSERT INTO group_members (user_id, group_name, source, rule_provider, added_at) VALUES
			('u1', 'admins', 'config', '', now()), ('u1', 'moderators', 'manual', '', now()), ('u1', 'contributors', 'rule', 'linuxdo', now())`,
		`INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, at) VALUES ('a1', 'u1', 'group.add', 'user', 'u1', now())`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (user_id, group_name, source, added_at) VALUES ('u1', 'admins', 'cli', now())`); err == nil {
		t.Fatal("00004 時 source cli 應該被 CHECK 擋下")
	}
	s, err := Open(ctx, dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ms, err := s.GroupMemberships(ctx, "u1"); err != nil || len(ms) != 3 {
		t.Fatalf("migration 後的成員資格 = %+v, %v", ms, err)
	}
	if es, err := s.AuditEntries(ctx, 10); err != nil || len(es) != 1 || es[0].Actor != "root" {
		t.Fatalf("migration 後的稽核紀錄 = %+v, %v", es, err)
	}
	if changed, err := s.SetCLIGroup(ctx, "u1", "admins", true); err != nil || !changed {
		t.Fatalf("source cli：%v %v", changed, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (user_id, group_name, source, added_at) VALUES ('u1', 'admins', 'nope', now())`); err == nil {
		t.Error("不認得的 source 要被 CHECK 擋下")
	}
	if _, err := p.DownTo(ctx, 4); err != nil {
		t.Fatalf("Down：%v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_members`).Scan(&n); err != nil || n != 3 {
		t.Errorf("Down 後的成員資格 = %d, %v，預期 3（cli 的被刪除）", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&n); err != nil || n != 1 {
		t.Errorf("Down 後的稽核紀錄 = %d, %v，預期 1（命令列的被刪除）", n, err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("Down 到 0：%v", err)
	}
}

// SQL 注入：使用者可控的字串一律是參數，不會被當成 SQL。
func TestUserInputIsNotSQL(t *testing.T) {
	ctx := context.Background()
	s := openFresh(t, nil)
	evil := []string{`'; DROP TABLE users; --`, `x' OR '1'='1`, `\'; SELECT pg_sleep(5); --`, `%`, `_`}
	for i, e := range evil {
		u, _, err := s.SignIn(ctx, hubstore.ExternalIdentity{Provider: "github", Subject: e, Login: e}, e)
		if err != nil {
			t.Fatalf("SignIn(%q)：%v", e, err)
		}
		if got, err := s.User(ctx, u.ID); err != nil || got.DisplayName != e {
			t.Fatalf("display_name = %q, %v", got.DisplayName, err)
		}
		if _, err := s.SetManualGroup(ctx, "", u.ID, e, true); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.CountUsers(ctx); n != int64(i+1) {
			t.Fatalf("CountUsers = %d", n)
		}
	}
	if _, err := s.User(ctx, `' OR '1'='1`); !errors.Is(err, hubstore.ErrNotFound) {
		t.Errorf("User(注入字串) = %v", err)
	}
	// LIKE 的萬用字元不會被當成萬用字元：前綴查詢用範圍比較。
	if got, err := s.LookupVersions(ctx, "youtube", "%", hubstore.SeenBy{SeeAll: true}); err != nil || len(got) != 0 {
		t.Errorf("LookupVersions(%%) = %d, %v", len(got), err)
	}
	if got, err := s.ListVersions(ctx, hubstore.VersionFilter{Platform: `' OR 1=1 --`, Limit: 10, Seen: hubstore.SeenBy{Visibilities: []string{`') OR true --`}}}); err != nil || len(got) != 0 {
		t.Errorf("ListVersions(注入字串) = %d, %v", len(got), err)
	}
}
