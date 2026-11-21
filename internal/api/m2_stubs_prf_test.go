package api

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// prfStubCases 是 PRF（處理方案與頻道覆寫） 的 9 條 501 stub（契約第 4 節）；m2_stubs_prf.go 刪除時，這個檔案改寫成真正路由的 scope 測試。
var prfStubCases = []stubCase{
	{"GET", "/api/profiles", auth.ScopeRead},
	{"POST", "/api/profiles", auth.ScopeAdmin},
	{"GET", "/api/profiles/standard", auth.ScopeRead},
	{"PATCH", "/api/profiles/standard", auth.ScopeAdmin},
	{"DELETE", "/api/profiles/standard", auth.ScopeAdmin},
	{"GET", "/api/profiles/standard/versions", auth.ScopeRead},
	{"POST", "/api/profiles/standard/default", auth.ScopeAdmin},
	{"GET", "/api/channels/c1/overrides", auth.ScopeRead},
	{"PATCH", "/api/channels/c1/overrides", auth.ScopeAdmin},
}

func TestPRFStubRoutes(t *testing.T) {
	if len(prfStubCases) != 9 {
		t.Fatalf("契約第 4 節的 PRF（處理方案與頻道覆寫） 有 9 條路由，測試清單有 %d 條", len(prfStubCases))
	}
	checkStubRoutes(t, prfStubCases)
}

func TestPRFStubsMatchContract(t *testing.T) {
	checkStubsMatch(t, "prfStubs", prfStubs, prfStubCases)
}
