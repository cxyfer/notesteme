package hubapi

import (
	"github.com/danielgtaylor/huma/v2"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func nilLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// stubCases 是 HACC、HREP、HMCP 規格「介面」章節的路由與存取層級（M5-S0）。
// 這份清單獨立於 production 的 Stubs，才抓得到 method、path、權限寫錯。
var stubCases = []struct {
	method, path string
	access       Access
}{
	{"GET", "/api/v1/auth/providers", AccessPublic},
	{"POST", "/api/v1/auth/logout", AccessSession},
	{"GET", "/api/v1/me", AccessAuthenticated},
	{"POST", "/api/v1/me/identities/github/link", AccessSession},
	{"DELETE", "/api/v1/me/identities/github", AccessSession},
	{"GET", "/api/v1/me/tokens", AccessSession},
	{"POST", "/api/v1/me/tokens", AccessSession},
	{"DELETE", "/api/v1/me/tokens/t1", AccessSession},
	{"GET", "/api/v1/me/shares", "upload"},
	{"POST", "/api/v1/takedowns", AccessPublic},
	{"GET", "/api/v1/takedowns/t1", AccessPublic},
	{"GET", "/api/v1/admin/takedowns", "moderate"},
	{"POST", "/api/v1/admin/takedowns/t1/decision", "moderate"},
	{"GET", "/api/v1/admin/reports", "moderate"},
	{"POST", "/api/v1/admin/reports/r1/decision", "moderate"},
	{"POST", "/api/v1/admin/versions/v1/hide", "moderate"},
	{"GET", "/api/v1/admin/users", "admin"},
	{"PUT", "/api/v1/admin/users/u1/groups/contributors", "admin"},
	{"DELETE", "/api/v1/admin/users/u1/groups/contributors", "admin"},
	{"POST", "/api/v1/admin/users/u1/suspend", "admin"},
	{"POST", "/api/v1/admin/users/u1/unsuspend", "admin"},
	{"GET", "/api/v1/admin/audit", "admin"},
	{"POST", "/api/v1/shares", "upload"},
	{"DELETE", "/api/v1/shares/s1", "upload"},
	{"GET", "/api/v1/versions", "read"},
	{"GET", "/api/v1/versions/v1", "read"},
	{"GET", "/api/v1/videos/youtube/abc", "read"},
	{"GET", "/api/v1/versions/v1/download", "download"},
	{"GET", "/api/v1/versions/v1/content", "regenerate"},
	{"GET", "/api/v1/lookup", "regenerate"},
	{"PUT", "/api/v1/versions/v1/vote", "read"},
	{"DELETE", "/api/v1/versions/v1/vote", "read"},
	{"POST", "/api/v1/versions/v1/reports", "read"},
}

// 每條 stub 都回 501 not_implemented（不讀 body，不是 404 或 405），而且宣告的存取層級與規格相同。
func TestStubRoutes(t *testing.T) {
	if len(Stubs) != len(stubCases) {
		t.Fatalf("production 有 %d 條 stub，測試清單有 %d 條", len(Stubs), len(stubCases))
	}
	h := New(Deps{Store: &fakeStore{}})
	for _, c := range stubCases {
		rec := do(h, c.method, c.path)
		if rec.Code != http.StatusNotImplemented || decode(t, rec)["code"] != "not_implemented" {
			t.Errorf("%s %s = %d %s，預期 501 not_implemented", c.method, c.path, rec.Code, rec.Body)
		}
		if got := accessFor(c.method, c.path); got != c.access {
			t.Errorf("%s %s 的存取層級 = %q，預期 %q", c.method, c.path, got, c.access)
		}
	}
	// 瀏覽器流程與 MCP。
	for _, p := range []string{"/auth/google/start", "/auth/github/callback?code=x&state=y", "/mcp"} {
		m := "GET"
		if p == "/mcp" {
			m = "POST"
		}
		if rec := do(h, m, p); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d", m, p, rec.Code)
		}
	}
	// 沒有登記的路徑仍然是 404（stub 沒有吃掉其他路徑）。
	if rec := do(h, "GET", "/api/v1/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/v1/nope = %d", rec.Code)
	}
}

// accessFor 依 production 的表找出 method + 實際路徑的存取層級（{x} 對任何一段）。
func accessFor(method, path string) Access {
	for _, s := range Stubs {
		if s.Method == method && matchPattern(s.Path, path) {
			return s.Access
		}
	}
	return ""
}

func matchPattern(pattern, path string) bool {
	ps, xs := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if !strings.HasPrefix(ps[i], "{") && ps[i] != xs[i] {
			return false
		}
	}
	return true
}

// 每條 stub 的 OpenAPI operation 都帶著存取層級，Access 只能是已知的值。
func TestStubAccessValues(t *testing.T) {
	known := map[Access]bool{AccessPublic: true, AccessSession: true, AccessAuthenticated: true,
		"read": true, "download": true, "regenerate": true, "upload": true, "moderate": true, "admin": true}
	for _, s := range append(append([]Stub{}, Stubs...), RawStubs...) {
		if !known[s.Access] {
			t.Errorf("%s 的 Access %q 不是已知的值", s.ID, s.Access)
		}
	}
	api := newHumaAPI(http.NewServeMux(), Deps{})
	for path, item := range api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op != nil && strings.HasPrefix(path, "/api/v1/") && op.Extensions[AccessExtension] == nil {
				t.Errorf("%s 沒有 %s", path, AccessExtension)
			}
		}
	}
}
