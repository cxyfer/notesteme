package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

type testServer struct {
	h   http.Handler
	clk *clock.Fake
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	h := New(Deps{
		Store: st, Auth: svc, Clock: clk, Version: "test",
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	return &testServer{h: h, clk: clk}
}

func (s *testServer) do(method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func (s *testServer) login(t *testing.T) (session, csrf string) {
	t.Helper()
	rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookie:
			session = c.Value
		case auth.CSRFCookie:
			csrf = c.Value
		}
	}
	if session == "" || csrf == "" {
		t.Fatalf("missing cookies: %v", rec.Header()["Set-Cookie"])
	}
	return session, csrf
}

func withCookies(session, csrf, header string) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: session})
		r.AddCookie(&http.Cookie{Name: auth.CSRFCookie, Value: csrf})
		if header != "" {
			r.Header.Set(auth.CSRFHeader, header)
		}
	}
}

func TestPublicRoutes(t *testing.T) {
	s := newTestServer(t)
	for _, p := range []string{"/healthz", "/readyz"} {
		if rec := s.do("GET", p, "", nil); rec.Code != 200 {
			t.Errorf("GET %s = %d", p, rec.Code)
		}
	}
}

func TestUnauthenticatedRefused(t *testing.T) {
	s := newTestServer(t)
	for _, c := range [][2]string{
		{"GET", "/api/items"}, {"POST", "/api/items"}, {"POST", "/mcp"},
		{"POST", "/api/auth/logout"}, {"GET", "/api/openapi.json"}, {"GET", "/api/unknown"},
	} {
		rec := s.do(c[0], c[1], "", nil)
		if rec.Code != 401 {
			t.Errorf("%s %s = %d, want 401", c[0], c[1], rec.Code)
			continue
		}
		var e Error
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Code != CodeUnauthorized || e.Message == "" {
			t.Errorf("%s %s body = %s", c[0], c[1], rec.Body)
		}
	}
}

func TestBearerRefusedForNow(t *testing.T) {
	s := newTestServer(t)
	rec := s.do("GET", "/api/items", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer abc") })
	if rec.Code != 401 {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestLoginCookies(t *testing.T) {
	s := newTestServer(t)
	rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %v", rec.Header()["Set-Cookie"])
	}
	for _, c := range cookies {
		if c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Errorf("%s: %+v", c.Name, c)
		}
		if c.Name == auth.SessionCookie && (!c.HttpOnly || c.Secure) {
			t.Errorf("session cookie over http: %+v", c)
		}
		if c.Name == auth.CSRFCookie && c.HttpOnly {
			t.Errorf("csrf cookie must be readable by the page: %+v", c)
		}
	}

	rec = s.do("POST", "/api/auth/login", `{"password":"pw"}`, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
	})
	for _, c := range rec.Result().Cookies() {
		if !c.Secure {
			t.Errorf("%s should be Secure behind https", c.Name)
		}
	}
}

func TestLoginWrongPassword(t *testing.T) {
	s := newTestServer(t)
	rec := s.do("POST", "/api/auth/login", `{"password":"nope"}`, nil)
	if rec.Code != 401 {
		t.Fatalf("status = %d", rec.Code)
	}
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code != CodeInvalidPass || e.Message != "密碼不正確。" {
		t.Errorf("error = %+v", e)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("no cookies on failure")
	}
}

func TestLoginLockedAfterFiveFailures(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 5; i++ {
		if rec := s.do("POST", "/api/auth/login", `{"password":"nope"}`, nil); rec.Code != 401 {
			t.Fatalf("attempt %d status = %d", i+1, rec.Code)
		}
	}
	rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil)
	if rec.Code != 429 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Message != "密碼不正確。" || e.RetryAfterS == nil || *e.RetryAfterS != 60 || e.Code != CodeLoginLocked {
		t.Errorf("error = %+v", e)
	}
	s.clk.Advance(61 * time.Second)
	if rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil); rec.Code != 200 {
		t.Errorf("after the lock expires status = %d", rec.Code)
	}
}

