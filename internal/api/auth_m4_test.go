package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/oidc"
	"github.com/cxyfer/notesteme/internal/store"
)

type m4Server struct {
	*testServer
	svc  *auth.Service
	logs *bytes.Buffer
}

func newM4Server(t *testing.T, cfg auth.Config) *m4Server {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var logs bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&logs, nil))
	cfg.Store, cfg.Clock, cfg.Params, cfg.Log = st, clk, auth.TestParams, lg
	svc := auth.New(cfg)
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	d := Deps{Store: st, Auth: svc, Clock: clk, Version: "test", Log: lg}
	return &m4Server{testServer: &testServer{h: New(d), clk: clk, deps: d}, svc: svc, logs: &logs}
}

func proxyAuthConfig() auth.Config {
	return auth.Config{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32")},
		Proxy:          auth.ProxyConfig{UserHeader: "Remote-User", AllowedUsers: []string{"alice"}},
	}
}

// browser 是一個帶 cookie jar 的 client（httptest 的 recorder 版本）。
type browser struct {
	s      *m4Server
	remote string
	jar    map[string]string
}

func (b *browser) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	return b.s.do(method, path, body, func(r *http.Request) {
		r.RemoteAddr = b.remote
		for k, v := range b.jar {
			r.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
	})
}

func (b *browser) keep(rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(b.jar, c.Name)
			continue
		}
		b.jar[c.Name] = c.Value
	}
	return rec
}

func TestProxyAuthMiddleware(t *testing.T) {
	s := newM4Server(t, proxyAuthConfig())
	alice := map[string]string{"Remote-User": "alice"}
	b := &browser{s: s, remote: "10.0.0.5:4000", jar: map[string]string{}}

	// 第一次：建立 proxy session 並設定 cookie。
	rec := b.keep(b.do("GET", "/api/tokens", "", alice))
	if rec.Code != 200 || b.jar[auth.SessionCookie] == "" || b.jar[auth.CSRFCookie] == "" {
		t.Fatalf("first request: %d %v", rec.Code, rec.Header()["Set-Cookie"])
	}
	first := b.jar[auth.SessionCookie]
	// 之後沿用同一個 session，不再建立新的。
	rec = b.keep(b.do("GET", "/api/tokens", "", alice))
	if rec.Code != 200 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("second request created another session: %v", rec.Header()["Set-Cookie"])
	}
	// 非 GET 沒有 CSRF：403；帶 CSRF：通過驗證（tokens 建立回 201）。
	if rec := b.do("POST", "/api/tokens", `{"name":"x","scopes":["read"]}`, alice); rec.Code != 403 {
		t.Fatalf("POST without CSRF = %d", rec.Code)
	}
	withCSRF := map[string]string{"Remote-User": "alice", auth.CSRFHeader: b.jar[auth.CSRFCookie]}
	if rec := b.do("POST", "/api/tokens", `{"name":"x","scopes":["read"]}`, withCSRF); rec.Code != 201 {
		t.Fatalf("POST with CSRF = %d %s", rec.Code, rec.Body)
	}
	// 標頭消失：session 立即撤銷，401；之後帶回標頭也不能用舊的 cookie（會建立新的 session）。
	if rec := b.do("GET", "/api/tokens", "", nil); rec.Code != 401 {
		t.Fatalf("header gone = %d", rec.Code)
	}
	rec = b.keep(b.do("GET", "/api/tokens", "", alice))
	if rec.Code != 200 || b.jar[auth.SessionCookie] == first {
		t.Fatalf("expected a new session after revocation: %d", rec.Code)
	}
}

