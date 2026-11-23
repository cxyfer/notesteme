package hubapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 列表、影片頁、取用與 lookup（M5-S4，HREP-R16～R19）的單元測試，重點在可見度的洩漏與不可信的參數。

type seedOpt struct {
	ext, kind, variant, lang, vis, content, license, correct string
	part, score                                              int
	unverified                                               bool
}

// seed 直接寫入 store 與 blob（不經過分享的驗證，external_id 可以是任意文字），回傳版本 ID。
func (e *repoEnv) seed(who string, o seedOpt) hubstore.Version {
	e.t.Helper()
	ctx := context.Background()
	if o.ext == "" {
		o.ext = "vid00000001"
	}
	if o.kind == "" {
		o.kind = "transcript"
	}
	if o.kind == "transcript" && o.variant == "" {
		o.variant = "source"
	}
	if o.lang == "" && o.kind != "frames" {
		o.lang = "en"
	}
	if o.vis == "" {
		o.vis = "authenticated"
	}
	if o.license == "" {
		o.license = "CC-BY-4.0"
	}
	if o.content == "" {
		o.content = fmt.Sprintf("%s-%s-%s-%s-%d", o.ext, o.kind, o.variant, o.lang, o.score)
	}
	var data []byte
	switch o.kind {
	case "transcript":
		var b bytes.Buffer
		if err := subtitle.RenderCuesJSON(&b, subtitle.Transcript{Lang: o.lang, Variant: subtitle.Variant(o.variant), Cues: []subtitle.Cue{
			{Index: 1, StartMS: 1000, EndMS: 3000, Text: o.content, Timing: subtitle.TimingSource}}}); err != nil {
			e.t.Fatal(err)
		}
		data = b.Bytes()
	case "note":
		data = []byte("# " + o.content + "\n\n<script>alert(1)</script>\n")
	default:
		data = []byte(`{"name":"` + o.content + `"}`)
	}
	sha, _, err := e.blobs.Put(ctx, bytes.NewReader(data), "")
	if err != nil {
		e.t.Fatal(err)
	}
	e.clk.Advance(time.Second)
	res, err := e.store.CreateShare(ctx, hubstore.NewShare{
		UserID: e.users[who], License: o.license, BundleSHA256: "seed-" + sha + o.vis + who, BundleSize: 1,
		Video: hubstore.Video{Platform: "youtube", ExternalID: o.ext, Part: o.part, Title: "Title of " + o.ext, ChannelExternalID: "UC" + o.ext},
		Blobs: []hubstore.Blob{{SHA256: sha, Size: int64(len(data)), MediaType: "application/json"}},
		Versions: []hubstore.NewVersion{{Version: hubstore.Version{Kind: o.kind, Variant: o.variant, Lang: o.lang, Format: "json",
			SHA256: sha, Size: int64(len(data)), Provenance: []byte(`{}`), CorrectModel: o.correct, ModelVerified: !o.unverified,
			Visibility: o.vis, License: o.license, Score: o.score}}},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Versions[0].Version
}

func body(rec *httptest.ResponseRecorder) string { return rec.Body.String() }

func listIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("列表 = %d %s", rec.Code, rec.Body)
	}
	var out []string
	for _, v := range decode(t, rec)["versions"].([]any) {
		out = append(out, v.(map[string]any)["id"].(string))
	}
	return out
}

