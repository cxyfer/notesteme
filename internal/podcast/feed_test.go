package podcast

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/netguard"
)

const fullRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
<title>The Show</title>
<itunes:title>Not This Title</itunes:title>
<link>https://example.com/show</link>
<atom:link href="https://example.com/feed.xml" rel="self" type="application/rss+xml"/>
<language>en-us</language>
<item>
  <guid isPermaLink="false"> ep-42 </guid>
  <title>Episode 42</title>
  <itunes:title>Wrong</itunes:title>
  <link>https://example.com/ep42</link>
  <description><![CDATA[Notes <b>here</b>]]></description>
  <pubDate>Wed, 01 Oct 2025 08:00:00 +0000</pubDate>
  <enclosure url="https://media.example.com/ep42.mp3" type="audio/mpeg" length="123456"/>
  <enclosure url="https://media.example.com/second.mp3" type="audio/mpeg" length="9"/>
  <itunes:duration>30:00</itunes:duration>
  <podcast:transcript url="https://media.example.com/ep42.vtt" type="text/vtt" language="en" rel="captions"/>
  <podcast:transcript url="https://media.example.com/ep42.html" type="text/html"/>
  <podcast:transcript type="text/vtt"/>
</item>
<item>
  <title>No guid</title>
  <enclosure url="https://media.example.com/noguid.mp3" type="audio/mpeg"/>
</item>
<item>
  <title>Nothing to identify</title>
</item>
<item>
  <guid>ep-42</guid>
  <title>Duplicate</title>
</item>
<item>
  <guid>text-only</guid>
  <title>Text only</title>
  <pubDate>not a date</pubDate>
  <itunes:duration>nonsense</itunes:duration>
</item>
</channel>
</rss>`

func TestParseRSS(t *testing.T) {
	f, err := Parse(strings.NewReader(fullRSS))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "The Show" || f.Language != "en-us" || f.Link != "https://example.com/show" {
		t.Errorf("feed = %+v", f)
	}
	if len(f.Episodes) != 3 {
		t.Fatalf("episodes = %+v", f.Episodes)
	}
	ep := f.Episodes[0]
	if ep.GUID != "ep-42" || ep.Title != "Episode 42" || ep.Link != "https://example.com/ep42" || ep.Description != "Notes <b>here</b>" {
		t.Errorf("ep = %+v", ep)
	}
	if ep.PublishedAt == nil || !ep.PublishedAt.Equal(time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("publishedAt = %v", ep.PublishedAt)
	}
	if ep.DurationS == nil || *ep.DurationS != 1800 {
		t.Errorf("duration = %v", ep.DurationS)
	}
	if ep.Enclosure == nil || *ep.Enclosure != (Enclosure{URL: "https://media.example.com/ep42.mp3", Type: "audio/mpeg", Length: 123456}) {
		t.Errorf("enclosure = %+v", ep.Enclosure)
	}
	want := []Transcript{
		{URL: "https://media.example.com/ep42.vtt", Type: "text/vtt", Language: "en", Rel: "captions"},
		{URL: "https://media.example.com/ep42.html", Type: "text/html"},
	}
	if len(ep.Transcripts) != 2 || ep.Transcripts[0] != want[0] || ep.Transcripts[1] != want[1] {
		t.Errorf("transcripts = %+v", ep.Transcripts)
	}

	// 沒有 guid：用音檔網址。
	if g := f.Episodes[1]; g.GUID != "https://media.example.com/noguid.mp3" || g.Title != "No guid" {
		t.Errorf("no guid = %+v", g)
	}
	// 時間或長度格式不認得：欄位維持 nil，集仍然保留；重複的 guid 第一個勝出。
	if g := f.Episodes[2]; g.GUID != "text-only" || g.PublishedAt != nil || g.DurationS != nil || g.Enclosure != nil {
		t.Errorf("text only = %+v", g)
	}
	if got, ok := f.Find(" ep-42 "); !ok || got.Title != "Episode 42" {
		t.Errorf("Find = %+v, %v", got, ok)
	}
	if _, ok := f.Find("missing"); ok {
		t.Error("不存在的 guid 不該找到")
	}
	if _, ok := f.Find(""); ok {
		t.Error("空 guid 不該找到")
	}
}

// namespace 以 URI 或慣用前綴比對：宣告缺漏時前綴仍然有效，換了前綴但 URI 對的也有效。
func TestParseNamespaceMatching(t *testing.T) {
	body := `<rss version="2.0" xmlns:it="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:pc="https://podcastindex.org/namespace/1.0">
