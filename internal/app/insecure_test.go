package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// lockedBuffer 是可以並行寫入與讀取的 log 輸出。
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// AUTH-R3：--insecure-no-auth 的實際行為。每個請求等同 owner；只接受 loopback 的 Host；
// 跨站的寫入被擋下；偽造的 X-Forwarded-For、proxy 標頭不影響；log 持續警告；UI 由 /api/auth/methods 得知。
func TestInsecureNoAuthServer(t *testing.T) {
	env := testEnv(t)
	delete(env.Vars, "NOTESTEME_PASSWORD")
	logs := &lockedBuffer{}
	env.Log = logs
	srv, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	base := "http://" + srv.Addr()

	do := func(method, path, body string, hdr map[string]string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			if k == "Host" {
				req.Host = v
				continue
			}
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}

	if !strings.Contains(logs.String(), InsecureWarning) {
		t.Errorf("啟動時沒有警告：%s", logs)
	}
	// 沒有任何憑證也能讀寫（等同 owner，含 admin 的路由）。
	for _, p := range []string{"/api/items", "/api/settings/server", "/api/auth/sessions"} {
		if resp, body := do("GET", p, "", nil); resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("GET %s = %d %s", p, resp.StatusCode, body)
		}
	}
	if resp, body := do("POST", "/api/queue/pause", "", nil); resp.StatusCode/100 != 2 {
		t.Errorf("POST 沒有 Origin（curl）= %d %s", resp.StatusCode, body)
	}
	if resp, body := do("POST", "/api/queue/resume", "", map[string]string{"Origin": base, "Sec-Fetch-Site": "same-origin"}); resp.StatusCode/100 != 2 {
		t.Errorf("同源的 POST = %d %s", resp.StatusCode, body)
	}
	// UI 的警告欄位（免驗證的登入方式路由）。
	if resp, body := do("GET", "/api/auth/methods", "", nil); resp.StatusCode != 200 || !strings.Contains(body, `"insecure_no_auth":true`) {
		t.Errorf("methods = %d %s", resp.StatusCode, body)
	}
	// 沒有密碼可以登入：409 no_password，不是 500。
	if resp, body := do("POST", "/api/auth/login", `{"password":"x"}`, nil); resp.StatusCode != http.StatusConflict || !strings.Contains(body, `"no_password"`) {
		t.Errorf("login = %d %s", resp.StatusCode, body)
	}
	if resp, body := do("POST", "/api/auth/password", `{"current_password":"x","new_password":"y"}`, nil); resp.StatusCode != http.StatusConflict || !strings.Contains(body, `"no_password"`) {
		t.Errorf("修改密碼 = %d %s", resp.StatusCode, body)
	}
	// /add 不導回登入頁。
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := noRedirect.Get(base + "/add?url=x"); err != nil || resp.StatusCode == http.StatusSeeOther {
		t.Errorf("/add = %v %v", resp, err)
	} else {
		resp.Body.Close()
	}

	// DNS rebinding：Host 不是 loopback 的請求（含 UI 與 /healthz 以外的路徑）一律 403。
	for _, host := range []string{"evil.example.com", "evil.example.com:" + strings.Split(srv.Addr(), ":")[1], "localhost.evil.com",
		"127.0.0.1.nip.io", "10.0.0.1", "[::ffff:127.0.0.1]"} {
		for _, p := range []string{"/api/items", "/", "/library", "/mcp"} {
			if resp, body := do("GET", p, "", map[string]string{"Host": host}); resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "insecure_host") {
				t.Errorf("Host %q %s = %d %s", host, p, resp.StatusCode, body)
			}
		}
	}
	for _, host := range []string{"localhost", "LOCALHOST:1234", "127.0.0.1", "[::1]:8080"} {
		if resp, body := do("GET", "/api/items", "", map[string]string{"Host": host}); resp.StatusCode != 200 {
			t.Errorf("Host %q = %d %s", host, resp.StatusCode, body)
		}
	}

	// CSRF：其他網站（含同一台電腦的其他 port）送來的寫入一律 403；GET 不受影響。
	for _, h := range []map[string]string{
		{"Origin": "https://evil.example.com"},
		{"Origin": "http://localhost:1"},
		{"Origin": "null"},
		{"Origin": base + "/"},
		{"Origin": "http://user@" + srv.Addr()},
		{"Origin": "file://" + srv.Addr()},
		{"Origin": "garbage"},
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": base, "Sec-Fetch-Site": "cross-site"},
	} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if resp, body := do(method, "/api/queue/pause", "", h); resp.StatusCode != http.StatusForbidden || !strings.Contains(body, "csrf_invalid") {
				t.Errorf("%s %v = %d %s", method, h, resp.StatusCode, body)
			}
		}
		if resp, _ := do("GET", "/api/items", "", h); resp.StatusCode != 200 {
			t.Errorf("GET %v = %d", h, resp.StatusCode)
		}
	}
	// 偽造的標頭不改變任何事（沒有 trusted_proxies、沒有反向代理驗證）。
	if resp, body := do("POST", "/api/queue/resume", "", map[string]string{"X-Forwarded-For": "8.8.8.8", "Remote-User": "admin",
		"X-Forwarded-Host": "evil.example.com", "X-Forwarded-Proto": "https"}); resp.StatusCode/100 != 2 {
		t.Errorf("偽造標頭 = %d %s", resp.StatusCode, body)
	}
	// bearer token 仍然只會縮小權限：無效的 token 是 401，不會被放大成 owner。
	if resp, _ := do("GET", "/api/items", "", map[string]string{"Authorization": "Bearer nst_bogus"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("無效的 token = %d", resp.StatusCode)
	}

	// 持續警告：每小時再寫一次。
	clk := env.Clock.(*clock.Fake)
	before := strings.Count(logs.String(), InsecureWarning)
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(logs.String(), InsecureWarning) <= before {
		if time.Now().After(deadline) {
			t.Fatalf("一小時後沒有再警告：%d 次", strings.Count(logs.String(), InsecureWarning))
		}
		clk.Advance(insecureWarnEvery)
		time.Sleep(10 * time.Millisecond)
	}
}

// AUTH-R3：DB 已經有密碼時，--insecure-no-auth 拒絕啟動（不默默停用 owner 設好的驗證），密碼不受影響。
func TestInsecureNoAuthRefusedWithStoredPassword(t *testing.T) {
	env := testEnv(t)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	delete(env.Vars, "NOTESTEME_PASSWORD")
	if _, err := StartServerWith(t.Context(), env, ServeOptions{InsecureNoAuth: true}); !errors.Is(err, ErrInsecureWithStoredPassword) {
		t.Fatalf("DB 有密碼：%v", err)
	}
	// 不帶旗標照常啟動，且要求登入。
	srv, err = StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	resp, err := http.Get("http://" + srv.Addr() + "/api/items")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("一般模式 = %d", resp.StatusCode)
	}
	resp, err = http.Get("http://" + srv.Addr() + "/api/auth/methods")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		InsecureNoAuth *bool `json:"insecure_no_auth"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if m.InsecureNoAuth == nil || *m.InsecureNoAuth {
		t.Errorf("一般模式的 insecure_no_auth = %v", m.InsecureNoAuth)
	}
}
