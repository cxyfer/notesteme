package subscription

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

func TestRecognize(t *testing.T) {
	const ch = "UCfixture000000000000000"
	ok := []struct {
		in   string
		want Source
	}{
		{"https://www.youtube.com/channel/" + ch, Source{KindChannel, PlatformYouTube, ch, ChannelURL(ch)}},
		{"https://m.youtube.com/channel/" + ch + "/videos", Source{KindChannel, PlatformYouTube, ch, ChannelURL(ch)}},
		{"https://www.youtube.com/@FixtureChannel", Source{KindChannel, PlatformYouTube, "@FixtureChannel", "https://www.youtube.com/@FixtureChannel"}},
		{"https://www.youtube.com/playlist?list=PL123", Source{KindPlaylist, PlatformYouTube, "PL123", "https://www.youtube.com/playlist?list=PL123"}},
		{"https://space.bilibili.com/9/lists/77?type=season", Source{KindCollection, PlatformBilibili, "9:77", CollectionURL("9", "77")}},
		{"https://space.bilibili.com/9/channel/collectiondetail?sid=77", Source{KindCollection, PlatformBilibili, "9:77", CollectionURL("9", "77")}},
	}
	for _, c := range ok {
		got, err := Recognize(c.in)
		if err != nil || got != c.want {
			t.Errorf("Recognize(%q) = %+v, %v；預期 %+v", c.in, got, err, c.want)
		}
	}
	feed, err := Recognize("https://feeds.example.com/show.xml")
	if err != nil || feed.Kind != KindFeed || feed.Platform != PlatformPodcast || feed.ExternalID != feed.URL {
		t.Errorf("Podcast feed：%+v, %v", feed, err)
	}

	bad := []struct{ in, code string }{
		{"https://www.youtube.com/watch?v=abcdefghijk", CodeNotACollection},
		{"https://youtu.be/abcdefghijk", CodeNotACollection},
		{"https://www.bilibili.com/video/BV1xx411c7mD", CodeNotACollection},
		{"https://www.youtube.com/channel/UCshort", CodeUnsupportedURL},
		{"https://www.youtube.com/channel/" + ch + "/shorts", CodeUnsupportedURL},
		{"https://www.youtube.com/playlist?list=", CodeUnsupportedURL},
		{"https://www.youtube.com/playlist?list=a/b", CodeUnsupportedURL},
		{"https://www.youtube.com/results?search_query=x", CodeUnsupportedURL},
		{"https://space.bilibili.com/9", CodeUnsupportedURL},
		{"https://space.bilibili.com/9/lists/77?type=series", CodeUnsupportedURL},
		{"https://space.bilibili.com/x/lists/77?type=season", CodeUnsupportedURL},
		{"https://live.bilibili.com/9/lists/77?type=season", CodeUnsupportedURL},
		{"https://b23.tv/abc", CodeUnsupportedURL},
		{"ftp://feeds.example.com/a.xml", CodeUnsupportedURL},
		{"javascript:alert(1)", CodeUnsupportedURL},
		{"", CodeUnsupportedURL},
		{"not a url", CodeUnsupportedURL},
	}
	for _, c := range bad {
		_, err := Recognize(c.in)
		var ue *URLError
		if !errors.As(err, &ue) || ue.Code != c.code {
			t.Errorf("Recognize(%q) 的錯誤 = %v；預期 code %s", c.in, err, c.code)
		}
	}
}

func intp(n int) *int { return &n }