<channel><title>T</title>
<item><guid>a</guid><it:duration>90</it:duration><pc:transcript url="https://x.test/a.srt" type="application/x-subrip"/></item>
<item><guid>b</guid><itunes:duration>120</itunes:duration><podcast:transcript url="https://x.test/b.json" type="application/json"/></item>
</channel></rss>`
	f, err := Parse(strings.NewReader(body))
	if err != nil || len(f.Episodes) != 2 {
		t.Fatalf("%+v, %v", f, err)
	}
	for i, wantDur := range []float64{90, 120} {
		ep := f.Episodes[i]
		if ep.DurationS == nil || *ep.DurationS != wantDur || len(ep.Transcripts) != 1 {
			t.Errorf("episode %d = %+v", i, ep)
		}
	}
}

func TestParseAtom(t *testing.T) {
	body := `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom" xml:lang="en">
<title>Atom Show</title>
<link rel="self" href="https://example.com/atom"/>
<link rel="alternate" href="https://example.com/"/>
<entry>
  <id>tag:example.com,2025:1</id>
  <title>Entry One</title>
  <link rel="alternate" href="https://example.com/1"/>
  <link rel="enclosure" href="https://media.example.com/1.m4a" type="audio/mp4" length="77"/>
  <summary>Short</summary>
  <published>2025-10-01T08:00:00+02:00</published>
</entry>
<entry><id>tag:2</id><title>Entry Two</title></entry>
</feed>`
	f, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Atom Show" || f.Language != "en" || f.Link != "https://example.com/" || len(f.Episodes) != 2 {
		t.Fatalf("feed = %+v", f)
	}
	ep := f.Episodes[0]
	if ep.GUID != "tag:example.com,2025:1" || ep.Title != "Entry One" || ep.Link != "https://example.com/1" || ep.Description != "Short" {
		t.Errorf("ep = %+v", ep)
	}
	if ep.Enclosure == nil || *ep.Enclosure != (Enclosure{URL: "https://media.example.com/1.m4a", Type: "audio/mp4", Length: 77}) {
		t.Errorf("enclosure = %+v", ep.Enclosure)
	}
	if ep.PublishedAt == nil || !ep.PublishedAt.Equal(time.Date(2025, 10, 1, 6, 0, 0, 0, time.UTC)) {
		t.Errorf("publishedAt = %v", ep.PublishedAt)
	}
	if f.Episodes[1].Enclosure != nil {
		t.Error("沒有音檔的集 Enclosure 應為 nil")
	}
}

func TestParseTimeFormats(t *testing.T) {
	want := time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)
	for _, s := range []string{
		"Wed, 01 Oct 2025 08:00:00 +0000", "Wed, 01 Oct 2025 08:00:00 GMT", "01 Oct 25 08:00 UTC", "01 Oct 25 08:00 +0000",
		"Wed, 1 Oct 2025 08:00:00 +0000", "2025-10-01T08:00:00Z",
	} {
		got := parseTime(s)
		if got == nil || !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v", s, got)
		}
	}
	if parseTime("yesterday") != nil || parseTime("") != nil {
		t.Error("不認得的格式應為 nil")
	}
}

func TestParseMalformed(t *testing.T) {
	for name, body := range map[string]string{
		"html":    "<html><body>hi</body></html>",
		"empty":   "",
		"text":    "just words",
		"foreign": `<feed xmlns="urn:other"/>`,
		"json":    `{"a":1}`,
	} {
		if _, err := Parse(strings.NewReader(body)); !errors.Is(err, ErrNotFeed) {
			t.Errorf("%s：err = %v", name, err)
		}
	}
}

// 根元素確認之後只是盡力而為：被截斷的內容回傳已讀到的部分。
func TestParseTruncatedKeepsWhatWasRead(t *testing.T) {
	body := `<?xml version="1.0"?><rss version="2.0"><channel><title>The Show</title>