func TestListingNeverLeaksVersionsTheViewerCannotSee(t *testing.T) {
	e := newRepoEnv(t, nil)
	pub := e.seed("alice", seedOpt{content: "everyone"})
	private := e.seed("alice", seedOpt{content: "secret-private-text", vis: "private", variant: "normalized"})
	group := e.seed("alice", seedOpt{content: "secret-group-text", vis: "group:translators", lang: "ja"})
	hidden := e.seed("alice", seedOpt{content: "hidden-text", variant: "corrected"})
	onlyPrivate := e.seed("alice", seedOpt{ext: "privonly0001", content: "other-private", vis: "private"})
	if err := e.store.SetVersionState(context.Background(), hidden.ID, "hidden"); err != nil {
		t.Fatal(err)
	}
	secrets := []string{private.ID, private.SHA256, group.ID, group.SHA256, hidden.ID, onlyPrivate.ID, onlyPrivate.SHA256, "privonly0001"}

	// bob（members）：列表、影片頁與 lookup 都不能有任何痕跡。
	for _, path := range []string{"/api/v1/versions", "/api/v1/versions?limit=200&sort=new", "/api/v1/versions?video=youtube:vid00000001",
		"/api/v1/videos/youtube/vid00000001"} {
		rec := e.as("bob", "GET", path, "")
		if rec.Code != 200 || !strings.Contains(body(rec), pub.ID) {
			t.Errorf("bob %s = %d %s", path, rec.Code, rec.Body)
		}
		for _, s := range secrets {
			if strings.Contains(body(rec), s) {
				t.Errorf("bob 的 %s 洩漏了 %s：%s", path, s, rec.Body)
			}
		}
	}
	// 只有私人版本的影片與不存在的影片：完全相同的 404。
	a, b := e.as("bob", "GET", "/api/v1/videos/youtube/privonly0001", ""), e.as("bob", "GET", "/api/v1/videos/youtube/nosuchvideo1", "")
	if a.Code != 404 || body(a) != body(b) || a.Code != b.Code {
		t.Errorf("只有私人版本的影片 = %d %s；不存在 = %d %s", a.Code, a.Body, b.Code, b.Body)
	}
	expect(t, "平台不對", e.as("bob", "GET", "/api/v1/videos/vimeo/vid00000001", ""), 404, "not_found")

	// 群組成員看得到群組的版本；上傳者看得到自己的私人版本，但 hidden 不列（R16）。
	e.grant("bob", "translators")
	if ids := strings.Join(listIDs(t, e.as("bob", "GET", "/api/v1/versions", "")), ","); !strings.Contains(ids, group.ID) || strings.Contains(ids, private.ID) {
		t.Errorf("群組成員的列表 = %s", ids)
	}
	ids := strings.Join(listIDs(t, e.as("alice", "GET", "/api/v1/versions?limit=200", "")), ",")
	if !strings.Contains(ids, private.ID) || !strings.Contains(ids, onlyPrivate.ID) || strings.Contains(ids, hidden.ID) {
		t.Errorf("上傳者的列表 = %s", ids)
	}
	// moderate 看得到全部（hidden 仍不列）。
	e.grant("bob", hubconfig.GroupModerators)
	ids = strings.Join(listIDs(t, e.as("bob", "GET", "/api/v1/versions?limit=200", "")), ",")
	if strings.Count(ids, ",") != 3 || strings.Contains(ids, hidden.ID) {
		t.Errorf("moderate 的列表 = %s", ids)
	}
	// PAT 只帶 read 時不算 moderate。
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	rec := e.bearer(tok, "GET", "/api/v1/versions?limit=200", "", "")
	if strings.Contains(body(rec), private.ID) || strings.Contains(body(rec), onlyPrivate.ID) {
		t.Errorf("只有 read 的 PAT 看到私人版本：%s", rec.Body)
	}
}

