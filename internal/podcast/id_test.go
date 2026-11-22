package podcast

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]float64{
		"1800": 1800, "1800.5": 1800.5, "30:00": 1800, "0:30:00": 1800, "1:02:03": 3723, " 90 ": 90,
		"0": 0, "90:00": 5400, "1:30.5": 90.5, "00:00:07": 7,
	} {
		if got, ok := ParseDuration(in); !ok || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v，預期 %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", " ", "abc", "-5", "1:2:3:4", "1.5:30", "30:", ":30", "1e3", "1..5", ".", "30 min"} {
		if got, ok := ParseDuration(in); ok {
			t.Errorf("ParseDuration(%q) = %v，應為 false", in, got)
		}
	}
}

func TestCanonicalURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://Feeds.Example.COM/Show.xml":         "https://feeds.example.com/Show.xml",
		"HTTPS://feeds.example.com:443/show.xml":     "https://feeds.example.com/show.xml",
		"http://feeds.example.com:80/show.xml":       "http://feeds.example.com/show.xml",
		"http://feeds.example.com:8080/show.xml":     "http://feeds.example.com:8080/show.xml",
		"https://feeds.example.com:80/show.xml":      "https://feeds.example.com:80/show.xml",
		"https://feeds.example.com/show.xml#top":     "https://feeds.example.com/show.xml",
		"https://feeds.example.com/show?a=1&b=2":     "https://feeds.example.com/show?a=1&b=2",
		"  https://feeds.example.com/show.xml  ":     "https://feeds.example.com/show.xml",
		"https://[::1]:8443/feed":                    "https://[::1]:8443/feed",
		"https://user:pw@feeds.example.com/Show.xml": "https://user:pw@feeds.example.com/Show.xml",
		// 主機根目錄的 feed：有沒有結尾的 / 是同一個資源。
		"https://feeds.example.com":      "https://feeds.example.com/",
		"https://feeds.example.com/":     "https://feeds.example.com/",
		"https://feeds.example.com?id=7": "https://feeds.example.com/?id=7",
	} {
		got, err := CanonicalURL(in)
		if err != nil || got.String() != want {
			t.Errorf("CanonicalURL(%q) = %v, %v，預期 %s", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://example.com/feed", "file:///etc/passwd", "https:///path", "not a url", "//example.com/feed", "mailto:a@b.c", "http://%zz"} {
		if got, err := CanonicalURL(in); err == nil {
			t.Errorf("CanonicalURL(%q) = %v，應失敗", in, got)
		}
	}
	a, _ := CanonicalURL("https://FEEDS.example.com:443/show.xml#x")
	b, _ := CanonicalURL("https://feeds.example.com/show.xml")
	if a.String() != b.String() {
		t.Errorf("同一個 feed 應得到同一個網址：%s、%s", a, b)
	}
}

func TestFeedSlug(t *testing.T) {
	for in, want := range map[string]string{
		"https://feeds.example.com/show.xml":                        "show",
		"https://feeds.example.com/My_Show.RSS":                     "my-show",
		"https://feeds.example.com/podcasts/the-daily/feed.xml":     "the-daily",
		"https://feeds.example.com/the-daily/rss":                   "the-daily",
		"https://feeds.example.com/the-daily/rss/":                  "the-daily",
		"https://feeds.example.com/a/b/podcast.php":                 "b",
		"https://feeds.example.com/feed/podcast/index.xml":          "example", // 全是泛用名稱 → 主機名稱跳過 feeds 後取 example
		"https://feeds.example.com/feed":                            "example",
		"https://feeds.example.com/":                                "example",
		"https://feeds.example.com":                                 "example",
		"https://www.example.com/rss":                               "example",
		"https://feeds.megaphone.fm/ABC123":                         "abc123",
		"https://example.com/shows/日本語/feed.xml":                    "shows",
		"https://example.com/shows/日本語/rss":                         "shows",
		"https://example.com/日本語":                                   "example",
		"https://example.com/Tech   Talk!!.xml":                     "tech-talk",
		"https://example.com/rss?show=7":                            "example",
		"https://127.0.0.1/atom":                                    "127",
		"https://example.com/" + strings.Repeat("a", 60):            strings.Repeat("a", 40),
		"https://example.com/" + strings.Repeat("ab-", 20) + "tail": strings.Repeat("ab-", 13) + "a",
		"https://feeds.example.com/xml/itunes/podcasts":             "example",
		"https://feeds.example.com/news.atom.xml":                   "news-atom",
	} {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		got := FeedSlug(u)
		if got != want {
			t.Errorf("FeedSlug(%q) = %q，預期 %q", in, got, want)
		}
		if len(got) > 40 || strings.Trim(got, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("FeedSlug(%q) = %q 不是合法的 feedKey", in, got)
		}
	}
}

func TestExternalID(t *testing.T) {
	if got := ExternalID("show", "ep/1/x"); got != "show/ep/1/x" {
		t.Errorf("ExternalID = %q", got)
	}
	for in, want := range map[string][2]string{
		"show/ep-42":       {"show", "ep-42"},
		"show/a/b/c":       {"show", "a/b/c"},
		"show/https://x/y": {"show", "https://x/y"},
	} {
		k, g, ok := SplitExternalID(in)
		if !ok || k != want[0] || g != want[1] {
			t.Errorf("SplitExternalID(%q) = %q, %q, %v", in, k, g, ok)
		}
	}
	for _, in := range []string{"", "show", "show/", "/ep", "/"} {
		if _, _, ok := SplitExternalID(in); ok {
			t.Errorf("SplitExternalID(%q) 應為 false", in)
		}
	}
}

func TestTranscriptFormat(t *testing.T) {
	for _, tt := range []struct {
		t    Transcript
		want string
	}{
		{Transcript{Type: "text/vtt"}, "vtt"},
		{Transcript{Type: "Text/VTT; charset=utf-8"}, "vtt"},
		{Transcript{Type: "application/x-subrip"}, "srt"},
		{Transcript{Type: "application/srt"}, "srt"},
		{Transcript{Type: "application/json"}, "json"},
		{Transcript{Type: "text/html", URL: "https://x.test/a.vtt"}, ""},
		{Transcript{Type: "text/plain", URL: "https://x.test/a.srt"}, ""},
		{Transcript{Type: "application/ttml+xml", URL: "https://x.test/a.ttml"}, ""},
		{Transcript{URL: "https://x.test/a.VTT?token=1"}, "vtt"},
		{Transcript{Type: "application/octet-stream", URL: "https://x.test/a.srt"}, "srt"},
		{Transcript{URL: "https://x.test/a.json"}, "json"},
		{Transcript{URL: "https://x.test/a"}, ""},
		{Transcript{}, ""},
	} {
		if got := TranscriptFormat(tt.t); got != tt.want {
			t.Errorf("TranscriptFormat(%+v) = %q，預期 %q", tt.t, got, tt.want)
		}
	}
}

func TestLang(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "EN": "en", "en-us": "en-US", "en_GB": "en-GB", "zh-hant-tw": "zh-Hant-TW", "zh_Hans": "zh-Hans",
		" ja ": "ja", "es-419": "es-419", "yue": "yue",
		"": "", "e": "", "english": "", "en-": "", "12": "", "en--us": "", "en-u$": "",
	} {
		if got := Lang(in); got != want {
			t.Errorf("Lang(%q) = %q，預期 %q", in, got, want)
		}
	}
}
