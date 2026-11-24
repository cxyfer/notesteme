package hubcli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"maps"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
)

// pgSchemaDSN 建立隨機名稱的 schema，回傳 search_path 只有它的 DSN；沒有 NOTESTEME_TEST_POSTGRES_DSN 時略過。
func pgSchemaDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("NOTESTEME_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("沒有設定 NOTESTEME_TEST_POSTGRES_DSN，略過需要 Postgres 的測試")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "hubcli_" + hex.EncodeToString(b)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), dsn); err == nil {
			_, _ = c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
			_ = c.Close(context.Background())
		}
	})
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// store.driver: postgres：doctor 在 migrate 之前回報問題且不建立任何資料表，migrate 之後通過。
func TestPostgresMigrateAndDoctor(t *testing.T) {
	dsn := pgSchemaDSN(t)
	vars := maps.Clone(secret)
	vars[hubconfig.EnvStoreDSN] = dsn
	yml := cfg + "store: { driver: postgres }\n"
	e, out, _ := env(t, yml, vars)
	if c := Run(context.Background(), []string{"doctor"}, e); c != ExitError || !strings.Contains(out.String(), "notesteme-hub migrate") {
		t.Fatalf("doctor（沒有 migrate）= %d %s", c, out)
	}
	e, out, errb := env(t, yml, vars)
	if c := Run(context.Background(), []string{"migrate"}, e); c != ExitOK || !strings.Contains(out.String(), "schema 版本：6") {
		t.Fatalf("migrate = %d %s %s", c, out, errb)
	}
	e, out, _ = env(t, yml, vars)
	if c := Run(context.Background(), []string{"doctor"}, e); c != ExitOK || !strings.Contains(out.String(), "0 個帳號") {
		t.Fatalf("doctor = %d %s", c, out)
	}
	// 管理命令直接操作 Postgres。
	e, _, errb = env(t, yml, vars)
	if c := Run(context.Background(), []string{"admin", "suspend", "nobody"}, e); c != ExitError || !strings.Contains(errb.String(), "nobody") {
		t.Errorf("admin suspend（不存在）= %d %s", c, errb)
	}
}

// 連不上 Postgres、S3 時 doctor 回報問題，輸出不含密碼與 secret。
func TestDoctorReportsUnreachableBackends(t *testing.T) {
	vars := maps.Clone(secret)
	vars[hubconfig.EnvStoreDSN] = "postgres://hub:Pg-S3cret-pw@127.0.0.1:1/hub?connect_timeout=1&sslmode=disable"
	vars[hubconfig.EnvS3AccessKeyID] = "AKID"
	vars[hubconfig.EnvS3SecretAccessKey] = "S3-Secret-Value-xyz"
	yml := cfg + "store: { driver: postgres }\nblobs: { driver: s3, s3: { endpoint: \"http://127.0.0.1:1\", bucket: b-1, path_style: true, timeout: 2s } }\n"
	e, out, errb := env(t, yml, vars)
	c := Run(context.Background(), []string{"doctor"}, e)
	all := out.String() + errb.String()
	if c != ExitError || !strings.Contains(all, "資料庫") || !strings.Contains(all, "檔案儲存") {
		t.Fatalf("doctor = %d %s", c, all)
	}
	for _, s := range []string{"Pg-S3cret-pw", "S3-Secret-Value-xyz"} {
		if strings.Contains(all, s) {
			t.Errorf("doctor 的輸出含祕密 %q：%s", s, all)
		}
	}
}

// 真的 S3 相容服務（MinIO）：doctor 確認 bucket 可以使用。
func TestDoctorS3(t *testing.T) {
	endpoint := os.Getenv("NOTESTEME_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("沒有設定 NOTESTEME_TEST_S3_ENDPOINT")
	}
	vars := maps.Clone(secret)
	vars[hubconfig.EnvS3Endpoint] = endpoint
	vars[hubconfig.EnvS3Bucket] = os.Getenv("NOTESTEME_TEST_S3_BUCKET")
	vars[hubconfig.EnvS3AccessKeyID] = os.Getenv("NOTESTEME_TEST_S3_ACCESS_KEY_ID")
	vars[hubconfig.EnvS3SecretAccessKey] = os.Getenv("NOTESTEME_TEST_S3_SECRET_ACCESS_KEY")
	vars[hubconfig.EnvS3PathStyle] = "true"
	e, out, errb := env(t, cfg+"blobs: { driver: s3 }\n", vars)
	if c := Run(context.Background(), []string{"migrate"}, e); c != ExitOK {
		t.Fatalf("migrate = %d %s", c, errb)
	}
	if c := Run(context.Background(), []string{"doctor"}, e); c != ExitOK || !strings.Contains(out.String(), "可以使用") {
		t.Fatalf("doctor = %d %s", c, out)
	}
}