func TestProxyAuthRejections(t *testing.T) {
	s := newM4Server(t, proxyAuthConfig())
	at := func(remote string, hdr map[string]string, method, path string) *httptest.ResponseRecorder {
		b := &browser{s: s, remote: remote, jar: map[string]string{}}
		return b.do(method, path, "", hdr)
	}
	// 不在清單中：403，訊息含值（最多 64 字元），沒有 session。
	rec := at("10.0.0.5:1", map[string]string{"Remote-User": "mallory"}, "GET", "/api/tokens")
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"proxy_user_not_allowed"`) ||
		!strings.Contains(rec.Body.String(), "這個帳號（mallory）不能使用 Notesteme。") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("not allowed: %d %s", rec.Code, rec.Body)
	}
	long := strings.Repeat("m", 100)
	rec = at("10.0.0.5:1", map[string]string{"Remote-User": long}, "GET", "/api/tokens")
	if !strings.Contains(rec.Body.String(), "（"+strings.Repeat("m", 64)+"）") {
		t.Fatalf("long value not truncated: %s", rec.Body)
	}
	// 不受信任的對端：即使偽造 X-Forwarded-For 成 proxy 的位址也不行。
	rec = at("203.0.113.7:1", map[string]string{"Remote-User": "alice", "X-Forwarded-For": "10.0.0.5"}, "GET", "/api/tokens")
	if rec.Code != 401 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("untrusted peer = %d", rec.Code)
	}
	if !strings.Contains(s.logs.String(), "忽略來自不受信任位址 203.0.113.7 的 Remote-User 標頭。") {
		t.Fatalf("missing log: %s", s.logs)
	}
	// 受信任的對端帶了 X-Forwarded-For（真實 client 的位址）：信任判斷仍看對端，接受標頭。
	rec = at("10.0.0.5:1", map[string]string{"Remote-User": "alice", "X-Forwarded-For": "198.51.100.3"}, "GET", "/api/tokens")
	if rec.Code != 200 {
		t.Fatalf("trusted peer with XFF = %d", rec.Code)
	}
	// /mcp 不接受 proxy 身分。
	if rec := at("10.0.0.5:1", map[string]string{"Remote-User": "alice"}, "POST", "/mcp"); rec.Code != 401 {
		t.Fatalf("/mcp = %d", rec.Code)
	}
	// 標頭大小寫不同的寫法也被視為同一個標頭（HTTP 標頭不分大小寫），出現兩次就忽略。
	b := &browser{s: s, remote: "10.0.0.5:1", jar: map[string]string{}}
	rec = s.do("GET", "/api/tokens", "", func(r *http.Request) {
		r.RemoteAddr = b.remote
		r.Header.Add("Remote-User", "alice")
		r.Header.Add("remote-user", "alice")
	})
	if rec.Code != 401 {
		t.Fatalf("duplicated header = %d", rec.Code)
	}
}

func TestProxySessionCannotUseMCPAndIsNotRevokedByIt(t *testing.T) {
	s := newM4Server(t, proxyAuthConfig())
	b := &browser{s: s, remote: "10.0.0.5:1", jar: map[string]string{}}
	alice := map[string]string{"Remote-User": "alice"}
	b.keep(b.do("GET", "/api/tokens", "", alice))
	hdr := map[string]string{"Remote-User": "alice", auth.CSRFHeader: b.jar[auth.CSRFCookie], "Content-Type": "application/json"}
	if rec := b.do("POST", "/mcp", `{}`, hdr); rec.Code != 401 {
		t.Fatalf("/mcp with proxy session = %d", rec.Code)
	}
	if rec := b.do("GET", "/api/tokens", "", alice); rec.Code != 200 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("proxy session should survive a /mcp attempt: %d", rec.Code)
	}
}

func TestBearerIgnoresProxyHeader(t *testing.T) {
	s := newM4Server(t, proxyAuthConfig())
	tok, err := s.svc.CreateToken(context.Background(), "reader", []string{"read"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{s: s, remote: "10.0.0.5:1", jar: map[string]string{}}
	rec := b.do("POST", "/api/tokens", `{}`, map[string]string{"Authorization": "Bearer " + tok.Token, "Remote-User": "alice"})
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"required_scope":"admin"`) || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("bearer + header = %d %s", rec.Code, rec.Body)
	}
	// 不在清單中的值也不影響 bearer。
	rec = b.do("GET", "/api/items", "", map[string]string{"Authorization": "Bearer " + tok.Token, "Remote-User": "mallory"})
	if rec.Code == 403 {
		t.Fatalf("bearer with a disallowed header value was refused: %s", rec.Body)
	}
}

