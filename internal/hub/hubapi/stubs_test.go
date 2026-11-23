package hubapi

import (
	"github.com/danielgtaylor/huma/v2"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func nilLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

type routeCase struct {
	method, path string
	access       Access
}

// implementedCases 是 M5-S1（auth.go）、M5-S2（tokens.go）、M5-S3（repository.go）、M5-S4（listing.go、download.go）與 M5-S5 實作的 /api/v1 路由與存取層級；OpenAPI 的 x-notesteme-access 要相同。
var implementedCases = []routeCase{
	{"GET", "/api/v1/auth/providers", AccessPublic},
	{"POST", "/api/v1/auth/logout", AccessSession},
	{"GET", "/api/v1/me", AccessAuthenticated},
	{"POST", "/api/v1/me/identities/github/link", AccessSession},
	{"DELETE", "/api/v1/me/identities/github", AccessSession},
	{"GET", "/api/v1/me/tokens", AccessSession},
	{"POST", "/api/v1/me/tokens", AccessSession},
	{"DELETE", "/api/v1/me/tokens/t1", AccessSession},
	{"PUT", "/api/v1/admin/users/u1/groups/contributors", "admin"},
	{"DELETE", "/api/v1/admin/users/u1/groups/contributors", "admin"},
	{"GET", "/api/v1/admin/audit", "admin"},
	{"POST", "/api/v1/shares", "upload"},
	{"DELETE", "/api/v1/shares/s1", "upload"},
	{"GET", "/api/v1/versions/v1", "read"},
	{"GET", "/api/v1/versions", "read"},
	{"GET", "/api/v1/videos/youtube/abc", "read"},
	{"GET", "/api/v1/versions/v1/download", "download"},
	{"GET", "/api/v1/versions/v1/content", "regenerate"},
	{"GET", "/api/v1/lookup", "regenerate"},
	// M5-S5（community.go、takedowns.go、moderation.go）。
	{"GET", "/api/v1/me/shares", "upload"},
	{"POST", "/api/v1/takedowns", AccessPublic},
	{"GET", "/api/v1/takedowns/t1", AccessPublic},
	{"GET", "/api/v1/admin/takedowns", "moderate"},
	{"POST", "/api/v1/admin/takedowns/t1/decision", "moderate"},
	{"GET", "/api/v1/admin/reports", "moderate"},
	{"POST", "/api/v1/admin/reports/r1/decision", "moderate"},
	{"POST", "/api/v1/admin/versions/v1/hide", "moderate"},
	{"GET", "/api/v1/admin/users", "admin"},
	{"POST", "/api/v1/admin/users/u1/suspend", "admin"},
	{"POST", "/api/v1/admin/users/u1/unsuspend", "admin"},
	{"PUT", "/api/v1/versions/v1/vote", "read"},
	{"DELETE", "/api/v1/versions/v1/vote", "read"},
	{"POST", "/api/v1/versions/v1/reports", "read"},
}

// stubCases 是 HACC、HREP 規格「介面」章節中還沒有實作的 Huma 路由（M5-S5 之後沒有；HMCP 的 /mcp 在 RawStubs）。
// 這份清單獨立於 production 的 Stubs，才抓得到 method、path、權限寫錯。
var stubCases = []routeCase{}

// 每條 stub 都宣告與規格相同的存取層級；公開的 stub 回 501 not_implemented（不讀 body，不是 404 或 405），
// 其他的 stub 在沒有登入時先被 middleware 擋下，回 401 unauthorized（HACC-R13：身分在讀取 body 之前檢查）。
func TestStubRoutes(t *testing.T) {
	if len(Stubs) != len(stubCases) {
		t.Fatalf("production 有 %d 條 stub，測試清單有 %d 條", len(Stubs), len(stubCases))
	}
	h := New(Deps{Store: &fakeStore{}})
	for _, c := range stubCases {
		rec := do(h, c.method, c.path)
		want, code := http.StatusUnauthorized, "unauthorized"
		if c.access == AccessPublic {
			want, code = http.StatusNotImplemented, "not_implemented"
		}
		if rec.Code != want || decode(t, rec)["code"] != code {
			t.Errorf("%s %s = %d %s，預期 %d %s", c.method, c.path, rec.Code, rec.Body, want, code)
		}
		if got := accessFor(c.method, c.path); got != c.access {
			t.Errorf("%s %s 的存取層級 = %q，預期 %q", c.method, c.path, got, c.access)
		}
	}
	// MCP（S6）。
	if rec := do(h, "POST", "/mcp"); rec.Code != http.StatusNotImplemented {
		t.Errorf("POST /mcp = %d", rec.Code)
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
	rt := newHumaAPI(http.NewServeMux(), Deps{})
	ops := map[string]*huma.Operation{}
	for path, item := range rt.api.OpenAPI().Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil {
				continue
			}
			ops[op.Method+" "+path] = op
			// 每個 operation（含 /healthz、/readyz）都宣告存取層級；沒有宣告的 middleware 當作要登入。
			if v, _ := op.Extensions[AccessExtension].(string); !known[Access(v)] {
				t.Errorf("%s %s 的 %s = %v", op.Method, path, AccessExtension, op.Extensions[AccessExtension])
			}
		}
	}
	for _, c := range implementedCases {
		found := false
		for k, op := range ops {
			if strings.HasPrefix(k, c.method+" ") && matchPattern(op.Path, c.path) {
				found = true
				if got := accessOf(op); got != c.access {
					t.Errorf("%s %s 的存取層級 = %q，預期 %q", c.method, c.path, got, c.access)
				}
			}
		}
		if !found {
			t.Errorf("%s %s 沒有註冊", c.method, c.path)
		}
	}
}
