package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/store"
)

// unixClient 回傳透過 Unix socket 連線的 http.Client，不帶任何 cookie 或標頭。
func unixClient(t *testing.T, path string) *http.Client {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func statusOf(t *testing.T, c *http.Client, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s：%v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// 同一個受保護的路由：Unix socket 不帶 cookie 也回 200，TCP 回 401（AUTH-R1）。
func TestSocketNeedsNoCredentialsButTCPDoes(t *testing.T) {
	env := testEnv(t)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	sock := socketPath(env.Vars["NOTESTEME_DATA"])
	uc := unixClient(t, sock)

	if code, body := statusOf(t, uc, "GET", "http://local/api/items", ""); code != 200 {
		t.Errorf("socket GET /api/items = %d %s，預期 200", code, body)
	}
	if code := get(t, srv.Addr(), "/api/items"); code != 401 {
		t.Errorf("TCP GET /api/items = %d，預期 401", code)
	}
	// 非 GET 沒有 CSRF token 也可以（501 代表通過驗證，只是路由還沒實作）。
	if code, _ := statusOf(t, uc, "POST", "http://local/api/estimate", ""); code != 501 {
		t.Errorf("socket POST /api/estimate = %d，預期 501", code)
	}
	if code := get(t, srv.Addr(), "/healthz"); code != 200 {
		t.Errorf("TCP /healthz = %d", code)
	}

	// 權限 0600，鎖檔記錄 socket 路徑。
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket 的 mode = %v，預期 0600 的 socket", fi.Mode())
	}
	info, err := store.ReadLockInfo(env.Vars["NOTESTEME_DATA"])
	if err != nil || info == nil || info.Socket != sock || info.Holder != "serve" {
		t.Errorf("lock info = %+v, %v", info, err)
	}
}

// 從 socket 新增影音：不需要 CSRF，commit 之後發布 item.created。
func TestSocketIngestPublishesItemCreated(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	env.Runner = &ytRunner{}
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	sub, ok := srv.events.bus.Subscribe()
	if !ok {
		t.Fatal("Subscribe 失敗")
	}
	defer sub.Close()

	uc := unixClient(t, socketPath(env.Vars["NOTESTEME_DATA"]))
	code, body := statusOf(t, uc, "POST", "http://local/api/items", `{"url":"https://www.youtube.com/watch?v=abcdefghijk"}`)
	if code != 201 {
		t.Fatalf("POST /api/items = %d %s", code, body)
	}
	select {
	case ev := <-sub.C:
		if ev.Type != events.TypeItemCreated || ev.Item != "yt:abcdefghijk" || ev.ItemID == "" {
			t.Errorf("事件 = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("沒有收到 item.created")
	}
}

// 正常關閉移除 socket；Kill 不移除，下次啟動時清掉；遺留的 socket 不會擋住啟動。
func TestSocketLifecycle(t *testing.T) {
	env := testEnv(t)
	sock := socketPath(env.Vars["NOTESTEME_DATA"])
	exists := func() bool { _, err := os.Lstat(sock); return err == nil }

	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	if !exists() {
		t.Fatal("啟動後應該有 socket")
	}
	srv.Kill()
	if !exists() {
		t.Fatal("Kill 不應該移除 socket")
	}

	srv, err = StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("遺留 socket 時重新啟動失敗：%v", err)
	}
	// 舊的 socket 已被換掉：新的 server 可以連得上。
	if code, _ := statusOf(t, unixClient(t, sock), "GET", "http://local/healthz", ""); code != 200 {
		t.Errorf("healthz = %d", code)
	}
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if exists() {
		t.Error("正常關閉後應該移除 socket")
	}
}

// 路徑上是其他種類的檔案時不刪除，啟動失敗並說明原因；失敗後鎖已釋放。
func TestSocketPathOccupiedByFile(t *testing.T) {
	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	path := socketPath(data)
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := StartServer(t.Context(), env)
	if err == nil || !strings.Contains(err.Error(), "不是 socket") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep" {
		t.Error("不是 socket 的檔案不應該被刪除")
	}
	lock, err := store.LockDir(data)
	if err != nil {
		t.Fatalf("啟動失敗後鎖應該已釋放：%v", err)
	}
	_ = lock.Close()
}

// 路徑超過 sockaddr_un 的上限時，回傳說明原因的錯誤（不靜默略過），並釋放鎖與 TCP listener。
func TestSocketPathTooLong(t *testing.T) {
	env := testEnv(t)
	long := filepath.Join(shortTempDir(t), strings.Repeat("d", sockPathMax))
	env.Vars["NOTESTEME_DATA"] = long
	_, err := StartServer(t.Context(), env)
	if err == nil || !strings.Contains(err.Error(), "太長") || !strings.Contains(err.Error(), "NOTESTEME_DATA") {
		t.Fatalf("err = %v", err)
	}
	lock, lerr := store.LockDir(long)
	if lerr != nil {
		t.Fatalf("啟動失敗後鎖應該已釋放：%v", lerr)
	}
	_ = lock.Close()
}

// Env.Listen 回傳的不是 Unix listener 時拒絕（它不受檔案權限與 uid 檢查保護，卻會以 owner 身分服務）。
func TestSocketRejectsNonUnixListener(t *testing.T) {
	env := testEnv(t)
	env.Listen = func(_, _ string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	_, err := StartServer(t.Context(), env)
	if err == nil || !strings.Contains(err.Error(), "unix listener") {
		t.Fatalf("err = %v", err)
	}
}