<item><guid>a</guid><title>A</title></item><item><guid>b</guid><title>B</ti`
	f, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "The Show" || len(f.Episodes) != 1 || f.Episodes[0].GUID != "a" {
		t.Errorf("feed = %+v", f)
	}
}

func TestParseLatin1(t *testing.T) {
	body := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><title>Caf\xe9</title></channel></rss>"
	f, err := Parse(strings.NewReader(body))
	if err != nil || f.Title != "Café" {
		t.Fatalf("%+v, %v", f, err)
	}
	if _, err := Parse(strings.NewReader(`<?xml version="1.0" encoding="Shift_JIS"?><rss><channel/></rss>`)); !errors.Is(err, ErrNotFeed) {
		t.Errorf("不支援的編碼：%v", err)
	}
}

// Windows-1252 的 0x80–0x9F 是彎引號、破折號等標點，不是 C1 控制字元；宣告 ISO-8859-1 的 feed 也同樣處理（同 WHATWG）。
func TestParseWindows1252(t *testing.T) {
	for _, enc := range []string{"windows-1252", "cp1252", "ISO-8859-1"} {
		body := "<?xml version=\"1.0\" encoding=\"" + enc + "\"?><rss><channel><title>\x93Caf\xe9\x94 \x97 it\x92s \x80 5</title></channel></rss>"
		f, err := Parse(strings.NewReader(body))
		if want := "“Café” — it’s € 5"; err != nil || f.Title != want {
			t.Errorf("%s：title = %q, %v，預期 %q", enc, f.Title, err, want)
		}
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func client(status int, body string, contentLength int64) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, ContentLength: contentLength, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

func TestFetch(t *testing.T) {
	ctx := context.Background()
	f, err := Fetch(ctx, client(200, fullRSS, -1), "https://feeds.example.com/show.xml")
	if err != nil || f.Title != "The Show" {
		t.Fatalf("%+v, %v", f, err)
	}

	var he *HTTPError
	if _, err := Fetch(ctx, client(404, fullRSS, -1), "https://feeds.example.com/show.xml"); !errors.As(err, &he) || he.StatusCode != 404 {
		t.Errorf("404：%v", err)
	}
	if _, err := Fetch(ctx, client(200, "<html/>", -1), "https://feeds.example.com/x"); !errors.Is(err, ErrNotFeed) {
		t.Errorf("html：%v", err)
	}
	if _, err := Fetch(ctx, client(200, fullRSS, netguard.LimitFeed+1), "https://feeds.example.com/x"); !errors.Is(err, netguard.ErrTooLarge) {
		t.Errorf("Content-Length 超過上限：%v", err)
	}
	big := fullRSS + strings.Repeat(" ", int(netguard.LimitFeed))
	if _, err := Fetch(ctx, client(200, big, -1), "https://feeds.example.com/x"); !errors.Is(err, netguard.ErrTooLarge) {
		t.Errorf("超過上限：%v", err)
	}
	if _, err := Fetch(ctx, nil, "https://feeds.example.com/x"); err == nil {
		t.Error("沒有 client 應失敗")
	}
	// 連線被防護擋下的錯誤原樣回傳，呼叫端用 netguard.PolicyMessage 判斷。
	blocked := &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) { return nil, netguard.ErrBlockedAddress })}
	if _, err := Fetch(ctx, blocked, "https://feeds.example.com/x"); !errors.Is(err, netguard.ErrBlockedAddress) {
		t.Errorf("被擋：%v", err)
	}
}

// XML 巢狀超過 64 層：停止解析並回 ErrNotFeed，不能因為深層巢狀配置大量記憶體。
func TestParseRejectsDeepNesting(t *testing.T) {
	const bombSize = 4 << 20
	bomb := "<rss><channel>" + strings.Repeat("<a>", bombSize/3)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Parse(strings.NewReader(bomb))
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrNotFeed) {
		t.Fatalf("err = %v，預期 ErrNotFeed", err)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 32<<20 {
		t.Errorf("解析 %d bytes 的巢狀配置了 %d MiB", len(bomb), alloc>>20)
	}

	// 深度剛好在上限內（rss、channel 加上 62 層）仍可解析，含這之前的 item。
	ok := `<rss><channel><item><guid>g</guid><enclosure url="https://m.example.com/a.mp3"/></item>` +
		strings.Repeat("<a>", 62) + strings.Repeat("</a>", 62) + `</channel></rss>`
	feed, err := Parse(strings.NewReader(ok))
	if err != nil || len(feed.Episodes) != 1 {
		t.Fatalf("深度 64：%+v, %v", feed, err)
	}
	// 超過一層就拒絕。
	tooDeep := `<rss><channel><item><guid>g</guid></item>` + strings.Repeat("<a>", 63) + strings.Repeat("</a>", 63) + `</channel></rss>`
	if _, err := Parse(strings.NewReader(tooDeep)); !errors.Is(err, ErrNotFeed) {
		t.Errorf("深度 65：err = %v", err)
	}
}

// 標題與描述只收沒有 namespace、RSS 或 Atom 的元素：media:title、dc:title 排在前面也不會取代標題。
func TestParseTitleAndDescriptionIgnoreForeignNamespaces(t *testing.T) {
	body := `<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd">
