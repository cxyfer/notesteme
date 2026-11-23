package api

import "testing"

// OPS-R21：vps 部署的 session 與 CSRF cookie 一律帶 Secure，即使請求是 http（TLS 在 Caddy 終止）。
func TestLoginCookiesAlwaysSecureOnVPS(t *testing.T) {
	s := newTestServer(t)
	s.deps.SecureCookies = true
	s.h = New(s.deps)
	rec := s.do("POST", "/api/auth/login", `{"password":"pw"}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %v", rec.Header()["Set-Cookie"])
	}
	for _, c := range cookies {
		if !c.Secure {
			t.Errorf("%s 必須帶 Secure", c.Name)
		}
	}
}
