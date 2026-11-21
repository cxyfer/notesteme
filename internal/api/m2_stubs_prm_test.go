package api

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
)

// prmStubCases 是 PRM（prompt 庫，不含 playground 與 compare） 的 6 條 501 stub（契約第 4 節）；m2_stubs_prm.go 刪除時，這個檔案改寫成真正路由的 scope 測試。
var prmStubCases = []stubCase{
	{"GET", "/api/prompts", auth.ScopeRead},
	{"POST", "/api/prompts", auth.ScopeAdmin},
	{"DELETE", "/api/prompts/p1", auth.ScopeAdmin},
	{"GET", "/api/prompts/p1/versions", auth.ScopeRead},
	{"POST", "/api/prompts/p1/versions", auth.ScopeAdmin},
	{"POST", "/api/prompts/p1/default", auth.ScopeAdmin},
}

func TestPRMStubRoutes(t *testing.T) {
	if len(prmStubCases) != 6 {
		t.Fatalf("契約第 4 節的 PRM（prompt 庫，不含 playground 與 compare） 有 6 條路由，測試清單有 %d 條", len(prmStubCases))
	}
	checkStubRoutes(t, prmStubCases)
}

func TestPRMStubsMatchContract(t *testing.T) {
	checkStubsMatch(t, "prmStubs", prmStubs, prmStubCases)
}
