package pipeline

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/source"
)

// rtFunc 讓測試用函式充當 http.RoundTripper，不經過任何真實網路。
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func feedClient(status int, body string, hits *[]string) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		*hits = append(*hits, r.URL.String())
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

const rssBody = `<?xml version="1.0"?><rss version="2.0"><channel><title>The Show</title>
<item><title>Episode 42</title><guid>ep-42</guid></item><item><title>Episode 41</title><guid>ep-41</guid></item></channel></rss>`

func TestResolveByURLOnly(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	tests := []struct {
		url, platform string
		kind          source.Kind
		subscribable  bool
	}{
		{"https://www.youtube.com/watch?v=abc123xxxxx", "youtube", source.KindVideo, false},
		{"https://youtu.be/abc123xxxxx", "youtube", source.KindVideo, false},
		{"https://www.youtube.com/shorts/abc123xxxxx", "youtube", source.KindVideo, false},
		{"https://www.youtube.com/playlist?list=PL123", "youtube", source.KindPlaylist, false},
		{"https://www.youtube.com/@somechannel", "youtube", source.KindChannel, true},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=2", "bilibili", source.KindVideo, false},
		{"https://space.bilibili.com/12345", "bilibili", source.KindChannel, true},
	}
	for _, tt := range tests {
		res, err := r.p.Resolve(ctx, tt.url)
		if err != nil || res.Platform != tt.platform || res.Kind != tt.kind || res.Subscribable != tt.subscribable {
			t.Errorf("%s：%+v, %v", tt.url, res, err)
		}
	}
	if res, _ := r.p.Resolve(ctx, "https://www.bilibili.com/video/BV1xx411c7mD?p=2"); res.Part != 2 {
		t.Errorf("part = %d", res.Part)
	}
}

func TestResolveFlagsCookieFromAvailability(t *testing.T) {
	for _, tt := range []struct {
		availability string
		cookie       bool
	}{{"needs_auth", true}, {"subscriber_only", true}, {"premium_only", true}, {"public", false}, {"", false}} {
		r := newRig(t)
		r.yt.info.Availability = tt.availability
		res, err := r.p.Resolve(context.Background(), watch)
		if err != nil || res.RequiresCookie != tt.cookie || (tt.cookie && res.SettingsLink != "/settings/accounts") || res.Availability != tt.availability {
			t.Errorf("%q：%+v, %v", tt.availability, res, err)
		}
	}
}

func TestResolveProbeFailureStillRecognises(t *testing.T) {
	r := newRig(t)
	r.yt.probeErr = errors.New("boom")
	res, err := r.p.Resolve(context.Background(), watch)
	if err != nil || res.Platform != "youtube" || res.RequiresCookie || res.Availability != "" {
		t.Fatalf("%+v, %v", res, err)
	}
}

func TestResolveFetchesOtherURLs(t *testing.T) {
	ctx := context.Background()
	var hits []string
	r := newRig(t)
	r.p.http = feedClient(200, rssBody, &hits)
	res, err := r.p.Resolve(ctx, "https://feeds.example.com/show.xml")
	if err != nil || res.Platform != "podcast" || res.Kind != source.KindFeed || res.Title != "The Show" || len(res.Episodes) != 2 || res.Episodes[0].GUID != "ep-42" {
		t.Fatalf("%+v, %v", res, err)
	}
	atom := `<feed xmlns="http://www.w3.org/2005/Atom"><title>A</title><entry><id>1</id><title>E</title></entry></feed>`
	r.p.http = feedClient(200, atom, &hits)
	if res, err := r.p.Resolve(ctx, "https://example.org/atom"); err != nil || res.Kind != source.KindFeed {
		t.Errorf("atom：%+v, %v", res, err)
	}
	for name, c := range map[string]*http.Client{
		"html":    feedClient(200, "<html><body>hi</body></html>", &hits),
		"404":     feedClient(404, rssBody, &hits),
		"empty":   feedClient(200, "", &hits),
		"foreign": feedClient(200, `<feed xmlns="urn:other"/>`, &hits),
	} {
		r.p.http = c
		if _, err := r.p.Resolve(ctx, "https://example.com/some/page"); !errors.Is(err, ErrUnrecognizedURL) {
			t.Errorf("%s：%v", name, err)
		}
	}
	// 沒有 HTTP client、不是 http(s)、或是 YouTube／Bilibili 的其他頁面：不抓取，直接無法辨識。
	hits = nil
	r.p.http = feedClient(200, rssBody, &hits)
	for _, u := range []string{"ftp://example.com/feed", "not a url", "https://www.youtube.com/feed/trending", "https://www.bilibili.com/anime", "https://www.youtube.com/watch?v=abc123xxxx"} {
		if _, err := r.p.Resolve(ctx, u); !errors.Is(err, ErrUnrecognizedURL) {
			t.Errorf("%s：%v", u, err)
		}
	}
	if len(hits) != 0 {
		t.Errorf("不該抓取：%v", hits)
	}
	r.p.http = nil
	if _, err := r.p.Resolve(ctx, "https://example.com/x"); !errors.Is(err, ErrUnrecognizedURL) {
		t.Errorf("沒有 client：%v", err)
	}
}

// 主機名稱尾端的 "." 是同一個主機（FQDN），不能因此繞過平台判斷而去抓取。
func TestPlatformHostIgnoresTrailingDot(t *testing.T) {
	for host, want := range map[string]bool{
		"youtube.com": true, "youtube.com.": true, "WWW.YouTube.com.": true, "youtu.be.": true,
		"m.bilibili.com.": true, "b23.tv.": true,
		"example.com": false, "example.com.": false, "notyoutube.com.": false,
	} {
		if got := platformHost(host); got != want {
			t.Errorf("platformHost(%q) = %v，預期 %v", host, got, want)
		}
	}
	var hits []string
	r := newRig(t)
	r.p.http = feedClient(200, rssBody, &hits)
	if _, err := r.p.Resolve(context.Background(), "https://www.youtube.com./feed/trending"); !errors.Is(err, ErrUnrecognizedURL) {
		t.Errorf("err = %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("不該抓取：%v", hits)
	}
}

func TestParseFeedTruncatedStillRecognised(t *testing.T) {
	info, err := parseFeed(strings.NewReader(rssBody[:120]))
	if err != nil {
		t.Fatalf("被截斷的 feed 仍要能辨識：%v", err)
	}
	if info.Title != "The Show" {
		t.Errorf("title = %q", info.Title)
	}
}
