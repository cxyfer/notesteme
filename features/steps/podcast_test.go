package steps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/source"
)

// httpGet 經 fakenet 與 netguard 取得網址的內容（和被測程式的路徑相同）。
func httpGet(t *testing.T, s *Scenario, rawURL string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	c := &http.Client{Transport: netguard.Transport(s.World.Net.Deps()), Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s：%v", rawURL, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestPodcastFeedRegistersFeedAndAudio(t *testing.T) {
	s := newTestScenario(t)
	err := s.podcastFeed("show", table(
		[]string{"guid", "title", "audio"},
		[]string{"ep-41", "Episode 41", "ep41.mp3"},
		[]string{"ep-43", "Episode 43", "ep43-with-extra-ads.mp3"},
	))
	if err != nil {
		t.Fatal(err)
	}
	resp, body := httpGet(t, s, "https://feeds.example.com/show.xml", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("feed status = %d", resp.StatusCode)
	}
	for _, want := range []string{
		"<guid isPermaLink=\"false\">ep-41</guid>",
		`url="https://media.example.com/show/ep41.mp3"`,
		"<itunes:duration>1500</itunes:duration>",
		"<itunes:duration>3600</itunes:duration>", // ep43 的 listed
		"<language>en</language>",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("feed 缺少 %q：\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "podcast:transcript") {
		t.Error("還沒有腳本化字幕，不該有 <podcast:transcript>")
	}
	// 音檔：marker 的 item 是這一集的代號；ffprobe 看到的長度是 marker 的 duration（3720，不是 listed）。
	_, audio := httpGet(t, s, "https://media.example.com/show/ep43-with-extra-ads.mp3", nil)
	m, ok := faketool.ParseMedia(audio)
	if !ok || m.Item != "pod:show/ep-43" || m.Duration != 3720 {
		t.Fatalf("音檔 marker = %+v ok=%v", m, ok)
	}
	// Range。
	r, part := httpGet(t, s, "https://media.example.com/show/ep41.mp3", map[string]string{"Range": "bytes=0-9"})
	if r.StatusCode != http.StatusPartialContent || len(part) != 10 {
		t.Errorf("Range status=%d len=%d", r.StatusCode, len(part))
	}
	if v := s.World.Net.Violations(); len(v) != 0 {
		t.Errorf("違規：%v", v)
	}
}

// audio 是完整網址：登記到那個網址（fixture 取 path 的檔名），而且不覆蓋該主機先設定的解析序列。
func TestPodcastFeedFullAudioURLKeepsRebindingSequence(t *testing.T) {
	s := newTestScenario(t)
	if err := s.hostResolvesToThen("rebind.example.com", "93.184.216.34", "192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	err := s.podcastFeed("show", table(
		[]string{"guid", "title", "audio"},
		[]string{"ep-42", "Episode 42", "https://rebind.example.com/ep42.mp3"},
	))
	if err != nil {
		t.Fatal(err)
	}
	ep, ok := s.World.Podcasts.Episode("pod:show/ep-42")
	if !ok || ep.AudioURL != "https://rebind.example.com/ep42.mp3" {
		t.Fatalf("episode = %+v ok=%v", ep, ok)
	}
	// 第一次解析是公開 IP：連得上；第二次解析是 192.168.1.1（netguard 之外的 fakenet 序列）。
	resp, body := httpGet(t, s, "https://rebind.example.com/ep42.mp3", nil)
	if resp.StatusCode != 200 || len(body) == 0 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	lookups := s.World.Net.Lookups()
	if len(lookups) == 0 || lookups[0].Host != "rebind.example.com" || lookups[0].IPs[0].String() != "93.184.216.34" {
		t.Fatalf("第一次解析 = %+v", lookups)
	}
	if err := s.noConnectionTo("192.168.1.1"); err != nil {
		t.Error(err)
	}
	// 之後（feed 步驟之後再解析）序列仍然往下走，不是被重設。
	if _, err := s.World.Net.LookupNetIP(context.Background(), "ip", "rebind.example.com"); err != nil {
		t.Fatal(err)
	}
	ls := s.World.Net.Lookups()
	if got := ls[len(ls)-1].IPs[0].String(); got != "192.168.1.1" {
		t.Errorf("第二次解析 = %s，預期 192.168.1.1", got)
	}
}

func TestPodcastFeedValidation(t *testing.T) {
	s := newTestScenario(t)
	ok := table([]string{"guid", "title", "audio"}, []string{"ep-1", "One", "ep41.mp3"})
	for name, want := range map[string]string{
		"Show":                            "feed 名稱",
		"my_show":                         "feed 名稱",
		"-show":                           "feed 名稱",
		"show-":                           "feed 名稱",
		"a b":                             "feed 名稱",
		"":                                "feed 名稱",
		strings.Repeat("a", 41):           "太長",
		"feed":                            "泛用名稱",
		"rss":                             "泛用名稱",
		"podcast":                         "泛用名稱",
		"index":                           "泛用名稱",
		"xml":                             "泛用名稱",
		strings.Repeat("a", 40):           "", // 剛好 40 個字元：合法
		"my-show-2":                       "",
		"feeds2":                          "", // 只有完整相同的名稱才是泛用名稱
		"show-" + strings.Repeat("b", 10): "",
	} {
		err := s.podcastFeed(name, ok)
		if want == "" {
			if err != nil {
				t.Errorf("名稱 %q 應該合法：%v", name, err)
			}
			continue
		}
		wantFail(t, "名稱 "+name, err, want)
	}

	wantFail(t, "表頭錯誤", s.podcastFeed("hdr", table([]string{"id", "title", "audio"}, []string{"a", "b", "ep41.mp3"})), "表頭")
	wantFail(t, "表頭順序", s.podcastFeed("hdr", table([]string{"guid", "audio", "title"}, []string{"a", "ep41.mp3", "b"})), "表頭")
	wantFail(t, "表頭少一欄", s.podcastFeed("hdr", table([]string{"guid", "title"}, []string{"a", "b"})), "表頭")
	wantFail(t, "列的欄數", s.podcastFeed("cols", table([]string{"guid", "title", "audio"}, []string{"a", "ep41.mp3"})), "三欄")
	wantFail(t, "guid 重複", s.podcastFeed("dup", table(
		[]string{"guid", "title", "audio"},
		[]string{"a", "A", "ep41.mp3"},
		[]string{"a", "B", "ep42.mp3"},
	)), "重複")
	wantFail(t, "guid 為空", s.podcastFeed("empty", table([]string{"guid", "title", "audio"}, []string{"", "A", "ep41.mp3"})), "不可為空")
	wantFail(t, "audio 為空", s.podcastFeed("empty", table([]string{"guid", "title", "audio"}, []string{"a", "A", ""})), "不可為空")
	wantFail(t, "沒有表格", s.podcastFeed("none", nil), "表格")
}

func TestPodcastFeedFixtureValidation(t *testing.T) {
	s := newTestScenario(t)
	tbl := func(audio string) *godog.Table {
		return table([]string{"guid", "title", "audio"}, []string{"ep-1", "One", audio})
	}
	// fixture 不存在。
	wantFail(t, "fixture 不存在", s.podcastFeed("show", tbl("nope.mp3")), "podcast/nope.mp3")
	wantFail(t, "完整網址的 fixture 不存在", s.podcastFeed("show", tbl("https://media.example.com/x/nope.mp3")), "podcast/nope.mp3")
	// fixture 存在但不是假媒體檔（字幕檔）。
	wantFail(t, "不是 marker", s.podcastFeed("show", tbl("ep42-transcript.vtt")), "不是假媒體檔")
	// 檔名不能跳出 podcast/。
	for _, bad := range []string{"../local/talk.mp4", "sub/ep41.mp3", ".hidden", "ep41.mp3?x=1"} {
		wantFail(t, bad, s.podcastFeed("show", tbl(bad)), "不正確")
	}
	wantFail(t, "非 http 網址", s.podcastFeed("show", tbl("ftp://media.example.com/ep41.mp3")), "http(s)")
	wantFail(t, "網址沒有檔名", s.podcastFeed("show", tbl("https://media.example.com/")), "檔名")
	// 失敗的步驟不留下 feed。
	if _, ok := s.World.Podcasts.Episode("pod:show/ep-1"); ok {
		t.Error("失敗的步驟不該登記 feed")
	}
}

func TestPodcastTranscriptSteps(t *testing.T) {
	s := newTestScenario(t)
	if err := s.podcastFeed("show", table(
		[]string{"guid", "title", "audio"},
		[]string{"ep-42", "Episode 42", "ep42.mp3"},
		[]string{"ep-43", "Episode 43", "ep43-with-extra-ads.mp3"},
	)); err != nil {
		t.Fatal(err)
	}
	// feed 或集不存在、副檔名、fixture 不存在：失敗（不是 pending）。
	wantFail(t, "feed 不存在", s.podcastTranscriptReturns("ep42-transcript.vtt", "pod:other/ep-42"), "不存在")
	wantFail(t, "集不存在", s.podcastTranscriptReturns("ep42-transcript.vtt", "pod:show/ep-99"), "不存在")
	wantFail(t, "不是 Podcast 代號", s.podcastTranscriptReturns("ep42-transcript.vtt", "yt:abc123xxxxx"), "代號")
	wantFail(t, "副檔名", s.podcastTranscriptReturns("ep42.mp3", "pod:show/ep-42"), "副檔名")
	wantFail(t, "html", s.podcastTranscriptReturns("ep42-transcript.html", "pod:show/ep-42"), "副檔名")
	wantFail(t, "fixture 不存在", s.podcastTranscriptReturns("nope.vtt", "pod:show/ep-42"), "podcast/nope.vtt")
	wantFail(t, "has nothing 的 feed 不存在", s.podcastTranscriptNothing("pod:other/ep-42"), "不存在")
	if s.World.Podcasts.HasTranscript("pod:show/ep-42") {
		t.Fatal("失敗的步驟不該留下字幕")
	}

	if err := s.podcastTranscriptReturns("ep42-transcript.vtt", "pod:show/ep-42"); err != nil {
		t.Fatal(err)
	}
	if !s.World.Podcasts.HasTranscript("pod:show/ep-42") || s.World.Podcasts.HasTranscript("pod:show/ep-43") {
		t.Fatal("只有 ep-42 有字幕")
	}
	// feed 帶 <podcast:transcript>，字幕網址可以下載並被計次。
	_, feed := httpGet(t, s, "https://feeds.example.com/show.xml", nil)
	if !strings.Contains(string(feed), `<podcast:transcript url="https://media.example.com/show/ep42-transcript.vtt" type="text/vtt"/>`) {
		t.Errorf("feed 沒有字幕標籤：\n%s", feed)
	}
	if n, _ := s.sourceCalls(podcastTranscriptID); n != 0 {
		t.Errorf("還沒下載字幕，呼叫次數 = %d", n)
	}
	resp, body := httpGet(t, s, "https://media.example.com/show/ep42-transcript.vtt", nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(string(body), "WEBVTT") || resp.Header.Get("Content-Type") != "text/vtt" {
		t.Errorf("字幕 status=%d type=%q body=%.20q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if n, err := s.sourceCalls(podcastTranscriptID); err != nil || n != 1 {
		t.Errorf("sourceCalls = %d, %v，預期 1", n, err)
	}
	if err := s.sourceWasCalled(podcastTranscriptID, 1); err != nil {
		t.Error(err)
	}

	// 同一個字幕檔給同一個 feed 的另一集：字幕網址相同，失敗。
	wantFail(t, "字幕網址重複", s.podcastTranscriptReturns("ep42-transcript.vtt", "pod:show/ep-43"), "已經是")
	// 另一集用另一個字幕檔。
	if err := s.podcastTranscriptReturns("ep43-transcript.vtt", "pod:show/ep-43"); err != nil {
		t.Fatal(err)
	}
	// has nothing：移除字幕標籤，字幕網址回 404。
	if err := s.podcastTranscriptNothing("pod:show/ep-42"); err != nil {
		t.Fatal(err)
	}
	if s.World.Podcasts.HasTranscript("pod:show/ep-42") {
		t.Error("has nothing 之後不該有字幕")
	}
	if resp, _ := httpGet(t, s, "https://media.example.com/show/ep42-transcript.vtt", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("移除後的字幕 status = %d", resp.StatusCode)
	}
}

// 來源 podcast-transcript 的步驟分派：production 還沒實作時 pending；cookie 與檔案大小對它沒有意義，一律失敗。
func TestPodcastTranscriptSourceDispatch(t *testing.T) {
	s := newTestScenario(t)
	if err := s.podcastFeed("show", table([]string{"guid", "title", "audio"}, []string{"ep-42", "Episode 42", "ep42.mp3"})); err != nil {
		t.Fatal(err)
	}
	err := s.sourceReturns(podcastTranscriptID, "ep42-transcript.vtt", "pod:show/ep-42")
	if source.Implemented(podcastTranscriptID) {
		if err != nil || !s.World.Podcasts.HasTranscript("pod:show/ep-42") {
			t.Errorf("來源已實作，應該登記字幕：%v", err)
		}
	} else if !errors.Is(err, godog.ErrPending) {
		t.Errorf("來源還沒實作，應該 pending：%v", err)
	}
	wantFail(t, "requires the cookie", s.sourceRequiresCookie(podcastTranscriptID, "youtube"), "cookie")
	wantFail(t, "accepts files up to", s.sourceAcceptsUpTo(podcastTranscriptID, "25MB"), "大小")
	if source.Implemented(podcastTranscriptID) {
		if err := s.sourceHasNothing(podcastTranscriptID, "pod:show/ep-42"); err != nil {
			t.Error(err)
		}
		wantFail(t, "has nothing 的集不存在", s.sourceHasNothing(podcastTranscriptID, "pod:show/none"), "不存在")
	}
}

func TestPodcastIngestBody(t *testing.T) {
	s := newTestScenario(t)
	got := s.ingestBody("pod:show/ep-42")
	eps, _ := got["episodes"].([]string)
	if got["url"] != "https://feeds.example.com/show.xml" || len(eps) != 1 || eps[0] != "ep-42" || got["path"] != nil {
		t.Errorf("body = %v", got)
	}
	// guid 含 /：以第一個 / 切開。
	got = s.ingestBody("pod:show/a/b")
	if eps, _ := got["episodes"].([]string); got["url"] != "https://feeds.example.com/show.xml" || len(eps) != 1 || eps[0] != "a/b" {
		t.Errorf("body = %v", got)
	}
	// 不是合法的代號：當作網址，讓請求自己回報錯誤。
	if got := s.ingestBody("pod:show"); got["url"] != "pod:show" || got["episodes"] != nil {
		t.Errorf("body = %v", got)
	}
	// 其他代號不受影響。
	if got := s.ingestBody("yt:abc123xxxxx"); got["url"] != "https://www.youtube.com/watch?v=abc123xxxxx" {
		t.Errorf("body = %v", got)
	}
}

// pod: 的字幕來源：必須是 production 已實作 podcast-transcript，而且 feed 帶有字幕標籤。
func TestHasTranscriptSourceForPodcast(t *testing.T) {
	s := newTestScenario(t)
	if err := s.podcastFeed("show", table([]string{"guid", "title", "audio"}, []string{"ep-42", "Episode 42", "ep42.mp3"})); err != nil {
		t.Fatal(err)
	}
	if s.hasTranscriptSource("pod:show/ep-42") {
		t.Error("feed 沒有字幕標籤")
	}
	// 直接寫入 fake 狀態（不經來源步驟）：結果只取決於 production 是否已實作來源。
	if err := s.World.Podcasts.SetTranscript("pod:show/ep-42", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.podcastTranscriptReturns("ep42-transcript.vtt", "pod:show/ep-42"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.hasTranscriptSource("pod:show/ep-42"), source.Implemented(podcastTranscriptID); got != want {
		t.Errorf("hasTranscriptSource = %v，預期 %v（跟 source.Implemented 一致）", got, want)
	}
	if s.hasTranscriptSource("pod:show/ep-99") || s.hasTranscriptSource("pod:other/ep-1") {
		t.Error("不存在的集沒有字幕來源")
	}
}
