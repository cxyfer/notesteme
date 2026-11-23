package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// Hub UI 的分派（M5-S9）：後端路徑照常進入 API，其他是 SPA；/assets 與有副檔名的路徑找不到時是 404。
func TestUIDispatch(t *testing.T) {
	cfg := testConfig(t, "")
	ui := fstest.MapFS{
		"index.html":        {Data: []byte(`<!doctype html><html><head><script type="module" src="/assets/index-A.js"></script></head><body><div id="root"></div></body></html>`)},
		"assets/index-A.js": {Data: []byte(`export{}`)},
	}
	s, err := Open(context.Background(), Options{Config: cfg, UI: ui})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := s.Handler()
	do := func(method, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
		return rec
	}
	for _, p := range []string{"/", "/versions/abc", "/videos/youtube/x1", "/account", "/admin/users", "/login", "/takedown"} {
		rec := do(http.MethodGet, p)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("GET %s：%d %q", p, rec.Code, rec.Body.String())
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if csp != UICSP || strings.Contains(csp, "youtube") {
			t.Errorf("GET %s 的 CSP：%q", p, csp)
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("GET %s 的 Cache-Control：%q", p, rec.Header().Get("Cache-Control"))
		}
	}
	if rec := do(http.MethodGet, "/assets/index-A.js"); rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("assets：%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	for _, p := range []string{"/assets/missing.js", "/favicon.ico"} {
		if rec := do(http.MethodGet, p); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s：%d", p, rec.Code)
		}
	}
	// 後端路徑不落到 UI
	for _, c := range []struct {
		target string
		code   int
	}{{"/healthz", 200}, {"/api/v1/auth/providers", 200}, {"/api/v1/nope", 404}, {"/auth/nope/start", 404}, {"/auth/x", 404}} {
		rec := do(http.MethodGet, c.target)
		if rec.Code != c.code || strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("GET %s：%d %q", c.target, rec.Code, rec.Body.String())
		}
	}
	if rec := do(http.MethodPost, "/versions/abc"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST 前端路徑：%d", rec.Code)
	}
}

// 沒有建置產物時回 Hub 的 placeholder（不是 Client 的）。
func TestUIPlaceholder(t *testing.T) {
	rec := httptest.NewRecorder()
	NewUI(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "notesteme-hub-ui-placeholder") {
		t.Errorf("placeholder：%q", rec.Body.String())
	}
}
