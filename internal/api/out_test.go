package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 這個檔案測 OUT 切片（S7）的路由：ASS 樣式庫、預覽、字幕匯入與雙語輸出。

// outRoute 是一條 OUT 路由與它要求的 scope，用來驗證 401、403 與「先檢查 scope 再讀本體」。
type outRoute struct{ method, path, scope string }

var outRoutes = []outRoute{
	{"GET", "/api/ass-styles", auth.ScopeRead},
	{"POST", "/api/ass-styles", auth.ScopeAdmin},
	{"GET", "/api/ass-styles/default", auth.ScopeRead},
	{"PATCH", "/api/ass-styles/default", auth.ScopeAdmin},
	{"POST", "/api/ass-styles/preview", auth.ScopeRead},
	{"POST", "/api/ass-styles/import", auth.ScopeAdmin},
	{"POST", "/api/items/i1/subtitles/import", auth.ScopeIngest},
}

func TestOutRoutesScopes(t *testing.T) {
	var svc *auth.Service
	r := newItemsRigAPI(t, func(d *Deps) { svc = d.Auth })
	n := 0
	mk := func(scopes ...string) string {
		n++
		ct, err := svc.CreateToken(context.Background(), "without-"+strconv.Itoa(n), scopes, nil)
		if err != nil {
			t.Fatal(err)
		}
		return ct.Token
	}
	for _, c := range outRoutes {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if rec := r.do(c.method, c.path, "", nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("沒有身分 = %d，預期 401", rec.Code)
			}
			// 缺 scope 時，連壞掉的本體都不能先被讀取：403 而不是 400 或 422。
			var wrong []string
			for _, sc := range []string{auth.ScopeRead, auth.ScopeIngest, auth.ScopeAdmin} {
				if sc != c.scope {
					wrong = append(wrong, sc)
				}
			}
			tok := mk(wrong...)
			rec := r.do(c.method, c.path, `{"broken":`, withBearer(tok))
			e := decodeError(t, rec.Body.Bytes())
			if rec.Code != http.StatusForbidden || e.RequiredScope != c.scope {
				t.Errorf("缺 scope = %d %s，預期 403 required_scope=%s", rec.Code, rec.Body, c.scope)
			}
		})
	}
}

func TestAssStylesCRUD(t *testing.T) {
	r := newItemsRig(t)

	// 列出：六個內建樣式在前，且都是 builtin。
	rec := r.owner("GET", "/api/ass-styles?lang=ja", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("列出 = %d %s", rec.Code, rec.Body)
	}
	list := decode[struct{ Styles []StyleBody }](t, rec).Styles
	var names []string
	for _, s := range list {
		names = append(names, s.Name)
		if !s.Builtin {
			t.Errorf("%s 應該是內建樣式", s.Name)
		}
	}
	if got := strings.Join(names, ","); got != "default,translation-top,translation-only,large,boxed,minimal" {
		t.Errorf("內建樣式 = %s", got)
	}
	if list[0].Translation.Font != "Noto Sans CJK JP" {
		t.Errorf("lang=ja 的譯文字型 = %q，預期 Noto Sans CJK JP（OUT-R7）", list[0].Translation.Font)
	}

	// 內建樣式不能修改（OUT-R6）。
	rec = r.owner("PATCH", "/api/ass-styles/default", `{"primary":{"size":60}}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != CodeBuiltinStyle {
		t.Errorf("修改內建 = %d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("GET", "/api/ass-styles/default", ""); decode[StyleBody](t, rec).Primary.Size == 60 {
		t.Error("內建樣式不應被改到")
	}

	// 複製內建樣式並調整：201；之後可以修改。
	rec = r.owner("POST", "/api/ass-styles", `{"name":"mine","from":"large","translation":{"size":70}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("建立 = %d %s", rec.Code, rec.Body)
	}
	got := decode[StyleBody](t, rec)
	if got.Builtin || got.Name != "mine" || got.Translation.Size != 70 || got.Primary.Size != 46 {
		t.Errorf("建立的樣式 = %+v", got)
	}
	rec = r.owner("PATCH", "/api/ass-styles/mine", `{"layout":{"margin_v":90}}`)
	if rec.Code != http.StatusOK || decode[StyleBody](t, rec).Layout.MarginV != 90 || decode[StyleBody](t, rec).Translation.Size != 70 {
		t.Errorf("修改自訂樣式 = %d %s", rec.Code, rec.Body)
	}

	// 錯誤：重名 409 style_exists（包括和內建同名）、內容不正確 422、不存在 404。
	for _, body := range []string{`{"name":"mine"}`, `{"name":"default"}`} {
		rec = r.owner("POST", "/api/ass-styles", body)
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusConflict || e.Code != CodeStyleExists {
			t.Errorf("%s = %d %s", body, rec.Code, rec.Body)
		}
	}
	for _, body := range []string{`{"name":"Bad Name"}`, `{"name":"x1","translation":{"size":0}}`, `{"name":"x2","translation":{"color":"red"}}`, `{"name":"x3","from":"nope"}`} {
		if rec = r.owner("POST", "/api/ass-styles", body); rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s，預期 422 或 404", body, rec.Code, rec.Body)
		}
	}
	if rec = r.owner("GET", "/api/ass-styles/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("不存在的樣式 = %d", rec.Code)
	}
	if rec = r.owner("PATCH", "/api/ass-styles/nope", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("修改不存在的樣式 = %d", rec.Code)
	}
}

