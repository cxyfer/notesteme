package fakehub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/hub/hubapi"
)

// 契約漂移（ADR-0030 第 3 點）：fakehub 實作的每一條路由都要存在於真 Hub 的 OpenAPI。
// 回應的必要欄位比對等真 Hub 實作這些路由之後再加（S3、S4，見 docs/handoff.md）。
func TestRoutesExistInHubOpenAPI(t *testing.T) {
	b, err := hubapi.OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, r := range Routes {
		method, path, ok := strings.Cut(r, " ")
		if !ok {
			t.Fatalf("Routes 的格式不對：%q", r)
		}
		ops, ok := doc.Paths[path]
		if !ok {
			t.Errorf("Hub 的 OpenAPI 沒有路徑 %s", path)
			continue
		}
		if _, ok := ops[strings.ToLower(method)]; !ok {
			t.Errorf("Hub 的 OpenAPI 沒有 %s", r)
		}
	}
}