func TestListFiltersSortAndBadges(t *testing.T) {
	e := newRepoEnv(t, nil)
	low := e.seed("alice", seedOpt{ext: "aaaaaaaaaa1", score: 1, correct: "openai/gpt-4.1", variant: "corrected"})
	high := e.seed("alice", seedOpt{ext: "aaaaaaaaaa2", score: 5, correct: "x/agent", variant: "corrected", unverified: true})
	note := e.seed("alice", seedOpt{ext: "aaaaaaaaaa2", kind: "note", lang: "ja"})
	order := func(path string) string { return strings.Join(listIDs(t, e.as("bob", "GET", path, "")), ",") }
	if got := order("/api/v1/versions"); got != high.ID+","+low.ID+","+note.ID {
		t.Errorf("score 排序 = %s", got)
	}
	if got := order("/api/v1/versions?sort=new"); got != note.ID+","+high.ID+","+low.ID {
		t.Errorf("new 排序 = %s", got)
	}
	for path, want := range map[string]string{
		"/api/v1/versions?exclude_unverified=true":                          low.ID + "," + note.ID,
		"/api/v1/versions?correct_model=openai/gpt-4.1":                     low.ID,
		"/api/v1/versions?kind=note&lang=ja":                                note.ID,
		"/api/v1/versions?channel=UCaaaaaaaaaa1":                            low.ID,
		"/api/v1/versions?video=youtube:aaaaaaaaaa2":                        high.ID + "," + note.ID,
		"/api/v1/versions?video=youtube:aaaaaaaaaa2/p3":                     "",
		"/api/v1/versions?platform=bilibili":                                "",
		"/api/v1/versions?variant=corrected&sort=score":                     high.ID + "," + low.ID,
		"/api/v1/versions?correct_model=" + url.QueryEscape("x' OR '1'='1"): "",
		"/api/v1/versions?lang=en&exclude_unverified=false":                 high.ID + "," + low.ID,
	} {
		if got := order(path); got != want {
			t.Errorf("%s = %q，預期 %q", path, got, want)
		}
	}
	m := decode(t, e.as("bob", "GET", "/api/v1/versions?exclude_unverified=false&variant=corrected", ""))
	first := m["versions"].([]any)[0].(map[string]any)
	if first["model_verified"] != false || fmt.Sprint(first["badges"]) != "[unverified_model]" || first["score"] != float64(5) ||
		fmt.Sprint(first["uploaders"]) != "[map[display_name:alice]]" || first["video"].(map[string]any)["external_id"] != "aaaaaaaaaa2" {
		t.Errorf("列表的欄位 = %v", first)
	}
	if _, ok := m["next_cursor"]; ok {
		t.Errorf("只有一頁時不應有 next_cursor：%v", m)
	}
	// SQL 注入之後資料表仍在。
	if got := order("/api/v1/versions?limit=1"); got != high.ID {
		t.Errorf("注入之後的列表 = %s", got)
	}
}

func TestListParameterValidation(t *testing.T) {
	e := newRepoEnv(t, nil)
	long := strings.Repeat("x", 257)
	for q, path := range map[string]string{
		"limit=0": "query.limit", "limit=201": "query.limit", "limit=-1": "query.limit", "limit=abc": "query.limit",
		"limit=1e2": "query.limit", "limit=0050": "query.limit", "limit=" + strings.Repeat("9", 40): "query.limit",
		"platform=vimeo": "query.platform", "platform=YouTube": "query.platform",
		"kind=video": "query.kind", "variant=final": "query.variant", "sort=old": "query.sort",
		"exclude_unverified=yes": "query.exclude_unverified", "lang=e": "query.lang", "lang=en_US": "query.lang",
		"lang=" + strings.Repeat("a", 40): "query.lang", "video=youtube": "query.video", "video=vimeo:abc": "query.video",
		"video=youtube:": "query.video", "channel=" + long: "query.channel", "correct_model=" + long: "query.correct_model",
		"transcribe_model=a%00b": "query.transcribe_model", "translate_model=a%0Ab": "query.translate_model",
		"cursor=%25%25": "query.cursor", "cursor=" + strings.Repeat("A", 600): "query.cursor",
	} {
		rec := e.as("bob", "GET", "/api/v1/versions?"+q, "")
		if rec.Code != 422 {
			t.Errorf("%s = %d %s", q, rec.Code, rec.Body)
			continue
		}
		errs := decode(t, rec)["errors"].([]any)
		if p := errs[0].(map[string]any)["path"]; p != path {
			t.Errorf("%s 的 path = %v，預期 %s", q, p, path)
		}
	}
	// 多個錯誤依參數順序列出。
	rec := e.as("bob", "GET", "/api/v1/versions?limit=0&platform=x&sort=y", "")
	var paths []string
	for _, x := range decode(t, rec)["errors"].([]any) {
		paths = append(paths, x.(map[string]any)["path"].(string))
	}
	if strings.Join(paths, ",") != "query.platform,query.sort,query.limit" {
		t.Errorf("錯誤的順序 = %v", paths)
	}
	expect(t, "合法的上限", e.as("bob", "GET", "/api/v1/versions?limit=200", ""), 200, "")
	expect(t, "未登入", e.do("GET", "/api/v1/versions"), 401, "unauthorized")
}

