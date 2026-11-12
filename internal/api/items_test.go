package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/worker"
)

// stubYT 回傳固定的影片資訊與一份 3 句的 VTT。
type stubYT struct{ info youtube.Info }

func (s stubYT) Probe(context.Context, string) (*youtube.Info, error) { i := s.info; return &i, nil }
func (stubYT) FetchCaptions(context.Context, string, youtube.Track, string) ([]byte, error) {
	return []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\none\n\n00:00:02.000 --> 00:00:04.000\ntwo\n\n00:00:04.000 --> 00:00:06.000\nthree\n"), nil
}
func (stubYT) Version(context.Context) (string, error) { return "2026.11.01", nil }

type itemsRig struct {
	h      http.Handler
	sess   string
	csrf   string
	worker *worker.Worker
	clk    *clock.Fake
	t      *testing.T
}

func newItemsRig(t *testing.T) *itemsRig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	q := queue.New(st, clk, "w")
	dur := 100
	p := pipeline.New(pipeline.Deps{
		Store: st, Clock: clk, DataDir: dir, Log: log, Wake: q.Wake, Version: "test",
		YouTube: stubYT{info: youtube.Info{ID: "abc123", Title: "Hello", Language: "en", DurationS: &dur,
			Tracks: []youtube.Track{{Lang: "en", Kind: "manual"}}}},
	})
	h := New(Deps{Store: st, Auth: svc, Clock: clk, Log: log, Version: "test", Pipeline: p, Queue: q})
	r := &itemsRig{h: h, worker: worker.New(q, p, clk, log), clk: clk, t: t}
	rec := r.do("POST", "/api/auth/login", `{"password":"pw"}`, nil)
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookie:
			r.sess = c.Value
		case auth.CSRFCookie:
			r.csrf = c.Value
		}
	}
	return r
}

func (r *itemsRig) do(method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func (r *itemsRig) owner(method, path, body string) *httptest.ResponseRecorder {
	return r.do(method, path, body, withCookies(r.sess, r.csrf, r.csrf))
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("回應不是預期的 JSON：%v\n%s", err, rec.Body)
	}
	return v
}

type itemsBody struct {
	Items []IngestedItem `json:"items"`
}

func TestCreateItemStatusAndBody(t *testing.T) {
	r := newItemsRig(t)
	rec := r.owner("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123"}`)
	if rec.Code != 201 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	body := decode[itemsBody](t, rec)
	if len(body.Items) != 1 || body.Items[0].Ref != "yt:abc123" || body.Items[0].AlreadyInLibrary || body.Items[0].ID == "" {
		t.Fatalf("body = %+v", body)
	}

	rec = r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123"}`)
	if rec.Code != 200 {
		t.Fatalf("已存在應回 200：%d %s", rec.Code, rec.Body)
	}
	if again := decode[itemsBody](t, rec); !again.Items[0].AlreadyInLibrary || again.Items[0].ID != body.Items[0].ID {
		t.Errorf("again = %+v", again)
	}
	list := decode[struct{ Items []Item }](t, r.owner("GET", "/api/items", ""))
	if len(list.Items) != 1 || list.Items[0].Ref != "yt:abc123" {
		t.Errorf("list = %+v", list)
	}
}

func TestCreateItemErrors(t *testing.T) {
	r := newItemsRig(t)
	tests := []struct {
		name, body string
		status     int
		code       string
		message    string
	}{
		{"頻道", `{"url":"https://www.youtube.com/@somechannel"}`, 422, CodeChannel, "這是頻道。要追蹤新內容，請建立訂閱。"},
		{"Bilibili 個人空間", `{"url":"https://space.bilibili.com/12345"}`, 422, CodeChannel, "這是頻道。要追蹤新內容，請建立訂閱。"},
		{"無法辨識", `{"url":"https://example.com/some/page"}`, 422, CodeUnrecognizedURL, "無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。"},
		{"Bilibili 影片", `{"url":"https://www.bilibili.com/video/BV1xx411c7mD"}`, 501, CodeNotImplemented, ""},
		{"播放清單", `{"url":"https://www.youtube.com/playlist?list=PL123"}`, 501, CodeNotImplemented, ""},
		{"未知的 until", `{"url":"https://youtu.be/abc123","until":"publish"}`, 422, CodeInvalidInput, ""},
		{"缺少 url", `{}`, 422, CodeInvalidInput, ""},
		{"多餘的欄位", `{"url":"https://youtu.be/abc123","nope":1}`, 422, CodeInvalidInput, ""},
	}
	for _, tt := range tests {
		rec := r.owner("POST", "/api/items", tt.body)
		e := decode[Error](t, rec)
		if rec.Code != tt.status || e.Code != tt.code || (tt.message != "" && e.Message != tt.message) {
			t.Errorf("%s：%d %s", tt.name, rec.Code, rec.Body)
		}
	}
	if list := decode[struct{ Items []Item }](t, r.owner("GET", "/api/items", "")); len(list.Items) != 0 {
		t.Errorf("被拒絕的請求不應建立影音：%+v", list)
	}
}