func TestReadyzFailedChecks(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := New(Deps{
		Store: st, Auth: auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams}), Clock: clk,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		ReadyChecks: []ReadyCheck{
			{Name: "ok", Check: func(context.Context) error { return nil }},
			{Name: "ffmpeg", Check: func(context.Context) error { return io.EOF }},
		},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 503 || e.Code != CodeNotReady || len(e.Failed) != 1 || e.Failed[0] != "ffmpeg" {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "EOF") {
		t.Error("readyz must not leak the underlying error")
	}
}

func TestPlannedRoutesAnswer501(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)
	for _, c := range [][2]string{
		{"POST", "/api/providers"},
	} {
		rec := s.do(c[0], c[1], "", withCookies(sess, csrf, csrf))
		var e Error
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != 501 || e.Code != CodeNotImplemented {
			t.Errorf("%s %s = %d %s", c[0], c[1], rec.Code, rec.Body)
		}
	}
}

func TestLoginBadBody(t *testing.T) {
	s := newTestServer(t)
	rec := s.do("POST", "/api/auth/login", `{}`, nil)
	if rec.Code != 422 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var e Error
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Code != CodeInvalidInput || e.Message == "" {
		t.Errorf("error = %+v (%s)", e, rec.Body)
	}
}

func TestSessionRequests(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)

	rec := s.do("GET", "/api/items", "", withCookies(sess, csrf, ""))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Errorf("GET items = %d %s", rec.Code, rec.Body)
	}

	// 缺 CSRF 標頭、標頭錯誤：403。
	for name, hdr := range map[string]string{"缺標頭": "", "錯誤的標頭": "forged"} {
		rec = s.do("POST", "/api/queue/pause", "", withCookies(sess, csrf, hdr))
		if rec.Code != 403 {
			t.Errorf("%s: status = %d", name, rec.Code)
		}
	}
	// 有 CSRF：通過驗證，到達尚未實作的 handler。
	rec = s.do("POST", "/api/queue/pause", "", withCookies(sess, csrf, csrf))
	if rec.Code != 501 {
		t.Errorf("with csrf: status = %d, body = %s", rec.Code, rec.Body)
	}
	// MCP 尚未實作：501。
	if rec = s.do("POST", "/mcp", "", withCookies(sess, csrf, csrf)); rec.Code != 501 {
		t.Errorf("POST /mcp = %d %s", rec.Code, rec.Body)
	}
	// 這個測試 server 只有驗證層、沒有注入 Pipeline：這是設定錯誤，回 500 而不是 501，
	// 因為 501 在驗收測試中代表「功能還沒做」（ADR-0015）。
	for _, c := range [][3]string{
		{"POST", "/api/items", `{"url":"https://example.com/x"}`},
		{"GET", "/api/settings/language", ""},
		{"PUT", "/api/settings/language", `{"targets":["en"]}`},
	} {
		if rec = s.do(c[0], c[1], c[2], withCookies(sess, csrf, csrf)); rec.Code != 500 {
			t.Errorf("%s %s = %d %s", c[0], c[1], rec.Code, rec.Body)
		}
	}
}

func TestLogoutEndsSession(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)
	rec := s.do("POST", "/api/auth/logout", "", withCookies(sess, csrf, csrf))
	if rec.Code != 200 && rec.Code != 204 {
		t.Fatalf("logout status = %d, body = %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge >= 0 {
			t.Errorf("%s should be cleared: %+v", c.Name, c)
		}
	}
	if rec := s.do("GET", "/api/items", "", withCookies(sess, csrf, "")); rec.Code != 401 {
		t.Errorf("after logout status = %d", rec.Code)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	s := newTestServer(t)
	sess, csrf := s.login(t)
	s.clk.Advance(auth.SessionIdleTimeout + time.Minute)
	if rec := s.do("GET", "/api/items", "", withCookies(sess, csrf, "")); rec.Code != 401 {
		t.Errorf("status = %d", rec.Code)
	}
}