const sampleASS = `[Script Info]
Title: sample
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Noto Sans CJK TC,64,&H0000FFFF,&H000000FF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,3,1,2,10,10,40,1
Style: Orig,Noto Sans,40,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,1,2,10,10,40,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Default,,0,0,0,,hello
`

func TestAssStyleImport(t *testing.T) {
	r := newItemsRig(t)
	body, _ := json.Marshal(map[string]string{"name": "imported", "content": sampleASS})
	rec := r.owner("POST", "/api/ass-styles/import", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("匯入 = %d %s", rec.Code, rec.Body)
	}
	got := decode[StyleBody](t, rec)
	if got.Name != "imported" || got.Builtin || got.Translation.Size != 64 || got.Translation.Font != "Noto Sans CJK TC" || got.Primary.Size != 40 {
		t.Errorf("匯入的樣式 = %+v", got)
	}
	// 沒有 [V4+ Styles] 的檔案：422，樣式不會被建立（OUT-R8）。
	noStyles, _ := json.Marshal(map[string]string{"name": "empty", "content": "[Script Info]\nTitle: x\n\n[Events]\nFormat: Layer, Start, End, Style, Text\n"})
	if rec = r.owner("POST", "/api/ass-styles/import", string(noStyles)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("沒有樣式段落 = %d %s", rec.Code, rec.Body)
	}
	if rec = r.owner("GET", "/api/ass-styles/empty", ""); rec.Code != http.StatusNotFound {
		t.Errorf("422 之後不應建立樣式：%d", rec.Code)
	}
	// 重名 409；path 與 content 擇一；相對路徑、錯誤副檔名、不存在的檔案都是 422。
	if rec = r.owner("POST", "/api/ass-styles/import", string(body)); rec.Code != http.StatusConflict {
		t.Errorf("重名匯入 = %d %s", rec.Code, rec.Body)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "s.ass")
	if err := os.WriteFile(file, []byte(sampleASS), 0o600); err != nil {
		t.Fatal(err)
	}
	// 副檔名不對的檔案即使存在、內容正確也不收。
	if err := os.WriteFile(filepath.Join(dir, "s.txt"), []byte(sampleASS), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{
		`{"name":"a","path":"` + file + `","content":"x"}`,
		`{"name":"a"}`,
		`{"name":"a","path":"s.ass"}`,
		`{"name":"a","path":"` + filepath.Join(dir, "s.txt") + `"}`,
		`{"name":"a","path":"` + filepath.Join(dir, "missing.ass") + `"}`,
	} {
		if rec = r.owner("POST", "/api/ass-styles/import", b); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s，預期 422", b, rec.Code, rec.Body)
		}
	}
	if rec = r.owner("POST", "/api/ass-styles/import", `{"name":"from-file","path":"`+file+`"}`); rec.Code != http.StatusCreated {
		t.Errorf("用 path 匯入 = %d %s", rec.Code, rec.Body)
	}
}

const importedZH = "1\n00:00:00,000 --> 00:00:02,000\n一\n\n2\n00:00:02,000 --> 00:00:04,000\n二\n\n3\n00:00:04,000 --> 00:00:06,000\n三\n"

