package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/ops"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestParseServeArgs(t *testing.T) {
	if o, err := ParseServeArgs(nil); err != nil || o.InsecureNoAuth {
		t.Fatalf("沒有旗標：%+v %v", o, err)
	}
	if o, err := ParseServeArgs([]string{"--insecure-no-auth"}); err != nil || !o.InsecureNoAuth {
		t.Fatalf("--insecure-no-auth：%+v %v", o, err)
	}
	if _, err := ParseServeArgs([]string{"--insecure-no-auth", "--bogus"}); err == nil {
		t.Fatal("不認得的旗標要拒絕")
	}
}

// OPS-R21、AUTH-R3：vps 部署拒絕 --insecure-no-auth（在密碼檢查之前）；local 部署在 listen 是 loopback、
// 沒有反向代理與 OIDC 設定、也沒有任何密碼時才能啟動（之前 local 回「尚未實作」，AUTH-R3 實作後改為可以啟動）。
func TestServeInsecureNoAuth(t *testing.T) {
	env := testEnv(t)
	delete(env.Vars, "NOTESTEME_PASSWORD")
	env.Vars["NOTESTEME_DEPLOYMENT"] = "vps"
	if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, ErrVPSInsecure) {
		t.Fatalf("vps：%v", err)
	}
	// vps 優先於其他條件：listen 不是 loopback 時仍回 OPS-R21 的訊息。
	env.Vars["NOTESTEME_LISTEN"] = "0.0.0.0:8080"
	if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, ErrVPSInsecure) {
		t.Fatalf("vps + 0.0.0.0：%v", err)
	}
	env.Vars["NOTESTEME_DEPLOYMENT"] = "local"
	for _, listen := range []string{"0.0.0.0:8080", ":8080", "[::]:8080", "192.168.1.10:8080", "notes.example.com:8080", "localhost.evil.com:8080"} {
		env.Vars["NOTESTEME_LISTEN"] = listen
		_, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true})
		if err == nil || !strings.Contains(err.Error(), "只能在只監聽本機") || !strings.Contains(err.Error(), listen) {
			t.Errorf("listen %s：%v", listen, err)
		}
	}
	env.Vars["NOTESTEME_LISTEN"] = "127.0.0.1:8080"
	// 有 NOTESTEME_PASSWORD：拒絕（不默默停用驗證），也不寫入密碼。
	env.Vars["NOTESTEME_PASSWORD"] = "pw"
	if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, ErrInsecureWithPassword) {
		t.Fatalf("有 NOTESTEME_PASSWORD：%v", err)
	}
	delete(env.Vars, "NOTESTEME_PASSWORD")
	// 反向代理與 OIDC 的設定。
	cfgPath := env.Vars["NOTESTEME_CONFIG"]
	for name, yaml := range map[string]string{
		"trusted_proxies": "trusted_proxies: [\"10.0.0.0/8\"]\n",
		"auth.proxy":      "trusted_proxies: [\"10.0.0.0/8\"]\nauth:\n  proxy:\n    user_header: Remote-User\n    allowed_users: [me]\n",
		"auth.oidc": "auth:\n  oidc:\n    issuer: https://id.example.com\n    client_id: c\n    redirect_url: https://n.example.com/api/auth/oidc/callback\n" +
			"    allowed_subjects: [s]\n",
	} {
		if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		env.Vars["NOTESTEME_OIDC_CLIENT_SECRET"] = "x"
		if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, ErrInsecureWithProxy) {
			t.Errorf("%s：%v", name, err)
		}
		delete(env.Vars, "NOTESTEME_OIDC_CLIENT_SECRET")
	}
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}
	// 沒有旗標也沒有密碼：AUTH-R3 的訊息不變。
	if _, err := StartServerWith(t.Context(), env, ServeOptions{}); err == nil || err.Error() != "尚未設定登入密碼。請設定 NOTESTEME_PASSWORD 後重新啟動。" {
		t.Fatalf("沒有密碼：%v", err)
	}
	if code, _, stderr := run("serve", "--insecure-no-auth", "--bogus"); code != 2 || !strings.Contains(stderr, "--bogus") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

