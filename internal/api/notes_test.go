package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

const noteVer = "notever0123456789"

// seedNote 直接寫入一個 note 產出（ADR-0020 第 4 點的 JSON），不經過 pipeline。
func (r *localRig) seedNote(id string) {
	r.t.Helper()
	doc := map[string]any{
		"lang": "en", "markdown": "---\ntitle: \"Talk\"\n---\n# Talk\n\n## Intro · 00:01\nHello.\n\n## 自我檢驗\n- q\n",
		"frontmatter": map[string]string{"title": "Talk"},
		"checks":      []map[string]any{{"id": "frontmatter", "ok": true}, {"id": "self_check", "ok": false, "message": "沒有自我檢驗"}},
		"engine":      "pipeline", "template": "video-notes@1",
		"sections":   []map[string]any{{"level": 2, "title": "Intro", "cue": 1, "body": "Hello.", "frames": []int{}}},
		"self_check": []string{"q"}, "images": []any{},
		"render": map[string]any{"status": "complete", "cue_start": map[string]int64{"1": 1000}, "frames": map[string]any{}, "extra": []any{}},
	}
	body, err := json.Marshal(doc)
	if err != nil {
		r.t.Fatal(err)
	}
	rel := filepath.ToSlash(filepath.Join("artifacts", id, noteVer+".json"))
	if err := os.MkdirAll(filepath.Join(r.dir, "artifacts", id), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, filepath.FromSlash(rel)), body, 0o600); err != nil {
		r.t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	_, err = r.st.Write().InsertArtifact(context.Background(), store.NewArtifact{
		ID: noteVer, ItemID: id, Kind: "note", Variant: "note", Lang: "en", Format: "notesteme-note+json",
		Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Provenance: json.RawMessage(`{"stage":"note"}`),
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

// NOTE-R13：GET note 的 JSON 與 Markdown、沒有筆記時 404 no_note。
func TestGetNote(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	get := func(q string) (int, string, string) {
		rec := r.do("GET", "/api/items/"+id+"/note"+q, "", r.owner(nil))
		return rec.Code, rec.Header().Get("Content-Type"), rec.Body.String()
	}
	if code, _, body := get(""); code != 404 || !strings.Contains(body, `"code":"no_note"`) {
		t.Fatalf("沒有筆記：%d %s", code, body)
	}
	r.seedNote(id)
	code, ct, body := get("")
	if code != 200 || !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("json：%d %s %s", code, ct, body)
	}
	var v NoteBody
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	if v.Lang != "en" || v.Frontmatter["title"] != "Talk" || len(v.Checks) != 2 || v.Checks[1].OK || v.Checks[1].Message != "沒有自我檢驗" ||
		v.Stale || v.Outputs == nil || !strings.Contains(v.Markdown, "## Intro") {
		t.Errorf("note = %+v", v)
	}
	if code, ct, md := get("?format=md"); code != 200 || !strings.HasPrefix(ct, "text/markdown") || !strings.HasPrefix(md, "---\ntitle:") {
		t.Errorf("md：%d %s %q", code, ct, md)
	}
	if code, _, body := get("?format=html"); code != 422 {
		t.Errorf("format 不正確：%d %s", code, body)
	}
	if code, _, body := get("?lang=ja"); code != 404 || !strings.Contains(body, "no_note") {
		t.Errorf("沒有這個語言：%d %s", code, body)
	}
	if rec := r.do("GET", "/api/items/nope/note", "", r.owner(nil)); rec.Code != 404 {
		t.Errorf("沒有影音：%d", rec.Code)
	}
}

// NOTE-R11、R12：模板的列表、建立、修改、預覽與錯誤代碼。
func TestNoteTemplateRoutes(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		rec := r.do(method, path, body, r.owner(nil))
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	code, m := call("GET", "/api/note-templates", "")
	list, _ := m["templates"].([]any)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["id"] != "video-notes" || list[0].(map[string]any)["builtin"] != true {
		t.Fatalf("list：%d %v", code, m)
	}
	if checks := list[0].(map[string]any)["checks"].([]any); len(checks) != 5 {
		t.Errorf("內建模板執行全部檢查：%v", checks)
	}
	if code, m := call("POST", "/api/note-templates", `{"id":"mine","from":"video-notes"}`); code != 201 || m["version"] != float64(1) || m["builtin"] != false {
		t.Fatalf("create：%d %v", code, m)
	}
	cases := []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/api/note-templates", `{"id":"mine","from":"video-notes"}`, 409, CodeTemplateExists},
		{"POST", "/api/note-templates", `{"id":"video-notes","body":"x"}`, 409, CodeBuiltinTemplate},
		{"PATCH", "/api/note-templates/video-notes", `{"body":"x"}`, 409, CodeBuiltinTemplate},
		{"PATCH", "/api/note-templates/nope", `{"body":"x"}`, 404, CodeNotFound},
		{"POST", "/api/note-templates/nope/preview", `{"item":"` + id + `"}`, 404, CodeNotFound},
		{"POST", "/api/note-templates/video-notes/preview", `{"item":"` + id + `"}`, 409, CodeNoNote},
		{"POST", "/api/note-templates/video-notes/preview", `{"item":"missing"}`, 404, CodeNotFound},
	}
	for _, c := range cases {
		if code, m := call(c.method, c.path, c.body); code != c.status || m["code"] != c.code {
			t.Errorf("%s %s %s：%d %v，預期 %d %s", c.method, c.path, c.body, code, m, c.status, c.code)
		}
	}
	code, m = call("POST", "/api/note-templates", `{"id":"leaky","body":"{{.Secret}}"}`)
	errs, _ := m["errors"].([]any)
	if code != 422 || len(errs) != 1 || errs[0].(map[string]any)["path"] != "body" || errs[0].(map[string]any)["message"] != "不認識的變數 .Secret。" {
		t.Errorf("未知的變數：%d %v", code, m)
	}
	if code, m := call("PATCH", "/api/note-templates/mine", `{"body":"{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n","checks":["frontmatter"]}`); code != 200 ||
		m["version"] != float64(2) || len(m["checks"].([]any)) != 1 {
		t.Errorf("patch：%d %v", code, m)
	}
	r.seedNote(id)
	code, m = call("POST", "/api/note-templates/mine/preview", `{"item":"`+id+`"}`)
	if code != 200 || !strings.Contains(m["markdown"].(string), "Intro") || strings.Contains(m["markdown"].(string), "自我檢驗") {
		t.Fatalf("preview：%d %v", code, m)
	}
	if checks := m["checks"].([]any); len(checks) != 1 || checks[0].(map[string]any)["id"] != "frontmatter" {
		t.Errorf("preview 只跑模板勾選的檢查：%v", checks)
	}
}