func TestListCursorPagesAndTampering(t *testing.T) {
	e := newRepoEnv(t, nil)
	var all []string
	for i := range 5 {
		all = append(all, e.seed("alice", seedOpt{ext: fmt.Sprintf("page%07d", i), score: i % 2}).ID)
	}
	secret := e.seed("alice", seedOpt{ext: "pagesecret1", vis: "private", score: 100})
	for _, sort := range []string{"score", "new"} {
		full := listIDs(t, e.as("bob", "GET", "/api/v1/versions?sort="+sort, ""))
		var paged []string
		cursor := ""
		for range 10 {
			path := "/api/v1/versions?limit=2&sort=" + sort
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			rec := e.as("bob", "GET", path, "")
			paged = append(paged, listIDs(t, rec)...)
			c, ok := decode(t, rec)["next_cursor"].(string)
			if !ok {
				break
			}
			cursor = c
		}
		if strings.Join(paged, ",") != strings.Join(full, ",") || len(full) != 5 {
			t.Errorf("%s：分頁 %v，一次 %v", sort, paged, full)
		}
		// 其他排序的 cursor 不能用。
		other := map[string]string{"score": "new", "new": "score"}[sort]
		rec := e.as("bob", "GET", "/api/v1/versions?limit=2&sort="+sort, "")
		c := decode(t, rec)["next_cursor"].(string)
		expect(t, "換了排序的 cursor", e.as("bob", "GET", "/api/v1/versions?sort="+other+"&cursor="+url.QueryEscape(c), ""), 422, "invalid_request")
	}
	// 偽造的 cursor 只會換位置，不會帶出看不到的版本。
	for _, raw := range []string{`{"o":"score","s":1000,"t":9999999999999,"i":"zzzz"}`, `{"o":"score","s":-5,"t":0,"i":"0"}`} {
		c := base64.RawURLEncoding.EncodeToString([]byte(raw))
		rec := e.as("bob", "GET", "/api/v1/versions?cursor="+c, "")
		if rec.Code != 200 || strings.Contains(body(rec), secret.ID) {
			t.Errorf("偽造的 cursor %s = %d %s", raw, rec.Code, rec.Body)
		}
	}
	for _, raw := range []string{`{"o":"score","s":1,"t":1,"i":"a","x":1}`, `{"o":"score","s":1,"t":1}`, `{"o":"score","s":1,"t":-1,"i":"a"}`,
		`{"o":"new","s":3,"t":1,"i":"a"}`, `[]`, `{"o":"score","s":1,"t":1,"i":"a"} {}`, `{"o":"score","s":"1","t":1,"i":"a"}`} {
		c := base64.RawURLEncoding.EncodeToString([]byte(raw))
		sort := "score"
		if strings.Contains(raw, `"new"`) {
			sort = "new"
		}
		expect(t, "竄改的 cursor "+raw, e.as("bob", "GET", "/api/v1/versions?sort="+sort+"&cursor="+c, ""), 422, "invalid_request")
	}
}

