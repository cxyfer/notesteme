package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// LIB-R25：列出每個產出的每個版本，讀取舊版本的內容；不支援的種類與 format 回 422、
// 不存在 404、垃圾桶中 404 in_trash、損毀 409 artifact_corrupt。
func TestArtifactVersions(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	r.seedNote(id)
	imp := func(text string) {
		t.Helper()
		content := strings.ReplaceAll(importedZH, "一", text)
		b, _ := json.Marshal(map[string]string{"content": content, "lang": "zh-Hant-TW"})
		if rec := r.do("POST", "/api/items/"+id+"/subtitles/import", string(b), r.owner(nil)); rec.Code != http.StatusCreated {
			t.Fatalf("匯入 = %d %s", rec.Code, rec.Body)
		}
	}
	imp("第一版")
	imp("第二版")
	get := func(path string) (int, string, string) {
		rec := r.do("GET", path, "", r.owner(nil))
		return rec.Code, rec.Header().Get("Content-Type"), rec.Body.String()
	}

	code, _, body := get("/api/items/" + id + "/artifacts?kind=transcript&variant=edited")
	if code != 200 {
		t.Fatalf("列表 = %d %s", code, body)
	}
	var list struct{ Artifacts []ArtifactVersionBody }
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Artifacts) != 2 || !list.Artifacts[0].Current || list.Artifacts[1].Current ||
		list.Artifacts[0].Executor != "human" || list.Artifacts[0].Lang != "zh-Hant-TW" {
		t.Fatalf("edited 的版本 = %+v", list.Artifacts)
	}
	if strings.Contains(body, "provenance") || strings.Contains(body, `"path"`) {
		t.Errorf("列表不該帶完整的來源紀錄或檔案路徑：%s", body)
	}
	old := list.Artifacts[1].ID

	// 舊版本的內容（json 同 /cues 的形狀、txt 純文字）
	code, ct, body := get("/api/artifacts/" + old + "/content")
	if code != 200 || !strings.HasPrefix(ct, "application/json") || !strings.Contains(body, `"text":"第一版"`) || !strings.Contains(body, `"variant":"edited"`) {
		t.Errorf("舊版本 json = %d %s %s", code, ct, body)
	}
	if code, ct, body := get("/api/artifacts/" + old + "/content?format=txt"); code != 200 || !strings.HasPrefix(ct, "text/plain") || !strings.HasPrefix(body, "第一版\n") {
		t.Errorf("舊版本 txt = %d %s %q", code, ct, body)
	}
	if code, _, _ := get("/api/artifacts/" + old + "/content?format=md"); code != 422 {
		t.Errorf("字幕要 md = %d，預期 422", code)
	}

	// 筆記：md（預設）；json 回 422
	if code, ct, md := get("/api/artifacts/" + noteVer + "/content"); code != 200 || !strings.HasPrefix(ct, "text/markdown") || !strings.Contains(md, "## Intro") {
		t.Errorf("筆記 = %d %s %q", code, ct, md)
	}
	if code, _, _ := get("/api/artifacts/" + noteVer + "/content?format=json"); code != 422 {
		t.Errorf("筆記要 json = %d，預期 422", code)
	}

	// 其他種類只列版本、不能讀內容
	if _, err := r.st.Write().InsertArtifact(context.Background(), store.NewArtifact{
		ID: "chap0000000000001", ItemID: id, Kind: "chapters", Format: "json", Path: "artifacts/x.json",
		SHA256: "00", Size: 2, Provenance: json.RawMessage(`{"stage":"chapters","provider":` + `{"x":1}` + `}`),
	}); err != nil {
		t.Fatal(err)
	}
	if code, _, body := get("/api/artifacts/chap0000000000001/content"); code != 422 {
		t.Errorf("chapters = %d %s，預期 422", code, body)
	}
	_, _, body = get("/api/items/" + id + "/artifacts?kind=chapters")
	if !strings.Contains(body, `"stage":"chapters"`) || strings.Contains(body, `"provider"`) {
		t.Errorf("來源紀錄中型別不對的欄位要丟掉：%s", body)
	}
	if code, _, body := get("/api/artifacts/nope/content"); code != 404 || !strings.Contains(body, "找不到這個產出") {
		t.Errorf("不存在 = %d %s", code, body)
	}

	// 舊版本的檔案損毀：409，只標記（不會重跑）
	var oldPath string
	arts, _ := r.st.Read().ArtifactGroupVersions(context.Background(), id, "transcript", "edited", "zh-Hant-TW")
	for _, a := range arts {
		if a.ID == old {
			oldPath = a.Path
		}
	}
	if err := os.WriteFile(filepath.Join(r.dir, filepath.FromSlash(oldPath)), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, body := get("/api/artifacts/" + old + "/content"); code != 409 || !strings.Contains(body, "artifact_corrupt") {
		t.Errorf("損毀 = %d %s", code, body)
	}

	// 垃圾桶中：列表與內容都是 404 in_trash
	if rec := r.do("DELETE", "/api/items/"+id, "", r.owner(nil)); rec.Code/100 != 2 {
		t.Fatalf("刪除 = %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/api/items/" + id + "/artifacts", "/api/artifacts/" + noteVer + "/content"} {
		if code, _, body := get(p); code != 404 || !strings.Contains(body, "in_trash") {
			t.Errorf("%s 在垃圾桶中 = %d %s", p, code, body)
		}
	}
}
