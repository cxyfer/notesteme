package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"reflect"
	"testing"
)

var updateOpenAPI = flag.Bool("update-openapi", false, "重新產生 internal/api/openapi.json")

// TestOpenAPISnapshot 確認 commit 的 openapi.json 與目前的路由一致（前端型別由它產生，NFR-CMP-3）。
// API 有變更時執行：go run ./internal/api/cmd/openapi -o internal/api/openapi.json
// （或 go test ./internal/api -run TestOpenAPISnapshot -update-openapi），再到 web/client 執行 npm run gen:api。
func TestOpenAPISnapshot(t *testing.T) {
	got, err := OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	if *updateOpenAPI {
		if err := os.WriteFile("openapi.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("internal/api/openapi.json 已過期：執行 go run ./internal/api/cmd/openapi -o internal/api/openapi.json，再到 web/client 執行 npm run gen:api")
	}
}

// 匯出的文件與正式 handler 的 GET /api/openapi.json 相同（不需要資料庫）。
func TestOpenAPISpecMatchesServed(t *testing.T) {
	s := newTestServer(t)
	// 正式環境有 SSE handler（不產生 events 的 stub operation）。
	d := s.deps
	d.Events = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	s.h = New(d)
	session, csrf := s.login(t)
	rec := s.do("GET", "/api/openapi.json", "", withCookies(session, csrf, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/openapi.json = %d", rec.Code)
	}
	var served, exported map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}
	b, err := OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &exported); err != nil {
		t.Fatal(err)
	}
	// 版本號不同（test 與 snapshot），其餘相同。
	served["info"].(map[string]any)["version"] = OpenAPISnapshotVersion
	if !reflect.DeepEqual(served, exported) {
		t.Error("OpenAPISpec 與 GET /api/openapi.json 的內容不同")
	}
}
