package fakehub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/hub/hubapi"
)

// openAPIDoc 是 Hub 的 OpenAPI（只解析契約檢查用到的部分）。
type openAPIDoc struct {
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]map[string]any `json:"schemas"`
	} `json:"components"`
}

func hubOpenAPI(t *testing.T) openAPIDoc {
	t.Helper()
	b, err := hubapi.OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	var doc openAPIDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// 契約漂移（ADR-0030 第 3 點）：fakehub 實作的每一條路由都要存在於真 Hub 的 OpenAPI。
func TestRoutesExistInHubOpenAPI(t *testing.T) {
	doc := hubOpenAPI(t)
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

// 契約漂移的第二半（ADR-0030 第 3 點）：fakehub 每一條路由的成功回應，都要有 Hub 在 OpenAPI 宣告的必要欄位，
// 而且型別相符。Hub 的 OpenAPI 由 Huma 依回應型別產生，沒有 omitempty 的欄位就是 required，
// 所以 OpenAPI 的 required 等同真 Hub 每次回應都會帶的欄位（HSHR 待決問題 S22）。
// fakehub 可以多帶欄位；巢狀的物件與陣列元素也要符合（seeding 讓每個陣列至少有一個元素）。
// content 路由的本體是原始位元組，改為檢查 Hub 宣告的 X-Notesteme-* 標頭。
func TestResponsesHaveHubRequiredFields(t *testing.T) {
	doc := hubOpenAPI(t)
	h := New(token)
	id := h.AddVersion(Version{
		Platform: "youtube", ExternalID: "contractxxx", Kind: "transcript", Variant: "source", Lang: "en",
		Uploader: "alice", TranscribeModel: "whisper-large-v3", License: "CC-BY-4.0", Score: 3,
		Content: []byte(`{"cues":[]}`),
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	manifest := `{"bundle_version":1,"item":{"platform":"youtube","external_id":"contractxxx","part":null},` +
		`"artifacts":[{"kind":"transcript","variant":"source","lang":"en","path":"artifacts/a.json"}],` +
		`"files":[{"path":"artifacts/a.json","sha256":"` + strings.Repeat("ab", 32) + `"}]}`
	shareBody, shareCT := multipartBody(t, bundle(t, manifest, map[string]string{"artifacts/a.json": `{"cues":[]}`}),
		map[string]string{"attest": "true"})
	prefix := VideoKeySHA256("youtube", "contractxxx", "")[:4]

	calls := map[string]struct {
		path   string
		status int
	}{
		"GET /api/v1/me":                    {"/api/v1/me", http.StatusOK},
		"POST /api/v1/shares":               {"/api/v1/shares", http.StatusCreated},
		"GET /api/v1/lookup":                {"/api/v1/lookup?platform=youtube&prefix=" + prefix, http.StatusOK},
		"GET /api/v1/versions/{id}":         {"/api/v1/versions/" + id, http.StatusOK},
		"GET /api/v1/versions/{id}/content": {"/api/v1/versions/" + id + "/content", http.StatusOK},
	}
	for _, r := range Routes {
		c, ok := calls[r]
		if !ok {
			t.Errorf("fakehub.Routes 多了 %s：契約測試要為它加上請求", r)
			continue
		}
		method, path, _ := strings.Cut(r, " ")
		var (
			code   int
			body   []byte
			header http.Header
		)
		if method == http.MethodPost {
			code, body, header = do(t, srv, method, c.path, shareBody, shareCT, token)
		} else {
			code, body, header = do(t, srv, method, c.path, nil, "", token)
		}
		if code != c.status {
			t.Errorf("%s：fakehub 回 %d，契約測試預期 %d：%s", r, code, c.status, body)
			continue
		}
		resp := successResponse(t, doc, path, method, code)
		if strings.HasSuffix(path, "/content") {
			for _, name := range []string{"X-Notesteme-SHA256", "X-Notesteme-License"} {
				if _, declared := resp.Headers[name]; !declared {
					t.Errorf("%s：Hub 的 OpenAPI 沒有宣告標頭 %s（契約測試要更新）", r, name)
				}
				if header.Get(name) == "" {
					t.Errorf("%s：fakehub 沒有回應標頭 %s", r, name)
				}
			}
			continue
		}
		schema := resp.Content["application/json"].Schema
		if schema == nil {
			t.Errorf("%s：Hub 的 OpenAPI 沒有 JSON 回應的 schema", r)
			continue
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Errorf("%s：fakehub 的回應不是 JSON：%v", r, err)
			continue
		}
		for _, p := range checkSchema(doc, schema, v, "$") {
			t.Errorf("%s：%s", r, p)
		}
	}
}

type openAPIResponse struct {
	Headers map[string]any `json:"headers"`
	Content map[string]struct {
		Schema map[string]any `json:"schema"`
	} `json:"content"`
}

// successResponse 回傳 Hub 的 OpenAPI 中這個操作的 code（應為 2xx）回應。
func successResponse(t *testing.T, doc openAPIDoc, path, method string, code int) openAPIResponse {
	t.Helper()
	var op struct {
		Responses map[string]openAPIResponse `json:"responses"`
	}
	if err := json.Unmarshal(doc.Paths[path][strings.ToLower(method)], &op); err != nil {
		t.Fatal(err)
	}
	resp, ok := op.Responses[fmt.Sprint(code)]
	if !ok {
		t.Fatalf("Hub 的 OpenAPI 中 %s %s 沒有 %d 回應", method, path, code)
	}
	return resp
}

// checkSchema 回傳 v 不符合 schema 的地方：缺少 required 欄位、型別不符；遞迴檢查物件的欄位與陣列的元素。
func checkSchema(doc openAPIDoc, schema map[string]any, v any, at string) []string {
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		s, ok := doc.Components.Schemas[name]
		if !ok {
			return []string{fmt.Sprintf("%s：找不到 schema %s", at, ref)}
		}
		schema = s
	}
	types := schemaTypes(schema)
	if v == nil {
		if len(types) > 0 && !slices.Contains(types, "null") {
			return []string{fmt.Sprintf("%s 是 null，Hub 宣告的型別是 %v", at, types)}
		}
		return nil
	}
	if got := jsonType(v); len(types) > 0 && !slices.Contains(types, got) && !(got == "integer" && slices.Contains(types, "number")) {
		return []string{fmt.Sprintf("%s 的型別是 %s，Hub 宣告的是 %v", at, got, types)}
	}
	var out []string
	switch val := v.(type) {
	case map[string]any:
		req, _ := schema["required"].([]any)
		for _, k := range req {
			if _, ok := val[k.(string)]; !ok {
				out = append(out, fmt.Sprintf("%s 缺少 Hub 的必要欄位 %q", at, k))
			}
		}
		props, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if fv, ok := val[k]; ok {
				ps, _ := props[k].(map[string]any)
				out = append(out, checkSchema(doc, ps, fv, at+"."+k)...)
			}
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for i, e := range val {
			out = append(out, checkSchema(doc, items, e, fmt.Sprintf("%s[%d]", at, i))...)
		}
	}
	return out
}

// schemaTypes 回傳 schema 的 type（字串或字串陣列）；沒有宣告時是 nil。
func schemaTypes(schema map[string]any) []string {
	switch t := schema["type"].(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, s := range t {
			out = append(out, fmt.Sprint(s))
		}
		return out
	}
	return nil
}

func jsonType(v any) string {
	switch n := v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		if n == float64(int64(n)) {
			return "integer"
		}
		return "number"
	}
	return "unknown"
}

// 契約測試本身要能抓到缺欄位：拿掉一個必要欄位時要報錯。
func TestCheckSchemaReportsMissingRequiredField(t *testing.T) {
	doc := hubOpenAPI(t)
	v := map[string]any{"id": "x", "display_name": "owner", "permissions": []any{}, "groups": []any{}}
	got := checkSchema(doc, map[string]any{"$ref": "#/components/schemas/Me"}, v, "$")
	if len(got) != 1 || !strings.Contains(got[0], `"identities"`) {
		t.Fatalf("got %v", got)
	}
	v["identities"] = []any{map[string]any{"provider": "github"}}
	got = checkSchema(doc, map[string]any{"$ref": "#/components/schemas/Me"}, v, "$")
	if len(got) != 1 || !strings.Contains(got[0], `"login"`) {
		t.Fatalf("nested: got %v", got)
	}
}
