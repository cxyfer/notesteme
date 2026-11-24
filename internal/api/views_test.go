package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

func decodeJSON[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return v
}

// LIB-R26～R28：建立、列出（建立順序）、名稱不分大小寫不得重複、刪除。
func TestViewsLifecycle(t *testing.T) {
	r := newTokenRig(t)
	rec := r.owner("POST", "/api/views", `{"name":"  等你決定  ","query":{"blocked":"awaiting_user","status":["metadata:done","metadata:done"],"group":"status","dur_min":0}}`)
	if rec.Code != 201 {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	created := decodeJSON[ViewBody](t, rec.Body.Bytes())
	if created.Name != "等你決定" || created.ID == "" || len(created.Query.Status) != 1 || created.Query.DurMin == nil || *created.Query.DurMin != 0 {
		t.Fatalf("建立 = %+v", created)
	}
	if rec := r.owner("POST", "/api/views", `{"name":"B站","query":{"platform":"bilibili","published_from":"2026-01-31"}}`); rec.Code != 201 {
		t.Fatalf("第二個 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/views", `{"name":"b站","query":{"platform":"youtube"}}`)
	if rec.Code != 409 || errCode(t, rec) != CodeViewExists {
		t.Fatalf("同名 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("GET", "/api/views", "")
	list := decodeJSON[struct{ Views []ViewBody }](t, rec.Body.Bytes())
	if rec.Code != 200 || len(list.Views) != 2 || list.Views[0].ID != created.ID || list.Views[1].Query.Platform != "bilibili" {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"q":`) {
		t.Errorf("沒有的參數要省略：%s", rec.Body)
	}
	if rec := r.owner("DELETE", "/api/views/"+created.ID, ""); rec.Code != 204 {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("DELETE", "/api/views/"+created.ID, ""); rec.Code != 404 || errCode(t, rec) != CodeNotFound {
		t.Fatalf("再刪一次 = %d %s", rec.Code, rec.Body)
	}
}

// LIB-R27：驗證失敗回 422，errors[].path 指到欄位；未知的參數也是 422。
func TestViewsValidation(t *testing.T) {
	r := newTokenRig(t)
	cases := []struct {
		name, body, path string
	}{
		{"空名稱", `{"name":"  ","query":{"platform":"youtube"}}`, "name"},
		{"名稱太長", `{"name":"` + strings.Repeat("長", 61) + `","query":{"platform":"youtube"}}`, "name"},
		{"控制字元", `{"name":"a\tb","query":{"platform":"youtube"}}`, "name"},
		{"沒有條件", `{"name":"x","query":{}}`, "query"},
		{"只有空白", `{"name":"x","query":{"q":"  "}}`, "query"},
		{"平台", `{"name":"x","query":{"platform":"vimeo"}}`, "query.platform"},
		{"步驟", `{"name":"x","query":{"status":["nope:done"]}}`, "query.status"},
		{"狀態格式", `{"name":"x","query":{"status":["metadata"]}}`, "query.status"},
		{"逗號", `{"name":"x","query":{"status":["metadata:done,normalize:done"]}}`, "query.status"},
		{"錯誤類別", `{"name":"x","query":{"error_class":"oops"}}`, "query.error_class"},
		{"日期", `{"name":"x","query":{"published_to":"2026-02-30"}}`, "query.published_to"},
		{"RFC 3339 不收", `{"name":"x","query":{"published_from":"2026-01-01T00:00:00Z"}}`, "query.published_from"},
		{"長度", `{"name":"x","query":{"dur_max":-1}}`, "query.dur_max"},
		{"timing", `{"name":"x","query":{"timing":"guess"}}`, "query.timing"},
		{"blocked", `{"name":"x","query":{"blocked":"Bad-Reason"}}`, "query.blocked"},
		{"read_state", `{"name":"x","query":{"read_state":"seen"}}`, "query.read_state"},
		{"group", `{"name":"x","query":{"group":"channel"}}`, "query.group"},
		{"q 太長", `{"name":"x","query":{"q":"` + strings.Repeat("字", 201) + `"}}`, "query.q"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := r.owner("POST", "/api/views", c.body)
			if rec.Code != 422 || errCode(t, rec) != CodeInvalidInput {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			e := decodeJSON[Error](t, rec.Body.Bytes())
			found := false
			for _, f := range e.Errors {
				found = found || strings.HasPrefix(f.Path, c.path)
			}
			if !found {
				t.Errorf("errors 沒有 %s：%s", c.path, rec.Body)
			}
		})
	}
	// 不認識的參數（例如顯示方式 view、API 的 duration_min）：422，不建立。
	for _, body := range []string{
		`{"name":"x","query":{"platform":"youtube","view":"grid"}}`,
		`{"name":"x","query":{"duration_min":60}}`,
		`{"name":"x","query":{"platform":"youtube"},"extra":1}`,
	} {
		if rec := r.owner("POST", "/api/views", body); rec.Code != 422 {
			t.Errorf("%s：%d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := r.owner("GET", "/api/views", ""); !strings.Contains(rec.Body.String(), `"views":[]`) {
		t.Errorf("驗證失敗不應建立：%s", rec.Body)
	}
}

// LIB-R27：數量上限。
func TestViewsLimit(t *testing.T) {
	r := newTokenRig(t)
	for i := range MaxViews {
		if rec := r.owner("POST", "/api/views", fmt.Sprintf(`{"name":"v%d","query":{"platform":"youtube"}}`, i)); rec.Code != 201 {
			t.Fatalf("%d：%d %s", i, rec.Code, rec.Body)
		}
	}
	rec := r.owner("POST", "/api/views", `{"name":"one more","query":{"platform":"youtube"}}`)
	if rec.Code != 409 || errCode(t, rec) != CodeViewLimit {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// LIB-R26～R28：列出需要 read；建立與刪除需要 admin。
func TestViewsScopes(t *testing.T) {
	r := newTokenRig(t)
	rec := r.owner("POST", "/api/tokens", `{"name":"r","scopes":["read","ingest"]}`)
	tok := decodeJSON[struct{ Token string }](t, rec.Body.Bytes())
	if rec := r.do("GET", "/api/views", "", withBearer(tok.Token)); rec.Code != 200 {
		t.Errorf("GET：%d %s", rec.Code, rec.Body)
	}
	rec = r.do("POST", "/api/views", `{"name":"x","query":{"platform":"youtube"}}`, withBearer(tok.Token))
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"required_scope":"admin"`) {
		t.Errorf("POST：%d %s", rec.Code, rec.Body)
	}
	if rec := r.do("DELETE", "/api/views/x", "", withBearer(tok.Token)); rec.Code != 403 {
		t.Errorf("DELETE：%d %s", rec.Code, rec.Body)
	}
}

// LIB-R27：媒體庫能勾的 status（每個步驟的每個狀態）都存得下。
func TestViewsAllStatuses(t *testing.T) {
	r := newTokenRig(t)
	statuses := []string{"pending", "running", "waiting", "awaiting_agent", "blocked", "done", "skipped", "failed"}
	var all []string
	for _, st := range pipeline.Stages {
		for _, s := range statuses {
			all = append(all, `"`+st+":"+s+`"`)
		}
	}
	if len(all) != maxViewStatus {
		t.Fatalf("步驟 × 狀態 = %d，maxViewStatus = %d", len(all), maxViewStatus)
	}
	rec := r.owner("POST", "/api/views", `{"name":"all","query":{"status":[`+strings.Join(all, ",")+`]}}`)
	if rec.Code != 201 {
		t.Fatalf("%d %.300s", rec.Code, rec.Body)
	}
}
