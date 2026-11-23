package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

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
