package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LocalHost": true, "127.0.0.1": true, "127.255.0.9": true, "::1": true, "[::1]": true,
		"": false, "0.0.0.0": false, "::": false, "[::]": false, "localhost.": false, "localhost.evil.com": false,
		"evil.localhost": false, "::ffff:127.0.0.1": false, "::1%lo": false, "[::1%25lo]": false, "127.0.0.1.nip.io": false,
		"2130706433": false, "0x7f.0.0.1": false, "127.1": false, "[127.0.0.1]": true, "10.0.0.1": false,
	} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v，預期 %v", host, got, want)
		}
	}
}

// insecureGuard：Host 與 Origin、Sec-Fetch-Site 的組合（含多個值與空的 Host）。
func TestInsecureGuard(t *testing.T) {
	h := insecureGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	type hdr = map[string][]string
	cases := []struct {
		method, host string
		h            hdr
		want         int
	}{
		{"GET", "127.0.0.1:8080", nil, 204},
		{"GET", "", nil, 403},
		{"GET", "evil.com", nil, 403},
		{"GET", "localhost:8080", hdr{"Origin": {"https://evil.com"}, "Sec-Fetch-Site": {"cross-site"}}, 204},
		{"HEAD", "localhost:8080", hdr{"Sec-Fetch-Site": {"cross-site"}}, 204},
		{"POST", "localhost:8080", nil, 204},
		{"POST", "localhost:8080", hdr{"Origin": {"http://localhost:8080"}}, 204},
		{"POST", "localhost:8080", hdr{"Origin": {"https://LOCALHOST:8080"}, "Sec-Fetch-Site": {"same-origin"}}, 204},
		{"POST", "localhost:8080", hdr{"Sec-Fetch-Site": {"none"}}, 204},
		{"POST", "localhost:8080", hdr{"Origin": {"http://localhost:8080", "http://localhost:8080"}}, 403},
		{"POST", "localhost:8080", hdr{"Sec-Fetch-Site": {"same-origin", "same-origin"}}, 403},
		{"POST", "localhost:8080", hdr{"Origin": {"http://127.0.0.1:8080"}}, 403},
		{"POST", "localhost:8080", hdr{"Origin": {"http://localhost:8080?x"}}, 403},
		{"POST", "localhost:8080", hdr{"Origin": {""}}, 403},
		{"DELETE", "localhost:8080", hdr{"Sec-Fetch-Site": {"same-site"}}, 403},
		{"OPTIONS", "localhost:8080", hdr{"Origin": {"https://evil.com"}}, 204},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/api/items", nil)
		req.Host = c.host
		for k, v := range c.h {
			req.Header[k] = v
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s Host=%q %v = %d，預期 %d %s", c.method, c.host, c.h, rec.Code, c.want, rec.Body)
		}
	}
}

// 無驗證模式的 handler：沒有憑證的請求是 PrincipalInsecure（全部權限）；本機 socket 的 NewLocal 不受 InsecureNoAuth 影響。
func TestInsecureNoAuthPrincipal(t *testing.T) {
	var got auth.Principal
	h := ownerMiddleware(nil, nil, PrincipalInsecure)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = principalFrom(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/items", nil))
	if got.Kind != PrincipalInsecure || got.Scopes != nil {
		t.Errorf("身分 = %+v", got)
	}
	// 帶 bearer token 但沒有驗證服務：不放大成 owner。
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/items", nil)
	req.Header.Set("Authorization", "Bearer x")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bearer = %d", rec.Code)
	}
	if p, err := (insecureSessions{}).AuthenticateSession(t.Context(), ""); err != nil || p.Kind != PrincipalInsecure || !(insecureSessions{}).NoAuth() {
		t.Errorf("insecureSessions = %+v %v", p, err)
	}
}