func TestSubtitleImport(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)
	url := "/api/items/" + id + "/subtitles/import"
	body := func(m map[string]string) string { b, _ := json.Marshal(m); return string(b) }

	// 匯入：201，新的 edited 版本（OUT-R13）。
	rec := r.owner("POST", url, body(map[string]string{"content": importedZH, "lang": "zh-Hant-TW", "name": "/home/someone/fixed.srt"}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("匯入 = %d %s", rec.Code, rec.Body)
	}
	got := decode[ImportedSubtitle](t, rec)
	if got.Variant != "edited" || got.Lang != "zh-Hant-TW" || got.Format != "srt" || got.Cues != 3 || got.Artifact == "" {
		t.Errorf("匯入結果 = %+v", got)
	}
	cues := decode[struct{ Variant, Lang string }](t, r.owner("GET", "/api/items/"+id+"/cues?lang=zh-Hant-TW", ""))
	if cues.Variant != "edited" {
		t.Errorf("匯入之後該語言的預設變體 = %q，預期 edited", cues.Variant)
	}
	// 來源紀錄：executor human，只留檔名。
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), id, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	var prov string
	for _, a := range arts {
		if a.ID == got.Artifact {
			prov = string(a.Provenance)
		}
	}
	if !strings.Contains(prov, `"executor":"human"`) || !strings.Contains(prov, `"name":"fixed.srt"`) || strings.Contains(prov, "someone") {
		t.Errorf("來源紀錄 = %s", prov)
	}

	// 時間不單調：422，line 是第幾句，file_line 是檔案的行號；不寫入任何東西（OUT-R15）。
	broken := "1\n00:00:05,000 --> 00:00:06,000\nfirst\n\n2\n00:00:02,000 --> 00:00:01,000\nsecond\n"
	before := len(arts)
	rec = r.owner("POST", url, body(map[string]string{"content": broken, "lang": "en"}))
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || rec.Code != http.StatusUnprocessableEntity || e.Line == nil || *e.Line != 2 || e.FileLine == nil || *e.FileLine != 6 {
		t.Errorf("壞時間 = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "second") {
		t.Errorf("錯誤訊息不應回顯檔案內容：%s", rec.Body)
	}
	if arts, _ = r.st.Read().CurrentArtifacts(context.Background(), id, "transcript"); len(arts) != before {
		t.Errorf("422 之後產出數 %d → %d", before, len(arts))
	}

	// 其他輸入錯誤：422；不存在的影音 404。
	dir := t.TempDir()
	file := filepath.Join(dir, "fixed.srt")
	if err := os.WriteFile(file, []byte(importedZH), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixed.exe"), []byte(importedZH), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{
		body(map[string]string{"content": importedZH, "path": file}),
		body(map[string]string{}),
		body(map[string]string{"content": importedZH, "lang": "not a lang"}),
		body(map[string]string{"path": "fixed.srt"}),
		body(map[string]string{"path": filepath.Join(dir, "fixed.exe")}),
		body(map[string]string{"content": "no cues here", "lang": "en"}),
	} {
		if rec = r.owner("POST", url, b); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s，預期 422", b, rec.Code, rec.Body)
		}
	}
	rec = r.owner("POST", url, body(map[string]string{"path": file, "lang": "zh-Hant-TW"}))
	if rec.Code != http.StatusCreated || decode[ImportedSubtitle](t, rec).Format != "srt" {
		t.Errorf("用 path 匯入 = %d %s", rec.Code, rec.Body)
	}
	if rec = r.owner("POST", "/api/items/nope/subtitles/import", body(map[string]string{"content": importedZH, "lang": "en"})); rec.Code != http.StatusNotFound {
		t.Errorf("不存在的影音 = %d %s", rec.Code, rec.Body)
	}
}