func TestItemDetailStagesAndSources(t *testing.T) {
	r := newItemsRig(t)
	created := decode[itemsBody](t, r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123","overrides":{"language":{"targets":["en"]}},"until":"transcript"}`))
	id := created.Items[0].ID

	d := decode[ItemDetail](t, r.owner("GET", "/api/items/"+id, ""))
	if len(d.Stages) != 3 || d.Stages[0].Stage != "metadata" || d.Stages[0].Status != "pending" || len(d.Sources) != 0 {
		t.Fatalf("新增後：%+v", d)
	}
	if d.Until != "transcript" || d.Overrides["language"] == nil || d.Stages[0].Actions == nil {
		t.Errorf("until 與 overrides 要回傳：%+v", d.Item)
	}

	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	d = decode[ItemDetail](t, r.owner("GET", "/api/items/"+id, ""))
	if d.Title != "Hello" || d.OriginalLang != "en" || d.LangSource != "metadata" || d.DurationS == nil || *d.DurationS != 100 {
		t.Errorf("metadata 之後：%+v", d.Item)
	}
	if d.Stages[0].Status != "done" || d.Stages[1].Status != "done" || d.Stages[2].Status != "pending" {
		t.Errorf("stages = %+v", d.Stages)
	}
	if d.Stages[1].ConfigSnapshotID == "" || d.Stages[1].ArtifactID == "" || d.Stages[1].FinishedAt == nil {
		t.Errorf("transcript = %+v", d.Stages[1])
	}
	if len(d.Sources) != 1 || d.Sources[0] != (Source{Source: "youtube-manual", Lang: "en", Kind: "manual", State: "used"}) {
		t.Errorf("sources = %+v", d.Sources)
	}

	if rec := r.owner("GET", "/api/items/nope", ""); rec.Code != 404 || decode[Error](t, rec).Code != CodeNotFound {
		t.Errorf("不存在的影音：%d %s", rec.Code, rec.Body)
	}
}

func TestCuesEndpoint(t *testing.T) {
	r := newItemsRig(t)
	id := decode[itemsBody](t, r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123"}`)).Items[0].ID
	if rec := r.owner("GET", "/api/items/"+id+"/cues", ""); rec.Code != 404 {
		t.Errorf("還沒有字幕：%d %s", rec.Code, rec.Body)
	}
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}

	type cues struct {
		Variant string
		Lang    string
		Cues    []Cue
	}
	got := decode[cues](t, r.owner("GET", "/api/items/"+id+"/cues", ""))
	if got.Variant != "normalized" || got.Lang != "en" || len(got.Cues) != 3 {
		t.Fatalf("預設變體：%+v", got)
	}
	if c := got.Cues[1]; c.Index != 2 || c.Start != "00:00:02.000" || c.End != "00:00:04.000" || c.Text != "two" {
		t.Errorf("cue = %+v", c)
	}
	src := decode[cues](t, r.owner("GET", "/api/items/"+id+"/cues?variant=source&lang=en", ""))
	if src.Variant != "source" || len(src.Cues) != 3 || src.Cues[0].Timing != "source" {
		t.Errorf("source = %+v", src)
	}
	// 時間碼與秒數都可以；範圍是 [from, to)（OUT-R3）。
	for _, q := range []string{"from=00:00:02&to=00:00:04", "from=2&to=4", "from=2.0&to=4.0"} {
		part := decode[cues](t, r.owner("GET", "/api/items/"+id+"/cues?variant=source&"+q, ""))
		if len(part.Cues) != 1 || part.Cues[0].Text != "two" || part.Cues[0].Start != "00:00:02.000" {
			t.Errorf("%s：%+v", q, part.Cues)
		}
	}
	for _, q := range []string{"from=abc", "to=-1", "variant=bogus"} {
		if rec := r.owner("GET", "/api/items/"+id+"/cues?"+q, ""); rec.Code != 422 {
			t.Errorf("%s：%d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := r.owner("GET", "/api/items/"+id+"/cues?lang=ja", ""); rec.Code != 404 {
		t.Errorf("語言不符：%d", rec.Code)
	}
}

func TestSettingsLanguageEndpoints(t *testing.T) {
	r := newItemsRig(t)
	def := decode[map[string]any](t, r.owner("GET", "/api/settings/language", ""))
	if def["allow_platform_translated"] != false || def["fallback"] == nil {
		t.Errorf("預設 = %v", def)
	}
	put := `{"targets":["zh-Hant-TW"],"caption_priority":[{"lang":"original","kind":"manual"}],"allow_platform_translated":true,"fallback":["supadata","groq"]}`
	rec := r.owner("PUT", "/api/settings/language", put)
	if rec.Code != 200 {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
	got := decode[map[string]any](t, r.owner("GET", "/api/settings/language", ""))
	if got["allow_platform_translated"] != true || len(got["caption_priority"].([]any)) != 1 {
		t.Errorf("GET = %v", got)
	}
	for name, body := range map[string]string{
		"空的目標語言": `{"targets":[]}`, "未知欄位": `{"nope":1}`, "kind 不合法": `{"caption_priority":[{"lang":"en","kind":"x"}]}`, "沒有內容": ``,
	} {
		if rec := r.owner("PUT", "/api/settings/language", body); rec.Code != 422 {
			t.Errorf("%s：%d %s", name, rec.Code, rec.Body)
		}
	}
	// 其他區段維持 501，不論有沒有內容。
	for _, body := range []string{"", `{"a":1}`} {
		rec := r.owner("PUT", "/api/settings/services", body)
		if rec.Code != 501 || decode[Error](t, rec).Code != CodeNotImplemented {
			t.Errorf("services：%d %s", rec.Code, rec.Body)
		}
	}
	if rec := r.owner("GET", "/api/settings/services", ""); rec.Code != 501 {
		t.Errorf("GET services = %d", rec.Code)
	}
}

func TestItemRoutesRequireAuth(t *testing.T) {
	r := newItemsRig(t)
	for _, c := range [][2]string{
		{"GET", "/api/items"}, {"POST", "/api/items"}, {"GET", "/api/items/x"}, {"GET", "/api/items/x/cues"},
		{"GET", "/api/settings/language"}, {"PUT", "/api/settings/language"},
	} {
		if rec := r.do(c[0], c[1], "", nil); rec.Code != 401 {
			t.Errorf("%s %s 未登入 = %d", c[0], c[1], rec.Code)
		}
	}
	// 缺 CSRF 的寫入被拒絕。
	if rec := r.do("POST", "/api/items", `{"url":"https://youtu.be/abc123"}`, withCookies(r.sess, r.csrf, "")); rec.Code != 403 {
		t.Errorf("缺 CSRF = %d", rec.Code)
	}
}

func TestSubtitlesDownload(t *testing.T) {
	r := newItemsRig(t)
	id := decode[itemsBody](t, r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123"}`)).Items[0].ID
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		query, typ, body string
	}{
		{"format=srt", "application/x-subrip", "1\n00:00:00,000 --> 00:00:02,000\none\n"},
		{"format=vtt", "text/vtt", "WEBVTT"},
		{"format=txt", "text/plain", "one\ntwo\nthree\n"},
		{"format=cues-json", "application/json", `"variant":"normalized"`},
		{"", "application/x-subrip", "one"},
		{"format=srt&from=2&to=4", "application/x-subrip", "two"},
	}
	for _, tt := range tests {
		rec := r.owner("GET", "/api/items/"+id+"/subtitles?"+tt.query, "")
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), tt.typ) || !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("%q：%d %s\n%s", tt.query, rec.Code, rec.Header(), rec.Body)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "Hello.en.") {
			t.Errorf("%q：Content-Disposition = %q", tt.query, cd)
		}
	}
	if rec := r.owner("GET", "/api/items/"+id+"/subtitles?format=srt&from=2&to=4", ""); strings.Contains(rec.Body.String(), "one") || !strings.Contains(rec.Body.String(), "00:00:02,000") {
		t.Errorf("時間範圍要保留原始時間：%s", rec.Body)
	}
	// 下載不會建立 srt 之類的產出（OUT-R1）。
	// 錯誤：M2 格式 501、未知格式 422、沒有字幕 404。
	for _, c := range []struct {
		query  string
		status int
	}{{"format=ass", 501}, {"format=bilingual-srt", 501}, {"format=docx", 422}, {"format=srt&from=x", 422}, {"format=srt&lang=ja", 404}} {
		if rec := r.owner("GET", "/api/items/"+id+"/subtitles?"+c.query, ""); rec.Code != c.status {
			t.Errorf("%s：%d %s", c.query, rec.Code, rec.Body)
		}
	}
	if rec := r.owner("GET", "/api/items/nope/subtitles?format=srt", ""); rec.Code != 404 {
		t.Errorf("不存在的影音 = %d", rec.Code)
	}
	if rec := r.do("GET", "/api/items/"+id+"/subtitles?format=srt", "", nil); rec.Code != 401 {
		t.Errorf("未登入 = %d", rec.Code)
	}
}

func TestRerunEndpoint(t *testing.T) {
	r := newItemsRig(t)
	id := decode[itemsBody](t, r.owner("POST", "/api/items", `{"url":"https://youtu.be/abc123"}`)).Items[0].ID
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec := r.owner("POST", "/api/items/"+id+"/rerun", `{"from":"normalize","overrides":{"language":{"targets":["en"]}}}`)
	if rec.Code != 202 {
		t.Fatalf("rerun = %d %s", rec.Code, rec.Body)
	}
	if d := decode[ItemDetail](t, rec); d.Stages[2].Stage != "normalize" || d.Stages[2].Status != "pending" || d.Stages[1].Status != "done" {
		t.Errorf("stages = %+v", d.Stages)
	}
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := decode[ItemDetail](t, r.owner("GET", "/api/items/"+id, "")); d.Stages[2].Status != "done" {
		t.Errorf("重跑後 stages = %+v", d.Stages)
	}
	for _, c := range []struct {
		path, body string
		status     int
	}{
		{"/api/items/" + id + "/rerun", `{"from":"correct"}`, 501},
		{"/api/items/" + id + "/rerun", `{"from":"nope"}`, 422},
		{"/api/items/" + id + "/rerun", `{}`, 422},
		{"/api/items/missing/rerun", `{"from":"normalize"}`, 404},
	} {
		if rec := r.owner("POST", c.path, c.body); rec.Code != c.status {
			t.Errorf("%s %s = %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
}

func TestResolveEndpoint(t *testing.T) {
	r := newItemsRig(t)
	rec := r.owner("POST", "/api/resolve", `{"url":"https://www.youtube.com/@somechannel"}`)
	got := decode[Resolution](t, rec)
	if rec.Code != 200 || got.Platform != "youtube" || got.Kind != "channel" || !got.Subscribable {
		t.Errorf("channel：%d %s", rec.Code, rec.Body)
	}
	rec = r.owner("POST", "/api/resolve", `{"url":"https://example.com/some/page"}`)
	if e := decode[Error](t, rec); rec.Code != 422 || e.Code != CodeUnrecognizedURL || e.Message != "無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。" {
		t.Errorf("unrecognised：%d %s", rec.Code, rec.Body)
	}
	if rec := r.owner("POST", "/api/resolve", `{}`); rec.Code != 422 {
		t.Errorf("缺 url = %d", rec.Code)
	}
	if rec := r.do("POST", "/api/resolve", `{"url":"https://youtu.be/abc123"}`, nil); rec.Code != 401 {
		t.Errorf("未登入 = %d", rec.Code)
	}
}
