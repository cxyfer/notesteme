package hubcli

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
)

func env(t *testing.T, yml string, extra map[string]string) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "notesteme-hub.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{hubconfig.EnvConfig: p, hubconfig.EnvData: filepath.Join(dir, "data")}
	for k, v := range extra {
		vars[k] = v
	}
	var out, errb bytes.Buffer
	return Env{Vars: vars, Stdout: &out, Stderr: &errb}, &out, &errb
}

const cfg = "public_url: https://hub.example.org\noauth:\n  github: { client_id: gh }\n"

var secret = map[string]string{
	"NOTESTEME_HUB_OAUTH_GITHUB_CLIENT_SECRET": "s",
	"NOTESTEME_HUB_SESSION_KEY":                "0123456789abcdef0123456789abcdef",
	"NOTESTEME_HUB_MASTER_KEY":                 "fedcba9876543210fedcba9876543210",
}

func TestUsageAndVersion(t *testing.T) {
	e, out, errb := env(t, cfg, nil)
	if c := Run(context.Background(), nil, e); c != ExitUsage || !strings.Contains(errb.String(), "serve") {
		t.Errorf("沒有子命令 = %d %s", c, errb)
	}
	if c := Run(context.Background(), []string{"nope"}, e); c != ExitUsage {
		t.Errorf("未知子命令 = %d", c)
	}
	if c := Run(context.Background(), []string{"--version"}, e); c != ExitOK || !strings.HasPrefix(out.String(), "notesteme-hub ") {
		t.Errorf("--version = %d %q", c, out)
	}
}

func TestMigrateAndDoctor(t *testing.T) {
	e, out, errb := env(t, cfg, secret)
	if c := Run(context.Background(), []string{"migrate"}, e); c != ExitOK || !strings.Contains(out.String(), "schema 版本：5") {
		t.Fatalf("migrate = %d %s %s", c, out, errb)
	}
	out.Reset()
	if c := Run(context.Background(), []string{"doctor"}, e); c != ExitOK || !strings.Contains(out.String(), "登入方式：github") || !strings.Contains(out.String(), "0 個帳號") {
		t.Errorf("doctor = %d %s", c, out)
	}
	// 沒有 secret：doctor 指出缺少的環境變數。
	e2, out2, _ := env(t, cfg, nil)
	if c := Run(context.Background(), []string{"doctor"}, e2); c != ExitError || !strings.Contains(out2.String(), "NOTESTEME_HUB_OAUTH_GITHUB_CLIENT_SECRET") {
		t.Errorf("doctor（缺 secret）= %d %s", c, out2)
	}
	// 沒有 session 金鑰：doctor 指出缺少的環境變數。
	e3, out3, _ := env(t, cfg, map[string]string{"NOTESTEME_HUB_OAUTH_GITHUB_CLIENT_SECRET": "s"})
	if c := Run(context.Background(), []string{"doctor"}, e3); c != ExitError || !strings.Contains(out3.String(), "NOTESTEME_HUB_SESSION_KEY") {
		t.Errorf("doctor（缺 session 金鑰）= %d %s", c, out3)
	}
}

func TestConfigError(t *testing.T) {
	e, _, errb := env(t, "public_url: http://hub.example.org\n", nil)
	if c := Run(context.Background(), []string{"serve"}, e); c != ExitError || !strings.Contains(errb.String(), "public_url 必須是 https") {
		t.Errorf("serve = %d %s", c, errb)
	}
}

func TestAdminArgs(t *testing.T) {
	e, _, errb := env(t, cfg, nil)
	cases := map[string]int{
		"admin":       ExitUsage,
		"admin grant": ExitUsage,
		"admin grant --provider gitlab --subject 1": ExitUsage,
		"admin grant --provider github --subject 1": ExitError, // 尚未實作
		"admin suspend":    ExitUsage,
		"admin suspend u1": ExitError,
		"admin delete u1":  ExitUsage,
	}
	for line, want := range cases {
		errb.Reset()
		if c := Run(context.Background(), strings.Fields(line), e); c != want {
			t.Errorf("%s = %d，預期 %d：%s", line, c, want, errb)
		}
	}
}

func TestServe(t *testing.T) {
	e, _, errb := env(t, cfg, secret)
	addr := make(chan string, 1)
	e.Listen = func(network, a string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			addr <- ln.Addr().String()
		}
		return ln, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"serve"}, e) }()
	var a string
	select {
	case a = <-addr:
	case c := <-done:
		t.Fatalf("serve 提早結束 = %d %s", c, errb)
	case <-time.After(10 * time.Second):
		t.Fatal("serve 沒有開始監聽")
	}
	resp, err := http.Get("http://" + a + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/v1/me（沒有登入）= %d，預期 401", resp.StatusCode)
	}
	cancel()
	if c := <-done; c != ExitOK {
		t.Errorf("serve 結束碼 = %d %s", c, errb)
	}
}

// doctor 不修改任何東西：還沒有 migrate 時回報問題，也不建立資料庫。
func TestDoctorBeforeMigrate(t *testing.T) {
	e, out, _ := env(t, cfg, secret)
	if c := Run(context.Background(), []string{"doctor"}, e); c != ExitError || !strings.Contains(out.String(), "notesteme-hub migrate") {
		t.Errorf("doctor（沒有 migrate）= %d %s", c, out)
	}
	if _, err := os.Stat(filepath.Join(e.Vars[hubconfig.EnvData], "notesteme-hub.db")); err == nil {
		t.Error("doctor 不應建立資料庫")
	}
}