func TestValidateBackfill(t *testing.T) {
	cases := []struct {
		b    Backfill
		path string
	}{
		{Backfill{Mode: BackfillNone}, ""},
		{Backfill{Mode: BackfillAll}, ""},
		{Backfill{Mode: BackfillRecent, Count: intp(1)}, ""},
		{Backfill{Mode: BackfillRecent, Count: intp(MaxRecent)}, ""},
		{Backfill{Mode: BackfillRecent, Count: intp(0)}, "backfill.count"},
		{Backfill{Mode: BackfillRecent, Count: intp(MaxRecent + 1)}, "backfill.count"},
		{Backfill{Mode: BackfillRecent}, "backfill.count"},
		{Backfill{Mode: "latest"}, "backfill.mode"},
		{Backfill{}, "backfill.mode"},
	}
	for _, c := range cases {
		errs := validateBackfill(c.b)
		got := ""
		if len(errs) > 0 {
			got = errs[0].Path
		}
		if got != c.path || len(errs) > 1 {
			t.Errorf("validateBackfill(%+v) = %v；預期 path %q", c.b, errs, c.path)
		}
	}
	if b := normalizeBackfill(Backfill{Mode: BackfillAll, Count: intp(3)}); b.Count != nil {
		t.Errorf("all 不應保留 count：%+v", b)
	}
}

func TestValidateFilters(t *testing.T) {
	base := DefaultFilters()
	with := func(f func(*Filters)) Filters { c := base; f(&c); return c }
	cases := []struct {
		f    Filters
		want string // "path=message"，空字串代表合法
	}{
		{base, ""},
		{with(func(f *Filters) { f.MinDurationS, f.MaxDurationS = intp(60), intp(61) }), ""},
		{with(func(f *Filters) { f.MinDurationS, f.MaxDurationS = intp(60), intp(60) }), "filters.max_duration_s=最長片長必須大於最短片長。"},
		{with(func(f *Filters) { f.MinDurationS = intp(-1) }), "filters.min_duration_s=片長必須是 0 以上的整數（秒）。"},
		{with(func(f *Filters) { f.Live = "record" }), "filters.live=直播的處理方式必須是 wait 或 skip。"},
		{with(func(f *Filters) { f.TitleExclude = "(" }), "filters.title_exclude=標題的正規表示式無效。"},
		{with(func(f *Filters) { f.TitleInclude = "(?<!x)y" }), "filters.title_include=標題的正規表示式無效。"},
		{with(func(f *Filters) { f.TitleInclude = strings.Repeat("a", maxTitleRegexp) }), ""},
		{with(func(f *Filters) { f.TitleInclude = strings.Repeat("字", maxTitleRegexp) }), ""},
		{with(func(f *Filters) { f.TitleInclude = strings.Repeat("a", maxTitleRegexp+1) }), "filters.title_include=標題的正規表示式最長 200 個字元。"},
	}
	for i, c := range cases {
		errs := validateFilters(c.f)
		got := ""
		if len(errs) > 0 {
			got = errs[0].Path + "=" + errs[0].Message
		}
		if got != c.want {
			t.Errorf("#%d validateFilters = %v；預期 %q", i, errs, c.want)
		}
	}
}

func TestFiltersInputApply(t *testing.T) {
	no, skip, re := false, LiveSkip, "x"
	got := (&FiltersInput{ExcludeShorts: &no, Live: &skip, TitleExclude: &re, MinDurationS: intp(5)}).apply(DefaultFilters())
	if got.ExcludeShorts || got.Live != LiveSkip || got.TitleExclude != "x" || got.MinDurationS == nil || *got.MinDurationS != 5 {
		t.Errorf("apply = %+v", got)
	}
	var nilIn *FiltersInput
	if got := nilIn.apply(DefaultFilters()); got != DefaultFilters() {
		t.Errorf("nil 輸入應保留原值：%+v", got)
	}
}

