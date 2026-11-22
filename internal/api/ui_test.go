package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/webui"
)

// newUITestServer 和 newTestServer 相同，另外掛上模擬建置產物的 Web UI。
func newUITestServer(t *testing.T, local bool) *testServer {
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
	ui := webui.New(fstest.MapFS{
		"index.html":          {Data: []byte(`<!doctype html><html><head><script type="module" src="/assets/index-abc.js"></script></head></html>`)},
		"assets/index-abc.js": {Data: []byte(`console.log(1)`)},
	})
	d := Deps{
		Store: st, Auth: svc, Clock: clk, Version: "test",
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), UI: ui,
	}
	h := New(d)
	if local {
		h = NewLocal(d)
	}
	return &testServer{h: h, clk: clk}
}

// 後端路徑不會被 UI 吃掉：/api、/mcp、/healthz、/readyz 的語意不變（未登入 401、方法錯誤 405、未知路徑 404 JSON）。
func TestUIDoesNotShadowBackend(t *testing.T) {
	s := newUITestServer(t, false)
	for _, p := range []string{"/healthz", "/readyz"} {
		rec := s.do("GET", p, "", nil)
		if rec.Code != 200 || rec.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("GET %s = %d，CSP=%q；預期 200 且不是 UI 回應", p, rec.Code, rec.Header().Get("Content-Security-Policy"))
		}
	}
	for _, c := range [][2]string{{"GET", "/api/items"}, {"GET", "/api/unknown"}, {"POST", "/mcp"}, {"GET", "/api"}} {
		if rec := s.do(c[0], c[1], "", nil); rec.Code != 401 {
			t.Errorf("%s %s = %d，預期 401（仍受 AUTH-R1 保護）", c[0], c[1], rec.Code)
		}
	}
	session, csrf := s.login(t)
	for _, p := range []string{"/api/unknown", "/api", "/api/"} {
		rec := s.do("GET", p, "", withCookies(session, csrf, ""))
		if rec.Code != 404 || rec.Header().Get("Content-Security-Policy") != "" {
			t.Errorf("GET %s（已登入）= %d CSP=%q，預期 404 且不是 UI 回應", p, rec.Code, rec.Header().Get("Content-Security-Policy"))
		}
	}
	if rec := s.do("DELETE", "/healthz", "", nil); rec.Code != 405 {
		t.Errorf("DELETE /healthz = %d，預期 405（不是 UI）", rec.Code)
	}
}

func TestUIServedForFrontendRoutes(t *testing.T) {
	s := newUITestServer(t, false)
	for _, p := range []string{"/", "/library", "/settings/appearance", "/items/abc", "/login"} {
		rec := s.do("GET", p, "", nil)
		if rec.Code != 200 || rec.Header().Get("Content-Security-Policy") != webui.CSP {
			t.Errorf("GET %s = %d，CSP=%q", p, rec.Code, rec.Header().Get("Content-Security-Policy"))
		}
	}
	// 路徑以 /api 開頭但不是 /api 底下的，屬於前端。
	if rec := s.do("GET", "/apiary", "", nil); rec.Code != 200 {
		t.Errorf("GET /apiary = %d，預期 200（前端路由）", rec.Code)
	}
	if rec := s.do("GET", "/assets/missing.js", "", nil); rec.Code != 404 {
		t.Errorf("GET /assets/missing.js = %d，預期 404", rec.Code)
	}
}

// 本機 socket（CLI 用）不提供 UI。
func TestUINotOnLocalSocket(t *testing.T) {
	s := newUITestServer(t, true)
	if rec := s.do("GET", "/library", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("本機 socket GET /library = %d，預期 404", rec.Code)
	}
}
