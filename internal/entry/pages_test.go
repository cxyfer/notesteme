package entry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/webui"
)

const goodSession = "good-session"

type fakeSessions struct {
	err   error // 非 nil 時一律回傳這個錯誤（例如 DB 錯誤）
	calls int
}

func (f *fakeSessions) AuthenticateSession(_ context.Context, tok string) (auth.Principal, error) {
	f.calls++
	if f.err != nil {
		return auth.Principal{}, f.err
	}
	if tok == goodSession {
		return auth.Principal{}, nil
	}
	return auth.Principal{}, auth.ErrUnauthenticated
}

const indexHTML = "<!doctype html><title>Notesteme</title>"

func newPages(s Sessions, log *slog.Logger) *Pages {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 標記「交給了 UI」
	})
	return NewPages(s, func() []byte { return []byte(indexHTML) }, ui, log)
}

func get(h http.Handler, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func withSession(tok string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: tok}) }
}

func TestPagesAddSignedIn(t *testing.T) {
	h := newPages(&fakeSessions{}, nil)
	rec := get(h, "/add?url=https%3A%2F%2Fyoutu.be%2Fabc", withSession(goodSession))
	if rec.Code != 200 || rec.Body.String() != indexHTML {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body)
	}
	assertEntryHeaders(t, rec)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func assertEntryHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q，want no-store", cc)
	}
	if xfo := rec.Header().Get("X-Frame-Options"); xfo != "DENY" {
		t.Errorf("X-Frame-Options = %q", xfo)
	}
}

func TestPagesAddSignedOut(t *testing.T) {
	cases := []struct {
		name   string
		target string
		mod    func(*http.Request)
		want   string
	}{
		{"沒有 cookie", "/add?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3Dabc", nil,
			"/login?next=%2Fadd%3Furl%3Dhttps%253A%252F%252Fwww.youtube.com%252Fwatch%253Fv%253Dabc"},
		{"沒有 query", "/add", nil, "/login?next=%2Fadd"},
		{"bearer token 不算登入", "/add?url=x", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+goodSession) },
			"/login?next=%2Fadd%3Furl%3Dx"},
		{"query 中的 token 不算登入", "/add?token=" + goodSession, nil, "/login?next=%2Fadd%3Ftoken%3D" + goodSession},
		{"失效的 session", "/add?url=x", withSession("revoked"), "/login?next=%2Fadd%3Furl%3Dx"},
		{"空的 session", "/add?url=x", withSession(""), "/login?next=%2Fadd%3Furl%3Dx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := get(newPages(&fakeSessions{}, nil), c.target, c.mod)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != c.want {
				t.Fatalf("status=%d Location=%q，want 303 %q", rec.Code, rec.Header().Get("Location"), c.want)
			}
			assertEntryHeaders(t, rec)
			if rec.Body.Len() != 0 {
				t.Errorf("303 不應有內容：%q", rec.Body)
			}
		})
	}
}

func TestPagesShare(t *testing.T) {
	cases := []struct {
		query    string
		signedIn bool
		want     string
	}{
		{"url=https%3A%2F%2Fyoutu.be%2Fabc&text=&title=", true, "/add?url=https%3A%2F%2Fyoutu.be%2Fabc"},
		{"text=Watch+this%3A+https%3A%2F%2Fyoutu.be%2Fabc", true, "/add?url=https%3A%2F%2Fyoutu.be%2Fabc"},
		{"text=just+some+words&title=Hello", true, "/add?share_error=no_url"},
		{"", true, "/add?share_error=no_url"},
		// & 與 # 在網址中要再編碼，不能變成 /add 的另一個參數
		{"url=https%3A%2F%2Fa.example.com%2Fx%3Fa%3D1%26b%3D2%23f", true, "/add?url=https%3A%2F%2Fa.example.com%2Fx%3Fa%3D1%26b%3D2%23f"},
		{"text=Watch+this%3A+https%3A%2F%2Fyoutu.be%2Fabc", false, "/login?next=%2Fadd%3Furl%3Dhttps%253A%252F%252Fyoutu.be%252Fabc"},
		{"text=nothing", false, "/login?next=%2Fadd%3Fshare_error%3Dno_url"},
	}
	for _, c := range cases {
		var mod func(*http.Request)
		if c.signedIn {
			mod = withSession(goodSession)
		}
		rec := get(newPages(&fakeSessions{}, nil), "/share?"+c.query, mod)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != c.want {
			t.Errorf("/share?%s（signedIn=%v）= %d %q，want %q", c.query, c.signedIn, rec.Code, rec.Header().Get("Location"), c.want)
		}
		assertEntryHeaders(t, rec)
	}
}

