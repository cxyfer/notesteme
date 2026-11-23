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

// OPS-R21：vps 部署拒絕 --insecure-no-auth（在密碼檢查之前）；local 時 AUTH-R3 還沒實作，也不默默接受。
func TestServeInsecureNoAuth(t *testing.T) {
	env := testEnv(t)
	delete(env.Vars, "NOTESTEME_PASSWORD")
	env.Vars["NOTESTEME_DEPLOYMENT"] = "vps"
	if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, ErrVPSInsecure) {
		t.Fatalf("vps：%v", err)
	}
	env.Vars["NOTESTEME_DEPLOYMENT"] = "local"
	if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, errInsecureNotImplemented) {
		t.Fatalf("local：%v", err)
	}
	if code, _, stderr := run("serve", "--insecure-no-auth", "--bogus"); code != 2 || !strings.Contains(stderr, "--bogus") {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
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