<channel>
<media:title>Wrong Show</media:title>
<title>The Show</title>
<item>
  <guid>a</guid>
  <media:title>Wrong</media:title>
  <dc:title>Wrong too</dc:title>
  <media:description>Wrong description</media:description>
  <media:content url="https://x.test/video.mp4"/>
  <itunes:summary>Fallback summary</itunes:summary>
  <title>Right</title>
  <description>Right description</description>
</item>
<item><guid>b</guid><media:title>Only media</media:title><itunes:summary>Summary only</itunes:summary></item>
</channel></rss>`
	f, err := Parse(strings.NewReader(body))
	if err != nil || len(f.Episodes) != 2 {
		t.Fatalf("%+v, %v", f, err)
	}
	if f.Title != "The Show" {
		t.Errorf("feed title = %q", f.Title)
	}
	if ep := f.Episodes[0]; ep.Title != "Right" || ep.Description != "Right description" {
		t.Errorf("ep a = %+v", ep)
	}
	// 沒有標題時不拿 media:title 湊數；itunes:summary 只在沒有 description 時當後備。
	if ep := f.Episodes[1]; ep.Title != "" || ep.Description != "Summary only" {
		t.Errorf("ep b = %+v", ep)
	}

	atom := `<feed xmlns="http://www.w3.org/2005/Atom" xmlns:media="http://search.yahoo.com/mrss/">
<media:title>Wrong</media:title><title>Atom Show</title>
<entry><id>1</id><media:title>Wrong</media:title><title>Entry</title><media:content url="x"/><summary>Sum</summary></entry></feed>`
	f, err = Parse(strings.NewReader(atom))
	if err != nil || f.Title != "Atom Show" || len(f.Episodes) != 1 || f.Episodes[0].Title != "Entry" || f.Episodes[0].Description != "Sum" {
		t.Fatalf("atom：%+v, %v", f, err)
	}
}

// Atom 的 published 優先於 updated，不論順序。
func TestParseAtomPublishedBeatsUpdated(t *testing.T) {
	for name, order := range map[string]string{
		"updated 在前":   `<updated>2025-12-01T00:00:00Z</updated><published>2025-10-01T00:00:00Z</published>`,
		"published 在前": `<published>2025-10-01T00:00:00Z</published><updated>2025-12-01T00:00:00Z</updated>`,
	} {
		f, err := Parse(strings.NewReader(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>1</id>` + order + `</entry></feed>`))
		if err != nil || len(f.Episodes) != 1 {
			t.Fatalf("%s：%+v, %v", name, f, err)
		}
		if got := f.Episodes[0].PublishedAt; got == nil || !got.Equal(time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("%s：publishedAt = %v", name, got)
		}
	}
	// 只有 updated 時用它。
	f, _ := Parse(strings.NewReader(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>1</id><updated>2025-12-01T00:00:00Z</updated></entry></feed>`))
	if got := f.Episodes[0].PublishedAt; got == nil || !got.Equal(time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("只有 updated：%v", got)
	}
}

// 不可信欄位的長度：guid 超過 1 KiB 的集略過；title 截到 1 KiB、description 截到 64 KiB（依 rune 邊界）。
func TestParseLimitsUntrustedFieldLengths(t *testing.T) {
	long := strings.Repeat("g", 1025)
	body := `<rss><channel><title>` + strings.Repeat("字", 1000) + `</title>
<item><guid>` + long + `</guid><title>skipped</title></item>
<item><guid>` + strings.Repeat("g", 1024) + `</guid><title>` + strings.Repeat("字", 1000) + `</title><description>` + strings.Repeat("é", 40000) + `</description></item>
<item><title>no guid</title><enclosure url="https://m.test/` + strings.Repeat("a", 1100) + `.mp3"/></item>
</channel></rss>`
	f, err := Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Episodes) != 1 {
		t.Fatalf("episodes = %d，guid 過長的集與用過長音檔網址當 guid 的集都要略過", len(f.Episodes))
	}
	ep := f.Episodes[0]
	if len(ep.GUID) != 1024 {
		t.Errorf("guid 長度 = %d，剛好 1 KiB 應保留", len(ep.GUID))
	}
	if len(ep.Title) > 1024 || len(ep.Title) < 1024-3 || !utf8.ValidString(ep.Title) {
		t.Errorf("title %d bytes，valid = %v", len(ep.Title), utf8.ValidString(ep.Title))
	}
	if len(ep.Description) > 64<<10 || len(ep.Description) < 64<<10-1 || !utf8.ValidString(ep.Description) {
		t.Errorf("description %d bytes，valid = %v", len(ep.Description), utf8.ValidString(ep.Description))
	}
	if len(f.Title) > 1024 || !utf8.ValidString(f.Title) {
		t.Errorf("feed title %d bytes", len(f.Title))
	}
}
