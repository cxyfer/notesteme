package api

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m5StubCases 是 HSHR 規格「介面」章節中尚未實作的 4 條 Client 路由（M5-S0）。
// 這份清單獨立於 production 的 m5Stubs，才抓得到 method、path、scope 寫錯。
var m5StubCases = []stubCase{
	{"GET", "/api/settings/hub", auth.ScopeAdmin},
	{"PUT", "/api/settings/hub", auth.ScopeAdmin},
	{"POST", "/api/items/x/hub/share", auth.ScopeAdmin},
	{"POST", "/api/hub/import", auth.ScopeIngest},
}

func TestM5StubRoutes(t *testing.T) {
	if len(m5Stubs) != len(m5StubCases) {
		t.Fatalf("production 有 %d 條 M5 stub，測試清單有 %d 條", len(m5Stubs), len(m5StubCases))
	}
	checkStubRoutes(t, m5StubCases)
}