func TestVideoPageGroupsAndParts(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.seed("alice", seedOpt{kind: "note"})
	tr := e.seed("alice", seedOpt{variant: "translated", lang: "zh-Hant"})
	lowSrc := e.seed("alice", seedOpt{content: "low"})
	highSrc := e.seed("alice", seedOpt{content: "high", score: 3})
	e.seed("alice", seedOpt{variant: "normalized", lang: "de"})
	e.seed("alice", seedOpt{variant: "normalized", lang: "ab"})
	e.seed("alice", seedOpt{part: 2, content: "part two"})
	rec := e.as("bob", "GET", "/api/v1/videos/youtube/vid00000001", "")
	if rec.Code != 200 {
		t.Fatalf("影片頁 = %d %s", rec.Code, rec.Body)
	}
	m := decode(t, rec)
	var got []string
	for _, g := range m["groups"].([]any) {
		g := g.(map[string]any)
		got = append(got, fmt.Sprintf("%s/%s/%s:%d", g["kind"], g["variant"], g["lang"], len(g["versions"].([]any))))
	}
	if strings.Join(got, ",") != "transcript/source/en:2,transcript/normalized/ab:1,transcript/normalized/de:1,transcript/translated/zh-Hant:1,note//en:1" {
		t.Errorf("分組 = %v", got)
	}
	src := m["groups"].([]any)[0].(map[string]any)["versions"].([]any)
	if src[0].(map[string]any)["id"] != highSrc.ID || src[1].(map[string]any)["id"] != lowSrc.ID {
		t.Errorf("組內排序 = %v", src)
	}
	if v := m["video"].(map[string]any); v["title"] != "Title of vid00000001" || v["part"] != nil {
		t.Errorf("影片 = %v", v)
	}
	if strings.Contains(body(rec), "part two") || !strings.Contains(body(rec), tr.ID) {
		t.Errorf("分P混進了影片頁：%s", rec.Body)
	}
	p2 := decode(t, e.as("bob", "GET", "/api/v1/videos/youtube/vid00000001?part=2", ""))
	if len(p2["groups"].([]any)) != 1 || p2["video"].(map[string]any)["part"] != float64(2) {
		t.Errorf("分P = %v", p2)
	}
	for _, bad := range []string{"0", "-1", "x", "10000", "1.5"} {
		rec := e.as("bob", "GET", "/api/v1/videos/youtube/vid00000001?part="+bad, "")
		if rec.Code != 422 || decode(t, rec)["errors"].([]any)[0].(map[string]any)["path"] != "query.part" {
			t.Errorf("part=%s = %d %s", bad, rec.Code, rec.Body)
		}
	}
	expect(t, "沒有這個分P", e.as("bob", "GET", "/api/v1/videos/youtube/vid00000001?part=3", ""), 404, "not_found")
	expect(t, "超長的 external_id", e.as("bob", "GET", "/api/v1/videos/youtube/"+strings.Repeat("a", 300), ""), 404, "not_found")
}

