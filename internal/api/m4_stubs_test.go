package api

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m4StubCases 是 M4 規格「介面」章節中尚未實作的路由（M4-S0 的 41 條；M4-S1 移除 NOTIFY 的 7 條）。
// 這份清單獨立於 production 的 m4Stubs，才抓得到 method、path、scope 寫錯。
var m4StubCases = []stubCase{
	// SUB
	{"GET", "/api/subscriptions", auth.ScopeRead},
	{"POST", "/api/subscriptions", auth.ScopeAdmin},
	{"GET", "/api/subscriptions/s1", auth.ScopeRead},
	{"PATCH", "/api/subscriptions/s1", auth.ScopeAdmin},
	{"DELETE", "/api/subscriptions/s1", auth.ScopeAdmin},
	{"GET", "/api/subscriptions/s1/entries", auth.ScopeRead},
	{"POST", "/api/subscriptions/s1/preview", auth.ScopeAdmin},
	{"POST", "/api/subscriptions/preview", auth.ScopeAdmin},
	{"POST", "/api/subscriptions/s1/check", auth.ScopeAdmin},
	{"POST", "/api/subscriptions/import", auth.ScopeAdmin},
	// DIG
	{"POST", "/api/items/x/continue", auth.ScopeIngest},
	{"POST", "/api/items/continue", auth.ScopeIngest},
	{"GET", "/api/digests", auth.ScopeAdmin},
	{"POST", "/api/digests", auth.ScopeAdmin},
	{"PATCH", "/api/digests/daily", auth.ScopeAdmin},
	{"DELETE", "/api/digests/daily", auth.ScopeAdmin},
	{"POST", "/api/digests/daily/preview", auth.ScopeAdmin},
	// COST
	{"GET", "/api/prices", auth.ScopeRead},
	{"PUT", "/api/prices", auth.ScopeAdmin},
	{"DELETE", "/api/prices", auth.ScopeAdmin},
	{"POST", "/api/prices/confirm", auth.ScopeAdmin},
	{"GET", "/api/costs", auth.ScopeRead},
	{"GET", "/api/costs/entries", auth.ScopeRead},
	{"GET", "/api/costs/export.csv", auth.ScopeRead},
	// LIB
	{"POST", "/api/items/batch", auth.ScopeIngest},
	{"GET", "/api/overview", auth.ScopeRead},
	// BND
	{"GET", "/api/items/x/bundle", auth.ScopeRead},
	{"POST", "/api/bundles/import", auth.ScopeAdmin},
	// GLOS
	{"POST", "/api/term-candidates/decide", auth.ScopeAdmin},
	{"GET", "/api/term-candidates", auth.ScopeRead},
	// SRCH
	{"GET", "/api/search", auth.ScopeRead},
	// AUTH
	{"GET", "/api/auth/sessions", auth.ScopeAdmin},
	{"DELETE", "/api/auth/sessions/current", auth.ScopeAdmin},
	{"POST", "/api/tokens/t1/rotate", auth.ScopeAdmin},
}

func TestM4StubRoutes(t *testing.T) {
	if len(m4Stubs) != len(m4StubCases) {
		t.Fatalf("production 有 %d 條 M4 stub，測試清單有 %d 條", len(m4Stubs), len(m4StubCases))
	}
	checkStubRoutes(t, m4StubCases)
}