// LoopbackListen 只接受 localhost 與 loopback 的 IP 字面值。
func TestLoopbackListen(t *testing.T) {
	for listen, want := range map[string]bool{
		"127.0.0.1:8080": true, "127.0.0.2:1": true, "[::1]:8080": true, "localhost:8080": true, "LOCALHOST:1": true,
		"0.0.0.0:8080": false, ":8080": false, "[::]:8080": false, "10.0.0.1:80": false, "localhost.evil.com:80": false,
		"[::ffff:127.0.0.1]:80": false, "[::1%lo]:80": false, "127.0.0.1": false, "": false, "evil.com:80": false,
	} {
		if got := LoopbackListen(listen); got != want {
			t.Errorf("LoopbackListen(%q) = %v，預期 %v", listen, got, want)
		}
	}
}

// serve 登記 backup.daily 與 alerts，GET /api/system 回報告警與每日備份的狀態；standalone 不登記。
func TestServeRegistersOpsAndReportsStatus(t *testing.T) {
	env := testEnv(t)
	clk := env.Clock.(*clock.Fake)
	env.Test.ManualWorker = true
	env.Test.DiskUsage = func(string) (int, error) { return 97, nil }
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	ctx := context.Background()
	if got := strings.Join(srv.scheduler.Kinds(), ","); !strings.Contains(got, ops.BackupKind) || !strings.Contains(got, ops.AlertsKind) {
		t.Fatalf("kinds = %s", got)
	}
	if err := srv.RunUntilIdle(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := srv.systemInfo(env).Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 程式與 schema 的版本（OPS-R22）。
	latest, err := store.LatestSchemaVersion(nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != Version() || v.SchemaVersion != latest {
		t.Fatalf("version = %q、schema_version = %d，預期 %q、%d", v.Version, v.SchemaVersion, Version(), latest)
	}
	if len(v.Alerts) != 1 || v.Alerts[0].Kind != ops.KindDiskUsage || v.Alerts[0].Key != "/data" || !v.Alerts[0].Since.Equal(clk.Now()) {
		t.Fatalf("alerts = %+v", v.Alerts)
	}
	want := time.Date(2026, 11, 13, 3, 0, 0, 0, time.UTC)
	if v.Backup.NextRunAt == nil || !v.Backup.NextRunAt.Equal(want) || v.Backup.LastOKAt != nil || v.Backup.LastError != nil {
		t.Fatalf("backup = %+v", v.Backup)
	}

	// 跨過 03:00：每日備份寫到 <資料目錄>/backups。
	clk.Set(want)
	if err := srv.RunUntilIdle(ctx); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(env.Vars["NOTESTEME_DATA"], "backups")
	if _, err := os.Stat(filepath.Join(dir, "notesteme-backup-20261113T030000Z.tar.gz")); err != nil {
		t.Fatal(err)
	}
	v, _ = srv.systemInfo(env).Info(ctx)
	if v.Backup.LastOKAt == nil || !v.Backup.LastOKAt.Equal(want) {
		t.Fatalf("成功後的 backup = %+v", v.Backup)
	}

	// 測試 hook。
	if err := srv.SetCookieUsedAt(ctx, "youtube", want); err == nil {
		t.Error("沒有上傳的 cookie 要回報")
	}
	if err := srv.SetToolCalls(ctx, ops.ToolYtDlp, want, 3, 1); err != nil {
		t.Fatal(err)
	}
}

func TestStandaloneDoesNotRegisterOps(t *testing.T) {
	env := testEnv(t)
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	for _, k := range sa.core.scheduler.Kinds() {
		if k == ops.BackupKind || k == ops.AlertsKind {
			t.Errorf("standalone 不能登記 %s", k)
		}
	}
}

// 沒有 store 時（不會發生在 serve）alerts 是空陣列、backup 欄位都是 null。
func TestOpsStatusWithoutStore(t *testing.T) {
	v := &api.SystemView{}
	if err := (systemInfo{}).opsStatus(t.Context(), v); err != nil || v.Alerts == nil || len(v.Alerts) != 0 {
		t.Fatalf("%+v %v", v, err)
	}
}