// 匯入的譯文語言（edited）與原文（en）不同，所以輸出成雙語；單語時標頭是 false。
func TestBilingualSubtitles(t *testing.T) {
	r := newItemsRig(t)
	id := r.ingestOne(abcURL)
	get := func(q string) *httptest.ResponseRecorder {
		return r.owner("GET", "/api/items/"+id+"/subtitles?"+q, "")
	}
	if rec := get("format=bilingual-srt"); rec.Header().Get("X-Notesteme-Bilingual") != "false" {
		t.Errorf("只有一種語言時應為單語：%s %s", rec.Header(), rec.Body)
	}
	rec := r.owner("POST", "/api/items/"+id+"/subtitles/import", `{"content":`+quote(importedZH)+`,"lang":"zh-Hant-TW"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("匯入 = %d %s", rec.Code, rec.Body)
	}

	rec = get("format=bilingual-srt&lang=zh-Hant-TW")
	if rec.Code != http.StatusOK || rec.Header().Get("X-Notesteme-Bilingual") != "true" || !strings.Contains(rec.Body.String(), "一\none") {
		t.Errorf("雙語 SRT = %d %s\n%s", rec.Code, rec.Header(), rec.Body)
	}
	rec = get("format=ass&lang=zh-Hant-TW&style=default")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/x-ssa") || rec.Header().Get("X-Notesteme-Bilingual") != "true" ||
		!strings.Contains(body, "[V4+ Styles]") || !strings.Contains(body, `一\N{\rPrimary}one`) {
		t.Errorf("雙語 ASS = %d %s\n%s", rec.Code, rec.Header(), rec.Body)
	}
	// 沒有指定語言與變體時，預設變體是處理得最完整的 edited（OUT-R2），原文是 en，所以也是雙語；
	// 指定 variant=normalized 是影音的原文語言本身，單語。
	if rec = get("format=ass"); rec.Header().Get("X-Notesteme-Bilingual") != "true" {
		t.Errorf("預設變體是匯入的 edited，應為雙語：%s", rec.Header())
	}
	if rec = get("format=ass&variant=normalized"); rec.Header().Get("X-Notesteme-Bilingual") != "false" {
		t.Errorf("variant=normalized 應為單語：%s", rec.Header())
	}
	// translation-only 樣式：只輸出一行，不含原文。
	rec = get("format=ass&lang=zh-Hant-TW&style=translation-only")
	if strings.Contains(rec.Body.String(), ",,one") || !strings.Contains(rec.Body.String(), ",,一") {
		t.Errorf("translation-only：\n%s", rec.Body)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// ---- 預覽 ----

// pngRunner 是 ffmpeg 與 ffprobe 的替身：預覽呼叫在工作目錄寫出 out.png。
type pngRunner struct {
	fail  bool
	ass   string
	calls int
}

var apiPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...)

func (p *pngRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if strings.HasSuffix(c.Path, "ffprobe") {
		return toolexec.Result{}, nil
	}
	p.calls++
	if p.fail {
		return toolexec.Result{ExitCode: 1, Stderr: []byte("No such filter: 'ass'\n")}, nil
	}
	b, _ := os.ReadFile(filepath.Join(c.Dir, "s.ass"))
	p.ass = string(b)
	return toolexec.Result{}, os.WriteFile(filepath.Join(c.Dir, "out.png"), apiPNG, 0o600)
}

func newPreviewAPIRig(t *testing.T, pr *pngRunner) *itemsRig {
	t.Helper()
	dir := t.TempDir()
	cfg := map[string]string{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg[name] = p
	}
	tool := media.New(pr, toolexec.Resolver{Config: cfg, Vars: map[string]string{}})
	return newItemsRig(t, func(d *pipeline.Deps) { d.Media = tool; d.Files = pathguard.Unrestricted() })
}

func TestAssStylePreview(t *testing.T) {
	pr := &pngRunner{}
	r := newPreviewAPIRig(t, pr)
	id := r.ingestOne(abcURL)

	rec := r.owner("POST", "/api/ass-styles/preview", `{"style":"large","item":"yt:abc123xxxxx","at":"00:00:03"}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Fatalf("預覽 = %d %s %q", rec.Code, rec.Header(), rec.Body.String())
	}
	if !strings.Contains(pr.ass, "[V4+ Styles]") || !strings.Contains(pr.ass, "Dialogue:") || !strings.Contains(pr.ass, "two") {
		t.Errorf("預覽用的 ASS：\n%s", pr.ass)
	}
	// 用影音 ID、暫時的 patch 也可以；預設樣式是 default。
	rec = r.owner("POST", "/api/ass-styles/preview", `{"item":"`+id+`","patch":{"translation":{"size":99}}}`)
	if rec.Code != http.StatusOK || !strings.Contains(pr.ass, ",99,") {
		t.Errorf("patch 預覽 = %d %s\n%s", rec.Code, rec.Body, pr.ass)
	}
	if got := decode[StyleBody](t, r.owner("GET", "/api/ass-styles/default", "")); got.Translation.Size == 99 {
		t.Error("預覽的 patch 不應寫入樣式")
	}

	// 輸入錯誤 422：沒有 item、不存在的樣式或影音、壞時間、patch 不合法。
	for _, b := range []string{
		`{}`,
		`{"item":"` + id + `","style":"nope"}`,
		`{"item":"yt:zzzzzzzzzzz"}`,
		`{"item":"` + id + `","at":"later"}`,
		`{"item":"` + id + `","patch":{"translation":{"size":-1}}}`,
	} {
		if rec = r.owner("POST", "/api/ass-styles/preview", b); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s，預期 422", b, rec.Code, rec.Body)
		}
	}

	// ffmpeg 失敗（例如沒有 libass）：502，不回傳 stderr。
	pr.fail = true
	rec = r.owner("POST", "/api/ass-styles/preview", `{"item":"`+id+`"}`)
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != http.StatusBadGateway || e.Code != CodeUpstream || strings.Contains(rec.Body.String(), "No such filter") {
		t.Errorf("ffmpeg 失敗 = %d %s", rec.Code, rec.Body)
	}
}