func TestDownloadRendersAndProtects(t *testing.T) {
	e := newRepoEnv(t, nil)
	v := e.seed("alice", seedOpt{content: "Hello from Hub.", license: "CC-BY-SA-4.0"})
	cases := map[string]string{
		"":          "1\n00:00:01,000 --> 00:00:03,000\nHello from Hub.\n",
		"srt":       "1\n00:00:01,000 --> 00:00:03,000\nHello from Hub.\n",
		"vtt":       "WEBVTT",
		"txt":       "Hello from Hub.",
		"cues-json": `"text":"Hello from Hub."`,
	}
	for f, want := range cases {
		path := "/api/v1/versions/" + v.ID + "/download"
		if f != "" {
			path += "?format=" + f
		}
		rec := e.as("bob", "GET", path, "")
		if rec.Code != 200 || !strings.Contains(body(rec), want) {
			t.Errorf("format=%s = %d %q", f, rec.Code, rec.Body)
		}
		h := rec.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Notesteme-License") != "CC-BY-SA-4.0" ||
			!strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="youtube-vid00000001-transcript-source-en.`) ||
			h.Get("Cache-Control") != "private, no-store" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("format=%s 的標頭 = %v", f, h)
		}
	}
	if ct := e.as("bob", "GET", "/api/v1/versions/"+v.ID+"/download?format=vtt", "").Header().Get("Content-Type"); ct != "text/vtt; charset=utf-8" {
		t.Errorf("vtt 的 Content-Type = %q", ct)
	}
	for _, bad := range []string{"md", "ass", "SRT", "../srt"} {
		rec := e.as("bob", "GET", "/api/v1/versions/"+v.ID+"/download?format="+url.QueryEscape(bad), "")
		if rec.Code != 422 || decode(t, rec)["errors"].([]any)[0].(map[string]any)["path"] != "query.format" {
			t.Errorf("format=%s = %d %s", bad, rec.Code, rec.Body)
		}
	}
	// 筆記以 markdown 附件下載，原始 HTML 不會被瀏覽器執行（nosniff、sandbox、attachment）。
	n := e.seed("alice", seedOpt{kind: "note"})
	rec := e.as("bob", "GET", "/api/v1/versions/"+n.ID+"/download", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" || !strings.HasSuffix(rec.Header().Get("Content-Disposition"), `.md"`) {
		t.Errorf("筆記 = %d %v", rec.Code, rec.Header())
	}
	ch := e.seed("alice", seedOpt{kind: "chapters", lang: "en"})
	expect(t, "章節", e.as("bob", "GET", "/api/v1/versions/"+ch.ID+"/download?format=json", ""), 200, "")
	fr := e.seed("alice", seedOpt{kind: "frames"})
	rec = e.as("alice", "GET", "/api/v1/versions/"+fr.ID+"/download", "")
	if rec.Code != 422 || decode(t, rec)["errors"].([]any)[0].(map[string]any)["path"] != "query.format" {
		t.Errorf("frames = %d %s", rec.Code, rec.Body)
	}

	// 看不到與不存在：完全相同的 404；removed：410。
	priv := e.seed("alice", seedOpt{vis: "private", content: "private words"})
	a, b := e.as("bob", "GET", "/api/v1/versions/"+priv.ID+"/download", ""), e.as("bob", "GET", "/api/v1/versions/nope/download", "")
	if a.Code != 404 || body(a) != body(b) || strings.Contains(body(a), "private words") {
		t.Errorf("私人版本 = %d %s；不存在 = %s", a.Code, a.Body, b.Body)
	}
	expect(t, "上傳者下載私人版本", e.as("alice", "GET", "/api/v1/versions/"+priv.ID+"/download", ""), 200, "")
	_ = e.store.SetVersionState(context.Background(), priv.ID, "removed")
	expect(t, "removed", e.as("alice", "GET", "/api/v1/versions/"+priv.ID+"/download", ""), 410, "removed")
	expect(t, "removed 的 content", e.as("alice", "GET", "/api/v1/versions/"+priv.ID+"/content", ""), 410, "removed")
	_ = e.store.SetVersionState(context.Background(), priv.ID, "withdrawn")
	expect(t, "withdrawn", e.as("alice", "GET", "/api/v1/versions/"+priv.ID+"/download", ""), 404, "not_found")

	// 權限：只有 read 的 PAT 不能下載。
	_, tok := e.createToken("bob", `{"name":"r","permissions":["read"]}`)
	expect(t, "沒有 download", e.bearer(tok, "GET", "/api/v1/versions/"+v.ID+"/download", "", ""), 403, "forbidden")

	// 內容 blob 被換掉（與雜湊不符）時是 500，不送出內容。
	p := filepath.Join(e.dir, "blobs", v.SHA256[:2], v.SHA256)
	if err := os.WriteFile(p, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = e.as("bob", "GET", "/api/v1/versions/"+v.ID+"/download", "")
	if rec.Code != 500 || strings.Contains(body(rec), "tampered") {
		t.Errorf("被換掉的 blob = %d %s", rec.Code, rec.Body)
	}
}

func TestContentReturnsTheRawCues(t *testing.T) {
	e := newRepoEnv(t, nil)
	v := e.seed("bob", seedOpt{content: "raw cues"})
	rec := e.as("bob", "GET", "/api/v1/versions/"+v.ID+"/content", "")
	expect(t, "沒有 regenerate", rec, 403, "forbidden")
	if decode(t, rec)["required_permission"] != "regenerate" {
		t.Errorf("required_permission = %s", rec.Body)
	}
	rec = e.as("alice", "GET", "/api/v1/versions/"+v.ID+"/content", "")
	sum := sha256.Sum256(rec.Body.Bytes())
	if rec.Code != 200 || rec.Header().Get("X-Notesteme-SHA256") != v.SHA256 || hex.EncodeToString(sum[:]) != v.SHA256 ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("content = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	n := e.seed("alice", seedOpt{kind: "note"})
	expect(t, "筆記沒有 cues", e.as("alice", "GET", "/api/v1/versions/"+n.ID+"/content", ""), 422, "invalid_request")
	priv := e.seed("bob", seedOpt{vis: "private", content: "bob only"})
	expect(t, "看不到的版本", e.as("alice", "GET", "/api/v1/versions/"+priv.ID+"/content", ""), 404, "not_found")
}

func TestDownloadNameIsSanitized(t *testing.T) {
	for _, c := range []struct {
		ext, kind, variant, lang string
		part                     int
		want                     string
	}{
		{"abc", "transcript", "source", "en", 0, "youtube-abc-transcript-source-en.srt"},
		{"BV1xx", "transcript", "translated", "zh-Hant", 2, "youtube-BV1xx-p2-transcript-translated-zh-Hant.srt"},
		{`../../etc/"passwd"` + "\r\nX-Evil: 1", "note", "", "", 0, "youtube-_.._etc__passwd___X-Evil__1-note.srt"},
		{"..hidden", "chapters", "", "日本", 0, "youtube-hidden-chapters-__.srt"},
		{strings.Repeat("a", 500), "note", "", "", 0, "youtube-" + strings.Repeat("a", 64) + "-note.srt"},
	} {
		got := downloadName(hubstore.Version{Kind: c.kind, Variant: c.variant, Lang: c.lang}, hubstore.Video{Platform: "youtube", ExternalID: c.ext, Part: c.part}, "srt")
		if got != c.want {
			t.Errorf("downloadName(%q) = %q，預期 %q", c.ext, got, c.want)
		}
		if strings.ContainsAny(got, "\"\r\n/\\") {
			t.Errorf("檔名有不安全的字元：%q", got)
		}
	}
}

// keyWithPrefix 找一個影片雜湊以 prefix 開頭的 external_id（4 碼約 65,536 次）。
func keyWithPrefix(t *testing.T, prefix string) string {
	t.Helper()
	for i := range 1 << 22 {
		ext := fmt.Sprintf("k%010d", i)
		if strings.HasPrefix(sqlite.VideoKeySHA256("youtube", ext, 0), prefix) {
			return ext
		}
	}
	t.Fatalf("找不到雜湊以 %s 開頭的影片", prefix)
	return ""
}

func TestLookupByPrefixWithoutLeaks(t *testing.T) {
	e := newRepoEnv(t, nil)
	e.grant("bob", hubconfig.GroupContributors)
	ffff := keyWithPrefix(t, "ffff")
	src := e.seed("alice", seedOpt{ext: ffff, content: "ffff source"})
	norm := e.seed("alice", seedOpt{ext: ffff, variant: "normalized", score: 2})
	corr := e.seed("alice", seedOpt{ext: ffff, variant: "corrected"})
	note := e.seed("alice", seedOpt{ext: ffff, kind: "note"})
	key := sqlite.VideoKeySHA256("youtube", ffff, 0)

	rec := e.as("bob", "GET", "/api/v1/lookup?platform=youtube&prefix=ffff", "")
	if rec.Code != 200 {
		t.Fatalf("lookup = %d %s", rec.Code, rec.Body)
	}
	m := decode(t, rec)
	videos := m["videos"].([]any)
	if len(videos) != 1 || videos[0].(map[string]any)["video_key_sha256"] != key {
		t.Fatalf("lookup = %s", rec.Body)
	}
	vs := videos[0].(map[string]any)["versions"].([]any)
	if len(vs) != 2 || vs[0].(map[string]any)["id"] != norm.ID || vs[1].(map[string]any)["id"] != src.ID {
		t.Errorf("lookup 的版本 = %v", vs)
	}
	for _, s := range []string{ffff, "Title of", corr.ID, note.ID, "external_id", "corrected"} {
		if strings.Contains(body(rec), s) {
			t.Errorf("lookup 洩漏了 %q：%s", s, rec.Body)
		}
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	empty := e.as("bob", "GET", "/api/v1/lookup?platform=youtube&prefix=fffe", "")
	if body(empty) != "{\"videos\":[]}\n" {
		t.Errorf("空的桶 = %q", empty.Body)
	}
	expectBody := func(name, path, want string) {
		t.Helper()
		if rec := e.as("bob", "GET", path, ""); rec.Code != 200 || body(rec) != want {
			t.Errorf("%s = %d %q，預期 %q", name, rec.Code, rec.Body, want)
		}
	}
	expectBody("其他平台", "/api/v1/lookup?platform=bilibili&prefix=ffff", body(empty))

	// 桶中只有別人的私人版本：與空桶完全相同。
	zero := keyWithPrefix(t, "0000")
	e.seed("alice", seedOpt{ext: zero, vis: "private"})
	e.seed("alice", seedOpt{ext: zero, vis: "group:translators", variant: "normalized"})
	expectBody("只有私人版本的桶", "/api/v1/lookup?platform=youtube&prefix=0000", body(empty))
	if rec := e.as("alice", "GET", "/api/v1/lookup?platform=youtube&prefix=0000", ""); len(decode(t, rec)["videos"].([]any)) != 1 {
		t.Errorf("上傳者自己的私人版本 = %s", rec.Body)
	}
	// hidden 不列（即使是上傳者）。
	_ = e.store.SetVersionState(context.Background(), src.ID, "hidden")
	_ = e.store.SetVersionState(context.Background(), norm.ID, "removed")
	expectBody("hidden 與 removed", "/api/v1/lookup?platform=youtube&prefix=ffff", body(empty))

	for q, path := range map[string]string{
		"platform=youtube": "query.prefix", "platform=youtube&prefix=": "query.prefix", "platform=youtube&prefix=fff": "query.prefix",
		"platform=youtube&prefix=fffff": "query.prefix", "platform=youtube&prefix=FFFF": "query.prefix", "platform=youtube&prefix=fffg": "query.prefix",
		"platform=youtube&prefix=" + url.QueryEscape("ff%_"): "query.prefix", "platform=youtube&prefix=" + url.QueryEscape("ff' OR 1=1--"): "query.prefix",
		"platform=youtube&prefix=" + url.QueryEscape("ffff\n"): "query.prefix", "prefix=ffff": "query.platform", "platform=vimeo&prefix=ffff": "query.platform",
	} {
		rec := e.as("bob", "GET", "/api/v1/lookup?"+q, "")
		if rec.Code != 422 || decode(t, rec)["errors"].([]any)[0].(map[string]any)["path"] != path {
			t.Errorf("%s = %d %s", q, rec.Code, rec.Body)
		}
	}
	// 權限：members 沒有 regenerate。
	e.revokeGroup("bob", hubconfig.GroupContributors)
	expect(t, "沒有 regenerate", e.as("bob", "GET", "/api/v1/lookup?platform=youtube&prefix=ffff", ""), 403, "forbidden")
}

func TestLookupIsRateLimited(t *testing.T) {
	e := newRepoEnv(t, func(c *hubconfig.Config) { c.RateLimits.RequestsPerMinute = 2 })
	_, tok := e.createToken("alice", `{"name":"c","permissions":["regenerate"]}`) // 第 1 個
	expect(t, "第 2 個", e.bearer(tok, "GET", "/api/v1/lookup?platform=youtube&prefix=0000", "", ""), 200, "")
	expect(t, "第 3 個", e.bearer(tok, "GET", "/api/v1/lookup?platform=youtube&prefix=0001", "", ""), 429, "rate_limited")
}
