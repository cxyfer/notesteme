package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestValidDomain(t *testing.T) {
	good := []string{"notes.example.com", "notes.example.com.", "a-b.example.org", "NOTES.Example.COM"}
	bad := []string{"", "localhost", "LOCALHOST.", "app.localhost", "127.0.0.1", "::1", "[::1]", "93.184.216.34",
		"notes", "https://notes.example.com", "notes.example.com:443", "notes.example.com/x", "user@notes.example.com",
		"notes..example.com", "-notes.example.com", "notes-.example.com", "notes example.com", "notes.example.com\n"}
	for _, d := range good {
		if !ValidDomain(d) {
			t.Errorf("%q 應該接受", d)
		}
	}
	for _, d := range bad {
		if ValidDomain(d) {
			t.Errorf("%q 應該拒絕", d)
		}
	}
}

func TestCheckVPSOrder(t *testing.T) {
	no := func() (bool, error) { return false, nil }
	yes := func() (bool, error) { return true, nil }
	called := func() (bool, error) { t.Fatal("環境變數有密碼時不讀 DB"); return false, nil }
	cases := []struct {
		env  map[string]string
		has  func() (bool, error)
		ok   bool
		want string
	}{
		{map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "localhost"}, no, false, MsgVPSDomain},
		{map[string]string{"NOTESTEME_DEPLOYMENT": "local", "NOTESTEME_DOMAIN": "notes.example.com"}, no, false, MsgVPSDeployment},
		{map[string]string{"NOTESTEME_DOMAIN": "notes.example.com"}, no, false, MsgVPSDeployment},
		{map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com"}, no, false, MsgVPSPassword},
		{map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com"}, yes, true, MsgVPSOK},
		{map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com", "NOTESTEME_PASSWORD": "pw"}, called, true, MsgVPSOK},
	}
	for i, c := range cases {
		ok, msg, err := CheckVPS(c.env, c.has)
		if err != nil || ok != c.ok || msg != c.want {
			t.Errorf("%d：CheckVPS = %v %q %v", i, ok, msg, err)
		}
	}
	boom := errors.New("cannot open")
	if _, _, err := CheckVPS(map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com"},
		func() (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Errorf("讀 DB 失敗要回報：%v", err)
	}
}

func TestPasswordInDB(t *testing.T) {
	ctx := context.Background()
	if has, err := PasswordInDB(ctx, t.TempDir()); err != nil || has {
		t.Fatalf("沒有 DB：%v %v", has, err)
	}
	dir := t.TempDir()
	s, err := store.Open(ctx, dir, clock.NewFake(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if has, err := PasswordInDB(ctx, dir); err != nil || has {
		t.Fatalf("DB 沒有密碼（serve 執行中）：%v %v", has, err)
	}
	if err := s.SetSetting(ctx, passwordSetting, "argon2id$hash"); err != nil {
		t.Fatal(err)
	}
	if has, err := PasswordInDB(ctx, dir); err != nil || !has {
		t.Fatalf("DB 有密碼（serve 執行中）：%v %v", has, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// 唯讀掛載：目錄與檔案都不能寫，也沒有 -wal／-shm。
	if err := os.Chmod(filepath.Join(dir, store.DBFileName), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if has, err := PasswordInDB(ctx, dir); err != nil || !has {
		t.Fatalf("唯讀的資料目錄：%v %v", has, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != store.DBFileName && e.Name() != "notesteme.lock" {
			t.Errorf("deploy-check 不能在資料目錄建立檔案：%s", e.Name())
		}
	}

	// 不是 SQLite 的檔案：回報錯誤，不當成沒有密碼。
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, store.DBFileName), []byte("not a database at all, just text......."), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PasswordInDB(ctx, bad); err == nil {
		t.Error("壞掉的 DB 要回報錯誤")
	}
}

func TestCallsRecorder(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	c := &Calls{Store: s, Clock: clk}
	c.ToolCall(ctx, ToolYtDlp, true)
	c.ToolCall(ctx, ToolYtDlp, false)
	if err := s.Write().UpsertSecret(ctx, CookieKind, "youtube", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	c.CookieOK(ctx, "youtube")
	calls, fails, _ := s.Read().ToolCallTotals(ctx, ToolYtDlp, time.Time{})
	if calls != 2 || fails != 1 {
		t.Fatalf("統計 = %d/%d", fails, calls)
	}
	fr, _ := s.Read().SecretsFreshness(ctx, CookieKind)
	if fr[0].LastOKAt == nil || !fr[0].LastOKAt.Equal(clk.Now()) {
		t.Fatalf("cookie = %+v", fr)
	}
	// 取消的 context 與 nil 不記錄。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	c.ToolCall(cctx, ToolYtDlp, true)
	c.CookieOK(cctx, "youtube")
	var nilCalls *Calls
	nilCalls.ToolCall(ctx, ToolYtDlp, true)
	nilCalls.CookieOK(ctx, "youtube")
	if calls, _, _ := s.Read().ToolCallTotals(ctx, ToolYtDlp, time.Time{}); calls != 2 {
		t.Errorf("取消後仍記錄了：%d", calls)
	}
	// 寫入失敗只記 log。
	_ = s.Close()
	c.ToolCall(ctx, ToolYtDlp, true)
	c.CookieOK(ctx, "youtube")
}

func TestUsagePercent(t *testing.T) {
	cases := []struct {
		blocks, free, avail uint64
		want                int
	}{
		{100, 100, 95, 0}, {100, 7, 2, 98}, {100, 10, 10, 90}, {100, 9, 9, 91}, {100, 9, 8, 92}, {0, 0, 0, 0}, {10, 20, 5, 0},
	}
	for _, c := range cases {
		if got := usagePercent(c.blocks, c.free, c.avail); got != c.want {
			t.Errorf("usagePercent(%d, %d, %d) = %d，預期 %d", c.blocks, c.free, c.avail, got, c.want)
		}
	}
	if p, err := DiskUsagePercent(t.TempDir()); err != nil || p < 0 || p > 100 {
		t.Errorf("DiskUsagePercent = %d，%v", p, err)
	}
	if _, err := DiskUsagePercent("/no/such/dir"); err == nil {
		t.Error("不存在的目錄要回報錯誤")
	}
}
