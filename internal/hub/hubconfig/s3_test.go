package hubconfig

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// store.dsn：設定檔可以寫不含密碼的 DSN，環境變數優先；含密碼時拒絕，錯誤訊息不回顯 DSN。
func TestStoreDSNFromFile(t *testing.T) {
	cfg, err := load(t, minimal+"store: { driver: postgres, dsn: \"postgres://hub@db:5432/hub?sslmode=disable\" }\n", nil)
	if err != nil || cfg.StoreDSN != "postgres://hub@db:5432/hub?sslmode=disable" {
		t.Fatalf("store.dsn：%q, %v", cfg.StoreDSN, err)
	}
	cfg, err = load(t, minimal+"store: { driver: postgres, dsn: \"host=db user=hub\" }\n", map[string]string{EnvStoreDSN: "postgres://hub:pw@db/hub"})
	if err != nil || cfg.StoreDSN != "postgres://hub:pw@db/hub" {
		t.Fatalf("環境變數優先：%q, %v", cfg.StoreDSN, err)
	}
	for _, dsn := range []string{
		"postgres://hub:Secr3tPW@db/hub",
		"postgresql://hub@db/hub?password=Secr3tPW",
		"host=db user=hub password=Secr3tPW",
		"postgres://hub:Secr3tPW@[::1/hub",
	} {
		_, err := load(t, minimal+fmt.Sprintf("store: { driver: postgres, dsn: %q }\n", dsn), nil)
		if err == nil || !strings.Contains(err.Error(), "store.dsn") || strings.Contains(err.Error(), "Secr3tPW") {
			t.Errorf("含密碼的 store.dsn %q：err = %v", dsn, err)
		}
	}
}

func s3Env(extra map[string]string) map[string]string {
	env := map[string]string{EnvS3AccessKeyID: "AKID", EnvS3SecretAccessKey: "SuperSecretValue"}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestS3Config(t *testing.T) {
	yml := minimal + `blobs:
  driver: s3
  s3:
    endpoint: http://minio:9000/
    bucket: notesteme-hub
    prefix: /blobs/
    path_style: true
    timeout: 30s
`
	cfg, err := load(t, yml, s3Env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := S3Config{Endpoint: "http://minio:9000", Region: DefaultS3Region, Bucket: "notesteme-hub", Prefix: "blobs", PathStyle: true,
		AccessKeyID: "AKID", SecretAccessKey: "SuperSecretValue", Timeout: 30 * time.Second}
	if cfg.S3 != want {
		t.Fatalf("S3 = %+v", cfg.S3)
	}
	// 環境變數優先於設定檔。
	cfg, err = load(t, yml, s3Env(map[string]string{EnvS3Endpoint: "https://acc.r2.cloudflarestorage.com", EnvS3Region: "auto",
		EnvS3Bucket: "other", EnvS3PathStyle: "false"}))
	if err != nil || cfg.S3.Endpoint != "https://acc.r2.cloudflarestorage.com" || cfg.S3.Region != "auto" || cfg.S3.Bucket != "other" || cfg.S3.PathStyle {
		t.Fatalf("環境變數：%+v, %v", cfg.S3, err)
	}
	// String 與 %+v、%#v 都不輸出 secret。
	for _, s := range []string{cfg.S3.String(), fmt.Sprintf("%+v", cfg.S3), fmt.Sprintf("%#v", cfg.S3), fmt.Sprintf("%v", *cfg)} {
		if strings.Contains(s, "SuperSecretValue") {
			t.Errorf("輸出含 secret：%s", s)
		}
	}
	// 只有環境變數也可以；預設逾時。
	cfg, err = load(t, minimal+"blobs: { driver: s3 }\n", s3Env(map[string]string{EnvS3Bucket: "b-1"}))
	if err != nil || cfg.S3.Timeout != DefaultS3Timeout || cfg.S3.Endpoint != "" {
		t.Fatalf("只有環境變數：%+v, %v", cfg.S3, err)
	}
}

func TestS3ConfigErrors(t *testing.T) {
	cases := map[string]struct {
		yml  string
		env  map[string]string
		want string
	}{
		"沒有 bucket":    {"blobs: { driver: s3 }\n", s3Env(nil), EnvS3Bucket},
		"bucket 格式":    {"blobs: { driver: s3, s3: { bucket: Bad_Bucket } }\n", s3Env(nil), "bucket"},
		"沒有 secret":    {"blobs: { driver: s3, s3: { bucket: b-1 } }\n", map[string]string{EnvS3AccessKeyID: "AKID"}, EnvS3SecretAccessKey},
		"endpoint 有帳密": {"blobs: { driver: s3, s3: { bucket: b-1, endpoint: \"https://u:SuperSecretValue@x\" } }\n", s3Env(nil), "endpoint"},
		"endpoint 協定":  {"blobs: { driver: s3, s3: { bucket: b-1, endpoint: \"ftp://x\" } }\n", s3Env(nil), "endpoint"},
		"timeout":      {"blobs: { driver: s3, s3: { bucket: b-1, timeout: soon } }\n", s3Env(nil), "blobs.s3.timeout"},
		"secret 不在設定檔": {"blobs: { driver: s3, s3: { bucket: b-1, secret_access_key: x } }\n", s3Env(nil), "secret_access_key"},
	}
	for name, c := range cases {
		_, err := load(t, minimal+c.yml, c.env)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "SuperSecretValue") {
			t.Errorf("%s：err = %v，預期含 %q 且不含 secret", name, err, c.want)
		}
	}
	// fs 時不檢查 s3 的欄位。
	if _, err := load(t, minimal+"blobs: { s3: { bucket: b-1 } }\n", nil); err != nil {
		t.Errorf("fs 時不檢查 s3：%v", err)
	}
}

// deploy/docker/hub-config.example.yaml 加上 compose.hub.yaml 提供的環境變數可以載入（postgres＋s3）。
func TestDeployExampleConfig(t *testing.T) {
	cfg, err := Load(Sources{File: "../../../deploy/docker/hub-config.example.yaml", Env: map[string]string{
		EnvStoreDSN:   "postgres://notesteme_hub:pw@hub-postgres:5432/notesteme_hub?sslmode=disable",
		EnvS3Endpoint: "http://hub-minio:9000", EnvS3Bucket: "notesteme-hub", EnvS3PathStyle: "true",
		EnvS3AccessKeyID: "notesteme-hub", EnvS3SecretAccessKey: "x",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StoreDriver != StorePostgres || cfg.BlobsDriver != BlobsS3 || !cfg.S3.PathStyle || cfg.S3.Endpoint != "http://hub-minio:9000" {
		t.Errorf("cfg = %s %s %v", cfg.StoreDriver, cfg.BlobsDriver, cfg.S3)
	}
}
