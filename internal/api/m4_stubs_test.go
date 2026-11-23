package api

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m4StubCases 是 M4 規格「介面」章節中尚未實作的路由（M4-S0 的 41 條；M4-S1 移除 NOTIFY 的 7 條，M4-S2 移除訂閱的 7 條，M4-S3 移除預覽與匯入的 3 條，M4-S6 移除 DIG 的 7 條，BND 移除 2 條，AUTH 移除 3 條）。
// 這份清單獨立於 production 的 m4Stubs，才抓得到 method、path、scope 寫錯。
var m4StubCases = []stubCase{
	// LIB
	{"POST", "/api/items/batch", auth.ScopeIngest},
	{"GET", "/api/overview", auth.ScopeRead},
	// GLOS
	{"POST", "/api/term-candidates/decide", auth.ScopeAdmin},
	{"GET", "/api/term-candidates", auth.ScopeRead},
	// SRCH
}

func TestM4StubRoutes(t *testing.T) {
	if len(m4Stubs) != len(m4StubCases) {
		t.Fatalf("production 有 %d 條 M4 stub，測試清單有 %d 條", len(m4Stubs), len(m4StubCases))
	}
	checkStubRoutes(t, m4StubCases)
}
