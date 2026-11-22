package podcast

import (
	"strings"
	"testing"
)

// 解析不可信的 RSS／Atom 與 URL、時長字串，任何輸入都不得 panic。
func FuzzParse(f *testing.F) {
	f.Add(`<rss version="2.0"><channel><title>t</title><item><guid>g</guid><enclosure url="http://e/x.mp3" type="audio/mpeg"/></item></channel></rss>`)
	f.Add(`<feed xmlns="http://www.w3.org/2005/Atom"><title type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">x<b>y</b></div></title><entry><id>1</id></entry></feed>`)
	f.Add(`<?xml version="1.0" encoding="iso-8859-1"?><rss><channel><item><itunes:duration>1:02:03</itunes:duration></item></channel></rss>`)
	f.Add(strings.Repeat("<a>", 200))
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = Parse(strings.NewReader(s))
	})
}

func FuzzParseDuration(f *testing.F) {
	for _, s := range []string{"", "90", "1:02", "1:02:03", "-1", "1e400", "::", "99999999999999999999", "1.5"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if d, ok := ParseDuration(s); ok && !(d >= 0) {
			t.Fatalf("ParseDuration(%q) = %v", s, d)
		}
	})
}

func FuzzCanonicalURL(f *testing.F) {
	for _, s := range []string{"https://Example.com/feed?x=1#f", "http://[::1]:80/", "feed://a.b/c", "", "https://user:pw@host/%zz", "//x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if u, err := CanonicalURL(s); err == nil {
			_ = FeedSlug(u)
		}
	})
}
