package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"net/netip"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"gopkg.in/yaml.v3"
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
		ok, msg, err := CheckVPS(c.env, c.has, yes)
		if err != nil || ok != c.ok || msg != c.want {
			t.Errorf("%d：CheckVPS = %v %q %v", i, ok, msg, err)
		}
	}
	boom := errors.New("cannot open")
	if _, _, err := CheckVPS(map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com"},
		func() (bool, error) { return false, boom }, yes); !errors.Is(err, boom) {
		t.Errorf("讀 DB 失敗要回報：%v", err)
	}
	// trusted_proxies 是空的：Caddy 後面所有登入共用同一個來源位址（AUTH-R6），不能對外開放。
	ready := map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com", "NOTESTEME_PASSWORD": "pw"}
	if ok, msg, err := CheckVPS(ready, called, no); ok || msg != MsgVPSTrustedProxies || err != nil {
		t.Errorf("沒有 trusted_proxies：%v %q %v", ok, msg, err)
	}
	if _, _, err := CheckVPS(ready, called, func() (bool, error) { return false, boom }); !errors.Is(err, boom) {
		t.Errorf("載入設定失敗要回報：%v", err)
	}
	// 前面的檢查沒過時不讀設定（順序同 OPS-R20）。
	if _, msg, _ := CheckVPS(map[string]string{"NOTESTEME_DEPLOYMENT": "vps", "NOTESTEME_DOMAIN": "notes.example.com"}, no,
		func() (bool, error) { t.Fatal("密碼檢查沒過時不檢查 trusted_proxies"); return false, nil }); msg != MsgVPSPassword {
		t.Errorf("順序：%q", msg)
	}
}

// compose 的 vps profile 預設的 trusted_proxies 必須正好是 caddy 的固定位址，兩邊不能各改各的（OPS-R19、R20）。
func TestComposeTrustsCaddy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Networks    map[string]struct {
				IPv4 string `yaml:"ipv4_address"`
			} `yaml:"networks"`
		} `yaml:"services"`
		Networks map[string]struct {
			IPAM struct {
				Config []struct {
					Subnet string `yaml:"subnet"`
				} `yaml:"config"`
			} `yaml:"ipam"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	caddy := doc.Services["caddy"].Networks["default"].IPv4
	if caddy+"/32" != DefaultVPSTrustedProxies {
		t.Fatalf("caddy 的位址 %q 與 DefaultVPSTrustedProxies %q 不一致", caddy, DefaultVPSTrustedProxies)
	}
	subnet := netip.MustParsePrefix(doc.Networks["default"].IPAM.Config[0].Subnet)
	if !subnet.Contains(netip.MustParseAddr(caddy)) {
		t.Fatalf("caddy 的位址 %s 不在網段 %s 內", caddy, subnet)
	}
	want := "${NOTESTEME_TRUSTED_PROXIES:-" + DefaultVPSTrustedProxies + "}"
	for _, svc := range []string{"notesteme", "vps-check"} {
		if got := doc.Services[svc].Environment["NOTESTEME_TRUSTED_PROXIES"]; got != want {
			t.Errorf("%s 的 NOTESTEME_TRUSTED_PROXIES = %q，預期 %q", svc, got, want)
		}
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

// SEC-R12：失敗的使用更新 last_used_at；失效的使用另外記 expired_at；之後的成功不清掉失效，只有重新上傳會。
func TestCallsRecorderCookieStatus(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	c := &Calls{Store: s, Clock: clk}
	w := s.Write()
	if err := w.UpsertSecret(ctx, CookieKind, "bilibili", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	status := func() store.SecretStatus {
		t.Helper()
		list, err := s.Read().SecretsStatus(ctx, CookieKind)
		if err != nil || len(list) != 1 {
			t.Fatalf("status = %v, %v", list, err)
		}
		return list[0]
	}
	clk.Advance(time.Minute)
	c.CookieFailed(ctx, "bilibili", false)
	if st := status(); st.LastUsedAt == nil || !st.LastUsedAt.Equal(clk.Now()) || st.ExpiredAt != nil || st.LastOKAt != nil {
		t.Fatalf("一般失敗：%+v", st)
	}
	clk.Advance(time.Minute)
	c.CookieFailed(ctx, "bilibili", true)
	if st := status(); st.ExpiredAt == nil || !st.ExpiredAt.Equal(clk.Now()) || !st.LastUsedAt.Equal(clk.Now()) {
		t.Fatalf("失效：%+v", st)
	}
	clk.Advance(time.Minute)
	c.CookieOK(ctx, "bilibili")
	if st := status(); st.ExpiredAt == nil || st.LastOKAt == nil || !st.LastOKAt.Equal(clk.Now()) || !st.LastUsedAt.Equal(clk.Now()) {
		t.Fatalf("成功後：%+v", st)
	}
	c.CookieFailed(ctx, "bilibili", true)
	clk.Advance(time.Minute)
	if err := w.UpsertSecret(ctx, CookieKind, "bilibili", []byte("n2"), []byte("c2")); err != nil {
		t.Fatal(err)
	}
	if st := status(); st.ExpiredAt != nil || !st.UpdatedAt.Equal(clk.Now()) || st.LastUsedAt == nil {
		t.Fatalf("重新上傳後：%+v", st)
	}
	// 取消的 context 與 nil 不記錄；寫入失敗只記 log。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	c.CookieFailed(cctx, "bilibili", true)
	var nilCalls *Calls
	nilCalls.CookieFailed(ctx, "bilibili", true)
	if st := status(); st.ExpiredAt != nil {
		t.Fatalf("取消後仍記錄了：%+v", st)
	}
	_ = s.Close()
	c.CookieFailed(ctx, "bilibili", false)
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
