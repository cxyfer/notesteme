package hubapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
	"github.com/cxyfer/notesteme/internal/websession"
)

type authEnv struct {
	h     http.Handler
	store hubstore.Store
	clk   *clock.Fake
	// token、csrf 是一個已登入的 session。
	token, csrf string
}

func newAuthEnv(t *testing.T, visibility string) *authEnv {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC))
	st, err := sqlite.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &hubconfig.Config{
		PublicURL: "https://hub.example.org", SiteVisibility: visibility, SessionKey: strings.Repeat("k", 32),
		OAuth: map[hubconfig.Provider]hubconfig.OAuthClient{hubconfig.GitHub: {ClientID: "id", ClientSecret: "secret"}},
		Groups: map[string]hubconfig.Group{hubconfig.GroupMembers: {Permissions: []string{"read", "download"}}},
	}
	auth, err := hubauth.New(hubauth.Deps{Config: cfg, Store: st, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	// session 直接寫進 store（OAuth 流程本身在 hubauth 的測試中驗證）。
	u, _, err := st.SignIn(context.Background(), hubstore.ExternalIdentity{Provider: "github", Subject: "1", Login: "alice"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := websession.RandomToken(32)
	if err := st.CreateSession(context.Background(), hubstore.Session{
		ID: "s1", UserID: u.ID, TokenHash: websession.HashToken(tok), CreatedAt: clk.Now(), AuthenticatedAt: clk.Now(), ExpiresAt: clk.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{Config: cfg, Store: st, Clock: clk, Auth: auth})
	return &authEnv{h: h, store: st, clk: clk, token: tok, csrf: websession.CSRFFor(tok)}
}

type reqOpt func(*http.Request)

func withSession(e *authEnv) reqOpt {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: e.token})
		r.AddCookie(&http.Cookie{Name: hubauth.CSRFCookie, Value: e.csrf})
	}
}

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (e *authEnv) do(method, path string, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{"x":1}`))
	for _, o := range opts {
		o(r)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func expect(t *testing.T, name string, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("%s = %d %s，預期 %d", name, rec.Code, rec.Body, status)
		return
	}
	if code != "" && decode(t, rec)["code"] != code {
		t.Errorf("%s 的 code = %v，預期 %s", name, decode(t, rec)["code"], code)
	}
}

func TestAuthMiddleware(t *testing.T) {
	e := newAuthEnv(t, hubconfig.VisibilityAuthenticated)
	s := withSession(e)
	csrf := withHeader(hubauth.CSRFHeader, e.csrf)

	rec := e.do("GET", "/api/v1/me")
	expect(t, "未登入的 /me", rec, 401, "unauthorized")
	if decode(t, rec)["message"] != "請先登入 Hub。" {
		t.Errorf("401 的訊息 = %v", decode(t, rec)["message"])
	}
	rec = e.do("GET", "/api/v1/me", s)
	expect(t, "/me", rec, 200, "")
	if m := decode(t, rec); m["display_name"] != "alice" || len(m["identities"].([]any)) != 1 {
		t.Errorf("/me = %v", m)
	}
	expect(t, "bearer（PAT 是 S2）", e.do("GET", "/api/v1/me", withHeader("Authorization", "Bearer nsh_x")), 401, "unauthorized")
	expect(t, "壞掉的 session cookie", e.do("GET", "/api/v1/me", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: hubauth.SessionCookie, Value: "forged"})
	}), 401, "unauthorized")
	expect(t, "公開的路由", e.do("GET", "/api/v1/auth/providers"), 200, "")
	expect(t, "公開的 stub", e.do("POST", "/api/v1/takedowns"), 501, "not_implemented")
	for _, p := range PublicDocPaths {
		expect(t, p, e.do("GET", p), 200, "")
	}

	// CSRF：非 GET 的 session 請求，在 stub 之前檢查。
	expect(t, "沒有 CSRF 的 stub", e.do("POST", "/api/v1/me/tokens", s), 403, "csrf_invalid")
	expect(t, "錯的 CSRF", e.do("POST", "/api/v1/me/tokens", s, withHeader(hubauth.CSRFHeader, "nope")), 403, "csrf_invalid")
	expect(t, "有 CSRF 的 stub", e.do("POST", "/api/v1/me/tokens", s, csrf), 501, "not_implemented")
	expect(t, "沒有 CSRF 的登出", e.do("POST", "/api/v1/auth/logout", s), 403, "csrf_invalid")
	expect(t, "移除唯一的登入方式", e.do("DELETE", "/api/v1/me/identities/github", s, csrf), 409, "last_identity")
	expect(t, "連結已有的供應商", e.do("POST", "/api/v1/me/identities/github/link", s, csrf), 409, "provider_already_linked")
	expect(t, "連結沒有啟用的供應商", e.do("POST", "/api/v1/me/identities/google/link", s, csrf), 404, "provider_not_configured")

	rec = e.do("POST", "/api/v1/auth/logout", s, csrf)
	expect(t, "登出", rec, 204, "")
	if c := rec.Header().Values("Set-Cookie"); len(c) != 2 || !strings.Contains(c[0], "Max-Age=0") {
		t.Errorf("登出的 Set-Cookie = %q", c)
	}
	expect(t, "登出後的 /me", e.do("GET", "/api/v1/me", s), 401, "unauthorized")
}

func TestPublicSiteLetsAnonymousReachReadRoutes(t *testing.T) {
	e := newAuthEnv(t, hubconfig.VisibilityPublic)
	expect(t, "read 的 stub", e.do("GET", "/api/v1/versions/v1"), 501, "not_implemented")
	expect(t, "download 的 stub", e.do("GET", "/api/v1/versions/v1/download"), 401, "unauthorized")
	expect(t, "/me", e.do("GET", "/api/v1/me"), 401, "unauthorized")
}

func TestOAuthStartAndCallbackHTTP(t *testing.T) {
	e := newAuthEnv(t, hubconfig.VisibilityAuthenticated)
	rec := e.do("GET", "/auth/github/start")
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "https://github.com/login/oauth/authorize?") {
		t.Fatalf("start = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	c := rec.Header().Get("Set-Cookie")
	for _, attr := range []string{"notesteme_hub_oauth=", "Path=/auth", "Max-Age=600", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(c, attr) {
			t.Errorf("oauth cookie %q 沒有 %s", c, attr)
		}
	}
	expect(t, "沒有啟用的供應商", e.do("GET", "/auth/google/start"), 404, "provider_not_configured")
	expect(t, "不認得的供應商", e.do("GET", "/auth/evil/start"), 404, "provider_not_configured")

	rec = e.do("GET", "/auth/github/callback?code=x&state=y", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: hubauth.OAuthCookie, Value: "y"})
	})
	expect(t, "不存在的 state", rec, 400, "oauth_state_invalid")
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(rec.Header().Get("Set-Cookie"), "notesteme_hub_oauth=;") {
		t.Errorf("callback 的標頭 = %v", rec.Header())
	}
}
