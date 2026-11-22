package fakepodcast

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// audio 回傳帶 marker（可選 listed=）與 padding 的假媒體檔。
func audio(item string, duration float64, listed string) []byte {
	line := "NOTESTEME-FAKE-MEDIA item=" + item + " duration=" + strconv.FormatFloat(duration, 'f', -1, 64)
	if listed != "" {
		line += " listed=" + listed
	}
	return []byte(line + " streams=audio/mp3\n" + strings.Repeat("NOTESTEME-FAKE-MEDIA-PADDING\n", 10))
}

func newFeeds(t *testing.T) *Feeds {
	t.Helper()
	f := New()
	err := f.Put(Feed{Name: "show", Episodes: []Episode{
		{GUID: "ep-42", Title: "Episode <42> & more", AudioURL: MediaURL("show", "ep42.mp3"), Audio: audio("local:orig", 1800, "")},
		{GUID: "ep-43", Title: "Episode 43", AudioURL: MediaURL("show", "ep43.mp3"), Audio: audio("", 3720, "3600")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type rssDoc struct {
	Language string `xml:"channel>language"`
	Title    string `xml:"channel>title"`
	Items    []struct {
		GUID      string `xml:"guid"`
		Title     string `xml:"title"`
		Enclosure struct {
			URL    string `xml:"url,attr"`
			Type   string `xml:"type,attr"`
			Length int    `xml:"length,attr"`
		} `xml:"enclosure"`
		Duration   string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
		Transcript []struct {
			URL  string `xml:"url,attr"`
			Type string `xml:"type,attr"`
		} `xml:"https://podcastindex.org/namespace/1.0 transcript"`
	} `xml:"channel>item"`
}

func get(t *testing.T, h http.Handler, method, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestRender(t *testing.T) {
	f := newFeeds(t)
	if err := f.SetTranscript("pod:show/ep-42", &Transcript{URL: MediaURL("show", "ep42-transcript.vtt"), Type: "text/vtt", Body: []byte("WEBVTT\n")}); err != nil {
		t.Fatal(err)
	}
	resp := get(t, f.FeedHandler("show"), http.MethodGet, FeedURL("show"), nil)
	body, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/rss+xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	s := string(body)
	for _, want := range []string{
		`xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"`,
		`xmlns:podcast="https://podcastindex.org/namespace/1.0"`,
		"<language>en</language>",
		"<title>show</title>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("RSS 缺少 %q：\n%s", want, s)
		}
	}
	var doc rssDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("RSS 不是合法的 XML：%v\n%s", err, s)
	}
	if len(doc.Items) != 2 || doc.Language != "en" {
		t.Fatalf("doc = %+v", doc)
	}
	ep42, ep43 := doc.Items[0], doc.Items[1]
	if ep42.GUID != "ep-42" || ep42.Title != "Episode <42> & more" {
		t.Errorf("ep42 = %+v", ep42)
	}
	// length 是實際提供的位元組數（marker 的 item 改寫後的長度）。
	served := get(t, f.AudioHandler("pod:show/ep-42"), http.MethodGet, ep42.Enclosure.URL, nil)
	got, _ := io.ReadAll(served.Body)
	if ep42.Enclosure.Length != len(got) || ep42.Enclosure.Type != "audio/mpeg" || ep42.Enclosure.URL != MediaURL("show", "ep42.mp3") {
		t.Errorf("enclosure = %+v，實際音檔 %d bytes", ep42.Enclosure, len(got))
	}
	// itunes:duration：沒有 listed 用 marker 的 duration，有 listed 用它。
	if ep42.Duration != "1800" || ep43.Duration != "3600" {
		t.Errorf("duration = %q、%q", ep42.Duration, ep43.Duration)
	}
	// 只有腳本化字幕的集有 <podcast:transcript>。
	if len(ep42.Transcript) != 1 || ep42.Transcript[0].Type != "text/vtt" || ep42.Transcript[0].URL != MediaURL("show", "ep42-transcript.vtt") {
		t.Errorf("ep42 transcript = %+v", ep42.Transcript)
	}
	if len(ep43.Transcript) != 0 {
		t.Errorf("ep43 不該有字幕：%+v", ep43.Transcript)
	}
}

// feed 每次請求都依當時的狀態產生：之後才腳本化的字幕也會出現。
func TestFeedHandlerRendersCurrentState(t *testing.T) {
	f := newFeeds(t)
	body := func() string {
		b, _ := io.ReadAll(get(t, f.FeedHandler("show"), http.MethodGet, FeedURL("show"), nil).Body)
		return string(b)
	}
	if strings.Contains(body(), "podcast:transcript") {
		t.Fatal("還沒有腳本化字幕")
	}
	if err := f.SetTranscript("pod:show/ep-43", &Transcript{URL: MediaURL("show", "t.srt"), Type: "application/x-subrip"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body(), `<podcast:transcript url="https://media.example.com/show/t.srt" type="application/x-subrip"/>`) {
		t.Error("字幕標籤沒有出現")
	}
	if err := f.SetTranscript("pod:show/ep-43", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body(), "podcast:transcript") {
		t.Error("字幕應該已移除")
	}
	if resp := get(t, f.FeedHandler("nope"), http.MethodGet, FeedURL("nope"), nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的 feed：%d", resp.StatusCode)
	}
}

func TestAudioHandlerRewritesItem(t *testing.T) {
	f := newFeeds(t)
	h := f.AudioHandler("pod:show/ep-42")
	resp := get(t, h, http.MethodGet, MediaURL("show", "ep42.mp3"), nil)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/mpeg" || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("status=%d header=%v", resp.StatusCode, resp.Header)
	}
	m, ok := faketool.ParseMedia(b)
	if !ok || m.Item != "pod:show/ep-42" || m.Duration != 1800 {
		t.Fatalf("marker = %+v ok=%v\n%q", m, ok, b[:80])
	}
	// padding 原樣保留，listed= 不外洩到音檔。
	if !strings.HasSuffix(string(b), strings.Repeat("NOTESTEME-FAKE-MEDIA-PADDING\n", 10)) {
		t.Error("padding 被改動")
	}
	f43 := get(t, f.AudioHandler("pod:show/ep-43"), http.MethodGet, MediaURL("show", "ep43.mp3"), nil)
	b43, _ := io.ReadAll(f43.Body)
	if m, ok := faketool.ParseMedia(b43); !ok || m.Item != "pod:show/ep-43" || m.Duration != 3720 {
		t.Fatalf("ep43 marker = %+v", m)
	}
	if strings.Contains(string(b43[:strings.IndexByte(string(b43), '\n')]), "listed") {
		t.Error("提供的音檔不該帶 listed=")
	}
}

func TestAudioHandlerHeadAndRange(t *testing.T) {
	f := newFeeds(t)
	h := f.AudioHandler("pod:show/ep-42")
	full, _ := io.ReadAll(get(t, h, http.MethodGet, MediaURL("show", "ep42.mp3"), nil).Body)

	head := get(t, h, http.MethodHead, MediaURL("show", "ep42.mp3"), nil)
	if head.StatusCode != 200 || head.Header.Get("Content-Length") != strconv.Itoa(len(full)) {
		t.Errorf("HEAD status=%d length=%q，預期 %d", head.StatusCode, head.Header.Get("Content-Length"), len(full))
	}
	rng := get(t, h, http.MethodGet, MediaURL("show", "ep42.mp3"), map[string]string{"Range": "bytes=5-14"})
	part, _ := io.ReadAll(rng.Body)
	if rng.StatusCode != http.StatusPartialContent || string(part) != string(full[5:15]) {
		t.Errorf("Range status=%d body=%q", rng.StatusCode, part)
	}
	if cr := rng.Header.Get("Content-Range"); cr != "bytes 5-14/"+strconv.Itoa(len(full)) {
		t.Errorf("Content-Range = %q", cr)
	}
	if resp := get(t, f.AudioHandler("pod:show/none"), http.MethodGet, "/", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的集：%d", resp.StatusCode)
	}
}

func TestTranscriptHandlerCountsCalls(t *testing.T) {
	f := newFeeds(t)
	ref := "pod:show/ep-42"
	h := f.TranscriptHandler(ref)
	if resp := get(t, h, http.MethodGet, "/", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("沒有字幕：%d", resp.StatusCode)
	}
	if err := f.SetTranscript(ref, &Transcript{URL: MediaURL("show", "a.vtt"), Type: "text/vtt", Body: []byte("WEBVTT\n\n")}); err != nil {
		t.Fatal(err)
	}
	resp := get(t, h, http.MethodGet, "/", nil)
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/vtt" || string(b) != "WEBVTT\n\n" {
		t.Errorf("status=%d type=%q body=%q", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	if n := f.TranscriptCalls(); n != 2 {
		t.Errorf("TranscriptCalls = %d，預期 2（含 404 的那次）", n)
	}
}

func TestPutValidation(t *testing.T) {
	f := New()
	ok := audio("", 60, "")
	cases := map[string]Feed{
		"沒有名稱":        {Episodes: []Episode{{GUID: "a", Audio: ok}}},
		"guid 為空":     {Name: "s", Episodes: []Episode{{Audio: ok}}},
		"guid 重複":     {Name: "s", Episodes: []Episode{{GUID: "a", Audio: ok}, {GUID: "a", Audio: ok}}},
		"音檔不是 marker": {Name: "s", Episodes: []Episode{{GUID: "a", Audio: []byte("RIFF....")}}},
	}
	for name, feed := range cases {
		if err := f.Put(feed); err == nil {
			t.Errorf("%s：應該失敗", name)
		}
	}
}

func TestSetTranscriptValidation(t *testing.T) {
	f := newFeeds(t)
	if err := f.SetTranscript("pod:show/nope", &Transcript{URL: "https://media.example.com/x.vtt"}); err == nil {
		t.Error("集不存在應該失敗")
	}
	tr := &Transcript{URL: MediaURL("show", "same.vtt"), Type: "text/vtt"}
	if err := f.SetTranscript("pod:show/ep-42", tr); err != nil {
		t.Fatal(err)
	}
	if err := f.SetTranscript("pod:show/ep-43", tr); err == nil {
		t.Error("同一個字幕網址給兩集應該失敗")
	}
	// 同一集重設不算衝突。
	if err := f.SetTranscript("pod:show/ep-42", tr); err != nil {
		t.Errorf("同一集重設：%v", err)
	}
}

func TestListedFromMarkerAndContentType(t *testing.T) {
	if got := ListedFromMarker(audio("", 3720, "3600")); got != 3600 {
		t.Errorf("listed = %v", got)
	}
	if got := ListedFromMarker(audio("", 60, "")); got != 0 {
		t.Errorf("沒有 listed = %v", got)
	}
	if got := ListedFromMarker([]byte("NOTESTEME-FAKE-MEDIA duration=1 listed=abc\n")); got != 0 {
		t.Errorf("不合法的 listed = %v", got)
	}
	for name, want := range map[string]string{
		"a.mp3": "audio/mpeg", "a.M4A": "audio/mp4", "a.mp4": "video/mp4", "a.vtt": "text/vtt",
		"a.srt": "application/x-subrip", "a.json?x=1": "application/json", "a.bin": "application/octet-stream",
	} {
		if got := ContentType(name); got != want {
			t.Errorf("ContentType(%q) = %q，預期 %q", name, got, want)
		}
	}
}

func TestSplitRef(t *testing.T) {
	for ref, want := range map[string][2]string{
		"pod:show/ep-42":     {"show", "ep-42"},
		"pod:show/a/b":       {"show", "a/b"},
		"pod:feed-1/guid:42": {"feed-1", "guid:42"},
	} {
		f, g, ok := SplitRef(ref)
		if !ok || f != want[0] || g != want[1] {
			t.Errorf("SplitRef(%q) = %q, %q, %v", ref, f, g, ok)
		}
	}
	for _, bad := range []string{"yt:abc", "pod:show", "pod:/x", "pod:show/"} {
		if _, _, ok := SplitRef(bad); ok {
			t.Errorf("SplitRef(%q) 應該失敗", bad)
		}
	}
}
