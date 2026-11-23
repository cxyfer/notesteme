package api

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m3StubCases 是 M3 規格「介面」章節中尚未實作的路由（M3-S0；frames 的兩條已在 M3-S3 實作）。
// 這份清單獨立於 production 的 m3Stubs，才抓得到 method、path、scope 寫錯。
var m3StubCases = []stubCase{
	{"GET", "/api/items/x/note", auth.ScopeRead},
	{"GET", "/api/note-templates", auth.ScopeRead},
	{"POST", "/api/note-templates", auth.ScopeAdmin},
	{"PATCH", "/api/note-templates/video-notes", auth.ScopeAdmin},
	{"POST", "/api/note-templates/video-notes/preview", auth.ScopeAdmin},
	{"GET", "/api/vaults", auth.ScopeAdmin},
}

func TestM3StubRoutes(t *testing.T) {
	if len(m3Stubs) != len(m3StubCases) {
		t.Fatalf("production 有 %d 條 M3 stub，測試清單有 %d 條", len(m3Stubs), len(m3StubCases))
	}
	checkStubRoutes(t, m3StubCases)
}