func TestProxyHeaderIgnoredOnLocalSocketAndWhenDisabled(t *testing.T) {
	s := newM4Server(t, auth.Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32")}})
	b := &browser{s: s, remote: "10.0.0.5:1", jar: map[string]string{}}
	if rec := b.do("GET", "/api/tokens", "", map[string]string{"Remote-User": "alice"}); rec.Code != 401 {
		t.Fatalf("disabled proxy auth = %d", rec.Code)
	}
}

func TestSessionRoutes(t *testing.T) {
	s := newM4Server(t, auth.Config{})
	sess, csrf := s.login(t)
	other, _ := s.login(t)
	rec := s.do("GET", "/api/auth/sessions", "", withCookies(sess, csrf, ""))
	if rec.Code != 200 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if strings.Contains(body, sess) || strings.Contains(body, other) || strings.Contains(body, "token_hash") {
		t.Fatal("session list leaks a secret")
	}
	var list struct {
		Sessions []auth.SessionInfo `json:"sessions"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Sessions) != 2 {
		t.Fatalf("sessions = %+v", list.Sessions)
	}
	var otherID string
	for _, x := range list.Sessions {
		if !x.Current {
			otherID = x.ID
		}
	}
	// 撤銷另一個：204、它立即失效；找不到：404；DELETE 要 CSRF。
	if rec := s.do("DELETE", "/api/auth/sessions/"+otherID, "", withCookies(sess, csrf, "")); rec.Code != 403 {
		t.Fatalf("DELETE without CSRF = %d", rec.Code)
	}
	if rec := s.do("DELETE", "/api/auth/sessions/"+otherID, "", withCookies(sess, csrf, csrf)); rec.Code != 204 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("revoke other = %d %v", rec.Code, rec.Header()["Set-Cookie"])
	}
	if rec := s.do("GET", "/api/items", "", withCookies(other, "", "")); rec.Code != 401 {
		t.Fatalf("revoked session = %d", rec.Code)
	}
	if rec := s.do("DELETE", "/api/auth/sessions/"+otherID, "", withCookies(sess, csrf, csrf)); rec.Code != 404 {
		t.Fatalf("revoke missing = %d", rec.Code)
	}
	// current：登出並清除 cookie。
	rec = s.do("DELETE", "/api/auth/sessions/current", "", withCookies(sess, csrf, csrf))
	if rec.Code != 204 || !strings.Contains(strings.Join(rec.Header()["Set-Cookie"], "\n"), "notesteme_session=;") {
		t.Fatalf("revoke current = %d %v", rec.Code, rec.Header()["Set-Cookie"])
	}
	if rec := s.do("GET", "/api/items", "", withCookies(sess, "", "")); rec.Code != 401 {
		t.Fatalf("after sign-out = %d", rec.Code)
	}
	// token：需要 admin；current 對 token 來說找不到。
	reader, _ := s.svc.CreateToken(context.Background(), "r", []string{"read"}, nil)
	admin, _ := s.svc.CreateToken(context.Background(), "a", []string{"admin"}, nil)
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	if rec := s.do("GET", "/api/auth/sessions", "", bearer(reader.Token)); rec.Code != 403 || !strings.Contains(rec.Body.String(), `"required_scope":"admin"`) {
		t.Fatalf("reader = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("DELETE", "/api/auth/sessions/current", "", bearer(admin.Token)); rec.Code != 404 {
		t.Fatalf("token current = %d", rec.Code)
	}
}

func TestRotateRoute(t *testing.T) {
	s := newM4Server(t, auth.Config{})
	sess, csrf := s.login(t)
	tok, _ := s.svc.CreateToken(context.Background(), "agent1", []string{"read", "agent"}, nil)
	rec := s.do("POST", "/api/tokens/agent1/rotate", "", withCookies(sess, csrf, csrf))
	if rec.Code != 200 {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	var out auth.CreatedToken
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Token == "" || out.Name != "agent1" || out.ID != tok.ID {
		t.Fatalf("rotate body = %s", rec.Body)
	}
	if rec := s.do("GET", "/api/items", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok.Token) }); rec.Code != 401 {
		t.Fatalf("old secret = %d", rec.Code)
	}
	_ = s.svc.RevokeToken(context.Background(), "agent1")
	rec = s.do("POST", "/api/tokens/agent1/rotate", "", withCookies(sess, csrf, csrf))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"token_inactive"`) {
		t.Fatalf("inactive = %d %s", rec.Code, rec.Body)
	}
	if rec := s.do("POST", "/api/tokens/nope/rotate", "", withCookies(sess, csrf, csrf)); rec.Code != 404 {
		t.Fatalf("missing = %d", rec.Code)
	}
}

// stubIdP 實作 auth.OIDCProvider（api 層只測路由與 cookie，協定在 internal/oidc 測）。
type stubIdP struct {
	req       oidc.AuthRequest
	verifyErr error
}

func (p *stubIdP) Issuer() string { return "https://id.example.com" }
func (p *stubIdP) AuthURL(_ context.Context, r oidc.AuthRequest) (string, error) {
	p.req = r
	return "https://id.example.com/authorize?state=" + url.QueryEscape(r.State), nil
}
func (p *stubIdP) Exchange(context.Context, string, string) (string, error) { return "tok", nil }
func (p *stubIdP) Verify(context.Context, string, string) (oidc.Claims, error) {
	return oidc.Claims{Issuer: "https://id.example.com", Subject: "user-1"}, p.verifyErr
}

func TestOIDCRoutes(t *testing.T) {
	idp := &stubIdP{}
	s := newM4Server(t, auth.Config{OIDC: auth.OIDCConfig{Provider: idp, AllowedSubjects: []string{"user-1"}}})
	b := &browser{s: s, remote: "192.0.2.1:1", jar: map[string]string{}}
	rec := b.keep(b.do("GET", "/api/auth/oidc/start?next="+url.QueryEscape("/a/../b?x=1"), "", nil))
	if rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), "https://id.example.com/authorize") {
		t.Fatalf("start = %d %v", rec.Code, rec.Header())
	}
	cookie := strings.Join(rec.Header()["Set-Cookie"], "\n")
	for _, want := range []string{"notesteme_oidc=", "Path=/api/auth/oidc", "HttpOnly", "SameSite=Lax", "Max-Age=600"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("oidc cookie lacks %q: %s", want, cookie)
		}
	}
	rec = b.keep(b.do("GET", "/api/auth/oidc/callback?code=c&state="+url.QueryEscape(idp.req.State), "", nil))
	// Location 原樣保留（不做 path.Clean）。
	if rec.Code != 302 || rec.Header().Get("Location") != "/a/../b?x=1" || b.jar[auth.SessionCookie] == "" || b.jar[OIDCCookie] != "" {
		t.Fatalf("callback = %d %v jar=%v", rec.Code, rec.Header(), b.jar)
	}
	if rec := b.do("GET", "/api/auth/sessions", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"method":"oidc"`) {
		t.Fatalf("sessions = %d %s", rec.Code, rec.Body)
	}
	// OIDC session 不能用 /mcp。
	if rec := b.do("POST", "/mcp", `{}`, map[string]string{auth.CSRFHeader: b.jar[auth.CSRFCookie]}); rec.Code != 401 {
		t.Fatalf("/mcp with oidc session = %d", rec.Code)
	}
	// IdP 的 error 不原樣回顯。
	b2 := &browser{s: s, remote: "192.0.2.1:1", jar: map[string]string{}}
	b2.keep(b2.do("GET", "/api/auth/oidc/start", "", nil))
	rec = b2.do("GET", "/api/auth/oidc/callback?error=%3Cscript%3E&state="+url.QueryEscape(idp.req.State), "", nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "身分提供者拒絕了登入（unknown）。") || strings.Contains(rec.Body.String(), "script") {
		t.Fatalf("idp error = %d %s", rec.Code, rec.Body)
	}
	// 驗證失敗：401，log 記原因但不記 code。
	b3 := &browser{s: s, remote: "192.0.2.1:1", jar: map[string]string{}}
	b3.keep(b3.do("GET", "/api/auth/oidc/start", "", nil))
	idp.verifyErr = oidc.ErrNonce
	rec = b3.do("GET", "/api/auth/oidc/callback?code=secret-code-123&state="+url.QueryEscape(idp.req.State), "", nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "無法驗證身分提供者的回應，請重新登入。") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("verify failure = %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(s.logs.String(), "nonce") || strings.Contains(s.logs.String(), "secret-code-123") {
		t.Fatalf("log should name the reason without the code: %s", s.logs)
	}
	// state 不符：400。
	if rec := b3.do("GET", "/api/auth/oidc/callback?code=c&state=forged", "", nil); rec.Code != 400 || !strings.Contains(rec.Body.String(), "oidc_state_invalid") {
		t.Fatalf("forged state = %d", rec.Code)
	}
	// 只有 GET 免驗證。
	if rec := s.do("POST", "/api/auth/oidc/start", "", nil); rec.Code != 401 {
		t.Fatalf("POST start = %d", rec.Code)
	}
	// 登入方式
	rec = s.do("GET", "/api/auth/methods", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"host":"id.example.com"`) || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("methods = %d %s", rec.Code, rec.Body)
	}
}

func TestOIDCRoutesNotFoundWhenDisabled(t *testing.T) {
	s := newM4Server(t, auth.Config{})
	for _, p := range []string{"/api/auth/oidc/start", "/api/auth/oidc/callback?state=x&code=y"} {
		if rec := s.do("GET", p, "", nil); rec.Code != 404 {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
	rec := s.do("GET", "/api/auth/methods", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"enabled":false`) || strings.Contains(rec.Body.String(), "host") {
		t.Fatalf("methods = %s", rec.Body)
	}
	// 其他 /api/auth/oidc/* 路徑不是免驗證的例外。
	if rec := s.do("GET", "/api/auth/oidc/other", "", nil); rec.Code != 401 {
		t.Errorf("other oidc path = %d", rec.Code)
	}
}

func TestLocalRedirect(t *testing.T) {
	for in, want := range map[string]string{"/a?b": "/a?b", "//evil": "/", "https://x": "/", "/\\x": "/", "": "/"} {
		if got := localRedirect(in); got != want {
			t.Errorf("localRedirect(%q) = %q", in, got)
		}
	}
}
