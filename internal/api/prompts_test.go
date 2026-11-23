package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/prompts"
	"github.com/cxyfer/notesteme/internal/store"
)

// promptsRig 是接上 prompt 庫的 rig；usedBy 決定「誰在用這個 prompt」（nil 代表沒有人用）。
func promptsRig(t *testing.T, usedBy func(context.Context, *store.Repo, string) ([]string, error)) *itemsRig {
	t.Helper()
	return newItemsRigAPI(t, func(d *Deps) {
		d.Prompts = prompts.New(prompts.Deps{Store: d.Store, Clock: d.Clock, UsedBy: usedBy})
	})
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("回應不是 JSON 物件：%v\n%s", err, rec.Body)
	}
	return m
}

// 跟著 PRM 的場景走一遍：建立、未知類型、新增版本、未定義的變數、內建不能改、設為預設、刪除。
func TestPromptsAPI_Lifecycle(t *testing.T) {
	r := promptsRig(t, nil)

	rec := r.owner("POST", "/api/prompts", `{"id":"my-correct","kind":"correct","body":"你是字幕校對員。\n詞彙表：{{glossary}}"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建立 = %d %s", rec.Code, rec.Body)
	}
	created := jsonOf(t, rec)
	if created["id"] != "my-correct" || created["kind"] != "correct" || created["version"] != float64(1) ||
		created["default_version"] != float64(1) || created["builtin"] != false || len(created["sha256"].(string)) != 64 {
		t.Errorf("建立的回應 = %v", created)
	}

	// PRM-R1：未知的類型 → 422，errors[0].path 是 kind。
	rec = r.owner("POST", "/api/prompts", `{"id":"my-poem","kind":"poem","body":"寫一首詩"}`)
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != http.StatusUnprocessableEntity || e.Code != CodeInvalidInput || len(e.Errors) == 0 || e.Errors[0].Path != "kind" {
		t.Errorf("未知的類型 = %d %s", rec.Code, rec.Body)
	}
	// 缺少必填欄位也走逐欄位的 422（errors[].path），不是框架的 invalid_request。
	for body, path := range map[string]string{
		`{"id":"my-x","body":"x"}`:       "kind",
		`{"id":"my-x","kind":"correct"}`: "body",
		`{"kind":"correct","body":"x"}`:  "id",
	} {
		rec = r.owner("POST", "/api/prompts", body)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusUnprocessableEntity || e.Code != CodeInvalidInput || len(e.Errors) == 0 || e.Errors[0].Path != path {
			t.Errorf("%s = %d %s，預期 path %s", body, rec.Code, rec.Body, path)
		}
	}
	rec = r.owner("POST", "/api/prompts/my-correct/versions", `{}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusUnprocessableEntity || len(e.Errors) == 0 || e.Errors[0].Path != "body" {
		t.Errorf("新版本缺 body = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/prompts/my-correct/default", `{}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusUnprocessableEntity || len(e.Errors) == 0 || e.Errors[0].Path != "version" {
		t.Errorf("設為預設缺 version = %d %s", rec.Code, rec.Body)
	}
	// 同 id 已存在、和內建同 id。
	rec = r.owner("POST", "/api/prompts", `{"id":"my-correct","kind":"correct","body":"x"}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != CodePromptExists {
		t.Errorf("重複建立 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/prompts", `{"id":"correct-default","kind":"correct","body":"x"}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != CodeBuiltinPrompt {
		t.Errorf("和內建同 id = %d %s", rec.Code, rec.Body)
	}

	// PRM-R2：新增版本 → 201，版本列表有兩個；預設版本不變。
	rec = r.owner("POST", "/api/prompts/my-correct/versions", `{"body":"你是字幕校對員。保留口語語氣。\n詞彙表：{{glossary}}"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("新增版本 = %d %s", rec.Code, rec.Body)
	}
	if v := jsonOf(t, rec); v["id"] != "my-correct" || v["version"] != float64(2) || len(v["sha256"].(string)) != 64 {
		t.Errorf("新增版本的回應 = %v", v)
	}
	rec = r.owner("GET", "/api/prompts/my-correct/versions", "")
	vers := decode[struct {
		ID       string              `json:"id"`
		Versions []PromptVersionBody `json:"versions"`
	}](t, rec)
	if rec.Code != 200 || vers.ID != "my-correct" || len(vers.Versions) != 2 || vers.Versions[0].Version != 1 ||
		vers.Versions[1].Version != 2 || !slices.Equal(vers.Versions[1].Variables, []string{"glossary"}) {
		t.Errorf("版本列表 = %d %s", rec.Code, rec.Body)
	}

	// PRM-R3：未定義的變數 → 422，undefined_variables 列出名稱，errors[0].path 是 body。
	rec = r.owner("POST", "/api/prompts/my-correct/versions", `{"body":"標題：{{titel}}"}`)
	e = decodeError(t, rec.Body.Bytes())
	if rec.Code != http.StatusUnprocessableEntity || !slices.Equal(e.UndefinedVariables, []string{"titel"}) ||
		len(e.Errors) == 0 || e.Errors[0].Path != "body" {
		t.Errorf("未定義的變數 = %d %s", rec.Code, rec.Body)
	}
	// 沒有留下版本。
	if rec := r.owner("GET", "/api/prompts/my-correct/versions", ""); strings.Count(rec.Body.String(), `"sha256"`) != 2 {
		t.Errorf("失敗之後的版本列表 = %s", rec.Body)
	}

	// PRM-R4：內建的不能新增版本、不能設預設、不能刪除；內建的版本可以讀。
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/prompts/correct-default/versions", `{"body":"something else"}`},
		{"POST", "/api/prompts/correct-default/default", `{"version":1}`},
		{"DELETE", "/api/prompts/correct-default", ""},
	} {
		rec := r.owner(c.method, c.path, c.body)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != CodeBuiltinPrompt {
			t.Errorf("%s %s = %d %s，預期 409 builtin_prompt", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if rec := r.owner("GET", "/api/prompts/localize-zh-hant-tw/versions", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"version":1`) {
		t.Errorf("內建的版本 = %d %s", rec.Code, rec.Body)
	}

	// PRM-R5：設為預設；版本不存在 → 422，path 是 version。
	rec = r.owner("POST", "/api/prompts/my-correct/default", `{"version":9}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusUnprocessableEntity || len(e.Errors) == 0 || e.Errors[0].Path != "version" {
		t.Errorf("不存在的版本 = %d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/prompts/my-correct/default", `{"version":2}`)
	if v := jsonOf(t, rec); rec.Code != 200 || v["id"] != "my-correct" || v["default_version"] != float64(2) {
		t.Errorf("設為預設 = %d %s", rec.Code, rec.Body)
	}

	// 列表：內建的在前（依 id 排序），接著是自訂的。
	rec = r.owner("GET", "/api/prompts", "")
	list := decode[struct {
		Prompts []PromptSummary `json:"prompts"`
	}](t, rec)
	var ids []string
	for _, p := range list.Prompts {
		ids = append(ids, p.ID)
	}
	want := []string{"chapters-default", "correct-default", "frames-default", "localize-zh-hans-cn", "localize-zh-hant-tw", "note-gemini-default", "segment-default", "translate-default", "my-correct"}
	if rec.Code != 200 || !slices.Equal(ids, want) {
		t.Fatalf("列表 = %d %v，預期 %v", rec.Code, ids, want)
	}
	if mine := list.Prompts[len(list.Prompts)-1]; mine.Builtin || mine.DefaultVersion != 2 || mine.LatestVersion != 2 || mine.Kind != "correct" {
		t.Errorf("my-correct = %+v", mine)
	}
	if !list.Prompts[0].Builtin {
		t.Error("內建的 prompt 應標示 builtin")
	}

	// 刪除 → 204；之後 404。
	if rec := r.owner("DELETE", "/api/prompts/my-correct", ""); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("刪除 = %d %s", rec.Code, rec.Body)
	}
	for _, c := range [][2]string{{"DELETE", "/api/prompts/my-correct"}, {"GET", "/api/prompts/my-correct/versions"}} {
		rec := r.owner(c[0], c[1], "")
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusNotFound || e.Code != CodeNotFound {
			t.Errorf("%s %s = %d %s，預期 404 not_found", c[0], c[1], rec.Code, rec.Body)
		}
	}
	if rec := r.owner("POST", "/api/prompts/my-correct/versions", `{"body":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("對不存在的 prompt 新增版本 = %d", rec.Code)
	}
	if rec := r.owner("POST", "/api/prompts/my-correct/default", `{"version":1}`); rec.Code != http.StatusNotFound {
		t.Errorf("對不存在的 prompt 設預設 = %d", rec.Code)
	}
}

// PRM-R8：被引用時刪除回 409 prompt_in_use，used_by 照 Deps.UsedBy 的順序列出；沒有人引用後可以刪除。
func TestPromptsAPI_DeleteInUse(t *testing.T) {
	users := []string{"standard", "settings", "channel:youtube:UC1"}
	r := promptsRig(t, func(_ context.Context, _ *store.Repo, id string) ([]string, error) {
		if id != "my-correct" {
			t.Errorf("UsedBy 被問到 %q", id)
		}
		return users, nil
	})
	if rec := r.owner("POST", "/api/prompts", `{"id":"my-correct","kind":"correct","body":"x"}`); rec.Code != 201 {
		t.Fatalf("建立 = %d %s", rec.Code, rec.Body)
	}
	rec := r.owner("DELETE", "/api/prompts/my-correct", "")
	e := decodeError(t, rec.Body.Bytes())
	if rec.Code != http.StatusConflict || e.Code != CodePromptInUse || !slices.Equal(e.UsedBy, users) {
		t.Fatalf("刪除被引用的 prompt = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("GET", "/api/prompts/my-correct/versions", ""); rec.Code != 200 {
		t.Errorf("被擋下的刪除不該動到 prompt：%d", rec.Code)
	}
	users = nil
	if rec := r.owner("DELETE", "/api/prompts/my-correct", ""); rec.Code != http.StatusNoContent {
		t.Errorf("沒有人引用之後刪除 = %d %s", rec.Code, rec.Body)
	}
}

// 沒有注入 prompt 庫是設定錯誤：回 500，不是代表「尚未實作」的 501（ADR-0015）。
func TestPromptsAPI_NotConfigured500(t *testing.T) {
	r := newItemsRig(t)
	rec := r.owner("GET", "/api/prompts", "")
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusInternalServerError || e.Code != CodeInternal {
		t.Errorf("= %d %s，預期 500 internal_error", rec.Code, rec.Body)
	}
}