func TestSchedule(t *testing.T) {
	s := func(v string) *string { return &v }
	if d, errs := parseSchedule(nil); d != DefaultSched || errs != nil {
		t.Errorf("預設 = %v %v", d, errs)
	}
	for _, v := range []string{"1h", "6h", "168h", "90m"} {
		if _, errs := parseSchedule(s(v)); errs != nil {
			t.Errorf("%s 應合法：%v", v, errs)
		}
	}
	for _, v := range []string{"59m59s", "168h1s", "0", "-1h", "abc", "", "1d"} {
		if _, errs := parseSchedule(s(v)); len(errs) != 1 || errs[0].Path != "schedule" {
			t.Errorf("%q 應不合法：%v", v, errs)
		}
	}
	for d, want := range map[time.Duration]string{6 * time.Hour: "6h", 90 * time.Minute: "1h30m", 168 * time.Hour: "168h", time.Hour + time.Second: "1h0m1s", 10 * time.Hour: "10h"} {
		if got := FormatSchedule(d); got != want {
			t.Errorf("FormatSchedule(%v) = %q；預期 %q", d, got, want)
		}
	}
}

func TestDecodeSettingsDefaults(t *testing.T) {
	if b := decodeBackfill(nil); b.Mode != BackfillNone {
		t.Errorf("空的 backfill = %+v", b)
	}
	if b := decodeBackfill([]byte(`{`)); b.Mode != BackfillNone {
		t.Errorf("壞掉的 backfill = %+v", b)
	}
	if f := decodeFilters([]byte(`{"live":""}`)); f.Live != LiveWait || !f.ExcludeShorts {
		t.Errorf("filters 預設值 = %+v", f)
	}
}

func TestCleanText(t *testing.T) {
	cases := map[string]string{
		"  Hello   world \n":                          "Hello world",
		"<b>Bold</b> &amp; <i>it</i>":                 "Bold & it",
		"&lt;script&gt;alert(1)&lt;/script&gt;Title":  "Title",
		"A<script>alert(1)</script>B":                 "A B",
		"A<style>p{}</style>B":                        "A B",
		"A<script>never closed":                       "A",
		"x\u202Ey\u2066z\u200Ew\u0000v\u0007u\uFEFFt": "xyzwvut",
		"&amp;lt;b&amp;gt;":                           "&lt;b&gt;", // 只解碼一次
		"trailing <a href=":                           "trailing",
		"中文\t標題":                                      "中文 標題",
		"\xff\xfeBad":                                 "�Bad", // 連續的不合法位元組換成一個 U+FFFD
		"<!-- c -->ok":                                "ok",
		"1 < 2 and 3 > 2":                             "1 < 2 and 3 > 2",
	}
	for in, want := range cases {
		if got := CleanText(in); got != want {
			t.Errorf("CleanText(%q) = %q；預期 %q", in, got, want)
		}
	}
	long := CleanText(strings.Repeat("字", maxTitleRunes+50))
	if n := utf8.RuneCountInString(long); n != maxTitleRunes+1 || !strings.HasSuffix(long, "…") {
		t.Errorf("截斷後有 %d 個字元：%q", n, long[len(long)-10:])
	}
	if got := CleanText(strings.Repeat("字", maxTitleRunes)); utf8.RuneCountInString(got) != maxTitleRunes {
		t.Errorf("剛好 %d 個字元不應截斷", maxTitleRunes)
	}
}

func FuzzCleanText(f *testing.F) {
	for _, s := range []string{"", "<b>x</b>", "&lt;script&gt;", "\u202Ex", strings.Repeat("a ", 400), "<script>", "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := CleanText(s)
		if !utf8.ValidString(out) {
			t.Fatalf("輸出不是合法的 UTF-8：%q", out)
		}
		if n := utf8.RuneCountInString(out); n > maxTitleRunes+1 {
			t.Fatalf("輸出有 %d 個字元", n)
		}
		if out != strings.TrimSpace(out) || strings.Contains(out, "  ") {
			t.Fatalf("空白沒有合併：%q", out)
		}
		for _, r := range out {
			if (unicode.IsControl(r) && r != ' ') || isBidiControl(r) || r == 0xFEFF {
				t.Fatalf("輸出含控制字元 %U：%q", r, out)
			}
		}
	})
}

const okRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Show</title>
<item><guid>e1</guid><title>One &nbsp; &amp; two</title><link>/ep/1</link>
<enclosure url="audio/1.mp3" type="audio/mpeg" length="1"/></item>
</channel></rss>`

func TestValidateXML(t *testing.T) {
	if err := validateXML([]byte(okRSS)); err != nil {
		t.Fatalf("合法的 RSS：%v", err)
	}
	deep := strings.Repeat("<a>", maxXMLDepth) + strings.Repeat("</a>", maxXMLDepth)
	if err := validateXML([]byte(deep)); err != nil {
		t.Errorf("剛好 %d 層應接受：%v", maxXMLDepth, err)
	}
	bad := map[string]string{
		"DOCTYPE":        `<?xml version="1.0"?><!DOCTYPE rss><rss/>`,
		"小寫 doctype":     `<!doctype rss><rss/>`,
		"billion laughs": `<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;">]><rss>&lol2;</rss>`,
		"XXE":            `<?xml version="1.0"?><!DOCTYPE r [<!ENTITY x SYSTEM "file:///etc/passwd">]><rss>&x;</rss>`,
		"未定義的 entity":    `<rss>&undefined;</rss>`,
		"過深":             strings.Repeat("<a>", maxXMLDepth+1) + strings.Repeat("</a>", maxXMLDepth+1),
		"兩個根":            `<rss/><rss/>`,
		"沒有根":            `<?xml version="1.0"?>`,
		"空的":             ``,
		"沒有關上":           `<rss><channel>`,
		"標籤不配對":          `<rss><a></b></rss>`,
		"不是 XML":         `{"json": true}`,
		"HTML":           `<html><body><p>hi<br></body></html>`,
		"屬性沒有引號":         `<rss version=2.0/>`,
		"根之後還有文字以外的元素":   `<rss></rss><x/>`,
	}
	for name, body := range bad {
		err := validateXML([]byte(body))
		if err == nil {
			t.Errorf("%s 應拒絕", name)
			continue
		}
		if strings.Contains(err.Error(), "rss") || strings.Contains(err.Error(), "passwd") {
			t.Errorf("%s 的錯誤帶了文件內容：%v", name, err)
		}
	}
}

func TestParseFeed(t *testing.T) {
	base, _ := url.Parse("https://feeds.example.com/show/feed.xml")
	feed, err := parseFeed([]byte(okRSS), base)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Episodes) != 1 {
		t.Fatalf("集數 = %d", len(feed.Episodes))
	}
	ep := feed.Episodes[0]
	if ep.Link != "https://feeds.example.com/ep/1" || ep.Enclosure == nil || ep.Enclosure.URL != "https://feeds.example.com/show/audio/1.mp3" {
		t.Errorf("相對網址沒有解析：link=%q enclosure=%+v", ep.Link, ep.Enclosure)
	}

	var b strings.Builder
	b.WriteString(`<rss version="2.0"><channel><title>Big</title>`)
	for i := range MaxEntries + 5 {
		fmt.Fprintf(&b, `<item><guid>g%d</guid><title>t</title><enclosure url="https://a.example.com/%d.mp3" type="audio/mpeg"/></item>`, i, i)
	}
	b.WriteString(`</channel></rss>`)
	feed, err = parseFeed([]byte(b.String()), nil)
	if err != nil || len(feed.Episodes) != MaxEntries {
		t.Fatalf("上限：%v，%d 集", err, len(feed.Episodes))
	}

	for _, body := range []string{`<!DOCTYPE x><rss/>`, `<html/>`, `garbage`} {
		_, err := parseFeed([]byte(body), base)
		ce := asCheckError(err)
		if ce.Class != errclass.ClassPermanent || ce.Message != msgNotFeed {
			t.Errorf("parseFeed(%q) = %+v", body, ce)
		}
	}
}