// /share 的 text、title 不寫進 log，即使驗證 session 時出錯。
func TestPagesShareDoesNotLogText(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := newPages(&fakeSessions{err: errors.New("db down")}, log)
	rec := get(h, "/share?text=secret+words+https%3A%2F%2Fa.example.com%2Fsecret&title=private", withSession(goodSession))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d，want 500", rec.Code)
	}
	for _, s := range []string{"secret", "private", "a.example.com"} {
		if strings.Contains(buf.String(), s) {
			t.Errorf("log 含有 %q：%s", s, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "db down") {
		t.Errorf("log 應記下錯誤：%s", buf.String())
	}
}

func TestPagesAddSessionError(t *testing.T) {
	h := newPages(&fakeSessions{err: errors.New("db down")}, nil)
	if rec := get(h, "/add?url=x", withSession(goodSession)); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d，want 500", rec.Code)
	}
}

func TestPagesLogin(t *testing.T) {
	fs := &fakeSessions{}
	rec := get(newPages(fs, nil), "/login?next=%2Fadd", nil)
	if rec.Code != 200 || rec.Body.String() != indexHTML {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body)
	}
	assertEntryHeaders(t, rec)
	if fs.calls != 0 {
		t.Errorf("/login 不需要驗證 session，卻呼叫了 %d 次", fs.calls)
	}
}

func TestPagesManifest(t *testing.T) {
	rec := get(newPages(&fakeSessions{}, nil), "/manifest.webmanifest", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("status=%d Content-Type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m struct {
		Name, StartURL, Scope, Display string
		Icons                          []struct{ Src, Sizes, Type string }
		ShareTarget                    struct {
			Action, Method string
			Params         map[string]string
		} `json:"share_target"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if m.Name != "Notesteme" || raw["start_url"] != "/" || m.Scope != "/" || m.Display != "standalone" {
		t.Errorf("manifest = %+v", raw)
	}
	if m.ShareTarget.Action != "/share" || m.ShareTarget.Method != "GET" ||
		m.ShareTarget.Params["url"] != "url" || m.ShareTarget.Params["text"] != "text" || m.ShareTarget.Params["title"] != "title" {
		t.Errorf("share_target = %+v", m.ShareTarget)
	}
	sizes := map[string]bool{}
	for _, ic := range m.Icons {
		if ic.Type != "image/png" || !strings.HasPrefix(ic.Src, "/icons/") {
			t.Errorf("icon = %+v", ic)
		}
		sizes[ic.Sizes] = true
	}
	if !sizes["192x192"] || !sizes["512x512"] {
		t.Errorf("icons = %+v", m.Icons)
	}
}

func TestPagesServiceWorker(t *testing.T) {
	rec := get(newPages(&fakeSessions{}, nil), "/sw.js", nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") ||
		rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("status=%d headers=%v", rec.Code, rec.Header())
	}
	// 不快取任何回應：沒有 cache.put／addAll，也沒有 respondWith。
	for _, s := range []string{"respondWith", ".put(", "addAll"} {
		if strings.Contains(rec.Body.String(), s) {
			t.Errorf("service worker 含有 %q", s)
		}
	}
}

func TestPagesMethodNotAllowed(t *testing.T) {
	for _, p := range []string{"/add", "/share", "/login", "/manifest.webmanifest", "/sw.js"} {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		rec := httptest.NewRecorder()
		newPages(&fakeSessions{}, nil).ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("POST %s = %d", p, rec.Code)
		}
	}
}

func TestPagesHead(t *testing.T) {
	req := httptest.NewRequest(http.MethodHead, "/login", nil)
	rec := httptest.NewRecorder()
	newPages(&fakeSessions{}, nil).ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD /login = %d body=%q", rec.Code, rec.Body)
	}
}

func TestPagesOtherPathsGoToUI(t *testing.T) {
	for _, p := range []string{"/", "/library", "/add/", "/adds", "/share/x", "/icons/icon-192.png"} {
		if rec := get(newPages(&fakeSessions{}, nil), p, nil); rec.Code != http.StatusTeapot {
			t.Errorf("GET %s = %d，應交給 UI", p, rec.Code)
		}
	}
}

func TestIsPagePathMatchesWebUIRoutes(t *testing.T) {
	// 頁面路由與 webui 的安全標頭共用同一份 CSP。
	if !strings.Contains(webui.CSP, "frame-ancestors 'none'") {
		t.Fatal("webui.CSP 沒有 frame-ancestors 'none'")
	}
}