func FuzzParseFeed(f *testing.F) {
	f.Add([]byte(okRSS))
	f.Add([]byte(`<!DOCTYPE x [<!ENTITY a "b">]><rss>&a;</rss>`))
	f.Add([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>1</id><title>x</title></entry></feed>`))
	f.Add([]byte(strings.Repeat("<a>", 100)))
	base, _ := url.Parse("https://feeds.example.com/x.xml")
	f.Fuzz(func(t *testing.T, body []byte) {
		feed, err := parseFeed(body, base)
		if err != nil {
			ce := asCheckError(err)
			if ce.Class != errclass.ClassPermanent || ce.Message != msgNotFeed {
				t.Fatalf("錯誤分類 = %+v", ce)
			}
			return
		}
		if len(feed.Episodes) > MaxEntries {
			t.Fatalf("超過上限：%d", len(feed.Episodes))
		}
	})
}

func FuzzRecognize(f *testing.F) {
	for _, s := range []string{"https://www.youtube.com/@x", "https://space.bilibili.com/1/lists/2?type=season", "https://feeds.example.com/a.xml", "https://www.youtube.com/playlist?list=PL1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		src, err := Recognize(raw)
		if err != nil {
			var ue *URLError
			if !errors.As(err, &ue) || (ue.Code != CodeUnsupportedURL && ue.Code != CodeNotACollection) {
				t.Fatalf("錯誤型別 = %T %v", err, err)
			}
			return
		}
		// 正規化之後的網址再辨識一次，結果相同（存進資料庫的網址可以重新辨識）。
		again, err := Recognize(src.URL)
		if err != nil || again != src {
			t.Fatalf("Recognize(%q) = %+v，再辨識 %q = %+v, %v", raw, src, src.URL, again, err)
		}
	})
}

func TestSafeLink(t *testing.T) {
	for in, want := range map[string]bool{
		"https://a.example.com/x": true, "HTTP://a.example.com": true, " https://a.example.com ": true,
		"javascript:alert(1)": false, "data:text/html,x": false, "/relative": false, "": false, "ftp://a.example.com": false,
		"https:///nohost": false,
	} {
		if got := safeLink(in); got != want {
			t.Errorf("safeLink(%q) = %v", in, got)
		}
	}
}

func TestStatusError(t *testing.T) {
	cases := []struct {
		code      int
		class     errclass.Class
		msg       string
		throttled bool
	}{
		{429, errclass.ClassRetryable, "暫時無法檢查訂閱（HTTP 429），稍後會自動重試。", true},
		{503, errclass.ClassRetryable, "暫時無法檢查訂閱（HTTP 503），稍後會自動重試。", false},
		{408, errclass.ClassRetryable, "暫時無法檢查訂閱（HTTP 408），稍後會自動重試。", false},
		{404, errclass.ClassPermanent, "訂閱來源已不存在（HTTP 404）。請確認網址，或刪除這個訂閱。", false},
		{410, errclass.ClassPermanent, "訂閱來源已不存在（HTTP 410）。請確認網址，或刪除這個訂閱。", false},
		{403, errclass.ClassPermanent, "訂閱來源回應了錯誤（HTTP 403）。請確認網址，或刪除這個訂閱。", false},
		{304, errclass.ClassPermanent, "訂閱來源回應了錯誤（HTTP 304）。請確認網址，或刪除這個訂閱。", false},
	}
	for _, c := range cases {
		e := statusError(c.code)
		if e == nil || e.Class != c.class || e.Message != c.msg || e.throttled != c.throttled {
			t.Errorf("statusError(%d) = %+v", c.code, e)
		}
	}
	for _, c := range []int{200, 204, 299} {
		if e := statusError(c); e != nil {
			t.Errorf("statusError(%d) = %+v", c, e)
		}
	}
}

func TestFetchFeed(t *testing.T) {
	var sawClose bool
	mux := http.NewServeMux()
	mux.HandleFunc("/ok.xml", func(w http.ResponseWriter, r *http.Request) {
		sawClose = r.Close
		_, _ = w.Write([]byte(okRSS))
	})
	mux.HandleFunc("/gone.xml", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusGone) })
	mux.HandleFunc("/busy.xml", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	mux.HandleFunc("/hostile.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!DOCTYPE x [<!ENTITY s SYSTEM "file:///etc/passwd">]><rss>&s;</rss>`))
	})
	mux.HandleFunc("/huge.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(netguard.LimitFeed+1))
		_, _ = w.Write(make([]byte, 1024))
	})
	mux.HandleFunc("/redirect.xml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sub/ok.xml", http.StatusFound)
	})
	mux.HandleFunc("/sub/ok.xml", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(okRSS)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := srv.Client()

	feed, err := fetchFeed(t.Context(), c, srv.URL+"/ok.xml")
	if err != nil || len(feed.Episodes) != 1 {
		t.Fatalf("ok：%v", err)
	}
	if !sawClose {
		t.Error("請求應帶 Connection: close，讓每次檢查都重新解析 DNS（SUB-R11）")
	}
	// 轉址之後，相對網址以最後的網址為準。
	feed, err = fetchFeed(t.Context(), c, srv.URL+"/redirect.xml")
	if err != nil || feed.Episodes[0].Link != srv.URL+"/ep/1" || feed.Episodes[0].Enclosure.URL != srv.URL+"/sub/audio/1.mp3" {
		t.Fatalf("轉址：%v %+v", err, feed)
	}

	cases := map[string]struct {
		class errclass.Class
		msg   string
	}{
		"/gone.xml":    {errclass.ClassPermanent, "訂閱來源已不存在（HTTP 410）。請確認網址，或刪除這個訂閱。"},
		"/busy.xml":    {errclass.ClassRetryable, "暫時無法檢查訂閱（HTTP 429），稍後會自動重試。"},
		"/hostile.xml": {errclass.ClassPermanent, msgNotFeed},
		"/huge.xml":    {errclass.ClassPermanent, "回應超過大小上限（10 MiB）。"},
	}
	for p, want := range cases {
		_, err := fetchFeed(t.Context(), c, srv.URL+p)
		ce := asCheckError(err)
		if ce.Class != want.class || ce.Message != want.msg {
			t.Errorf("%s：%+v；預期 %+v", p, ce, want)
		}
		if strings.Contains(ce.Message, "passwd") {
			t.Errorf("%s 的訊息帶了 feed 的內容", p)
		}
	}

	if _, err := fetchFeed(t.Context(), nil, srv.URL+"/ok.xml"); asCheckError(err).Class != errclass.ClassRetryable {
		t.Errorf("沒有 client：%v", err)
	}
	srv.Close()
	ce := asCheckError(func() error { _, err := fetchFeed(t.Context(), c, srv.URL+"/ok.xml"); return err }())
	if ce.Class != errclass.ClassRetryable || ce.Message != "暫時無法檢查訂閱（連線失敗），稍後會自動重試。" {
		t.Errorf("連線失敗：%+v", ce)
	}
}

func TestTransportError(t *testing.T) {
	ce := transportError(fmt.Errorf("dial: %w", netguard.ErrBlockedAddress))
	if ce.Class != errclass.ClassPermanent || ce.Message != "這個網址指向內部網路，已封鎖。" {
		t.Errorf("內部網路：%+v", ce)
	}
	ce = transportError(errors.New("secret body text"))
	if ce.Class != errclass.ClassRetryable || strings.Contains(ce.Message, "secret") {
		t.Errorf("其他錯誤：%+v", ce)
	}
}

func TestAsCheckError(t *testing.T) {
	ce := &checkError{Class: errclass.ClassNeedsAction, Message: "m"}
	if got := asCheckError(fmt.Errorf("wrap: %w", ce)); got != ce {
		t.Errorf("包裝過的 checkError 應原樣取出：%+v", got)
	}
	if got := asCheckError(errors.New("<rss>leak</rss>")); got.Class != errclass.ClassRetryable || strings.Contains(got.Message, "leak") {
		t.Errorf("未知錯誤：%+v", got)
	}
}
