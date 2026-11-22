package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

const podcastFeedXML = `<?xml version="1.0"?><rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0"><channel><title>The Show</title><language>en</language>
<item><guid>ep-41</guid><title>Episode 41</title><enclosure url="https://media.example.com/ep41.mp3" type="audio/mpeg" length="1"/></item>
<item><guid>ep-42</guid><title>Episode 42</title><pubDate>Wed, 01 Oct 2025 08:00:00 +0000</pubDate><itunes:duration>30:00</itunes:duration>
<enclosure url="https://media.example.com/ep42.mp3" type="audio/mpeg" length="1"/><podcast:transcript url="https://media.example.com/ep42.vtt" type="text/vtt"/></item>
</channel></rss>`

type feedRT struct{}

func (feedRT) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Status: "OK", Body: io.NopCloser(strings.NewReader(podcastFeedXML)), Header: http.Header{}}, nil
}

func podcastRig(t *testing.T, check func(context.Context, string) error) *itemsRig {
	return newItemsRig(t, func(d *pipeline.Deps) {
		d.HTTP = &http.Client{Transport: feedRT{}}
		d.CheckURL = check
	})
}

// resolve 回應的單集帶發佈時間、長度、字幕與音檔類型（ING-R1）。
func TestResolveEpisodeFields(t *testing.T) {
	r := podcastRig(t, nil)
	rec := r.owner("POST", "/api/resolve", `{"url":"https://feeds.example.com/show.xml"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	got := decode[struct {
		Platform string    `json:"platform"`
		Kind     string    `json:"kind"`
		Episodes []Episode `json:"episodes"`
	}](t, rec)
	if got.Platform != "podcast" || got.Kind != "feed" || len(got.Episodes) != 2 {
		t.Fatalf("got = %+v", got)
	}
	e := got.Episodes[1]
	if e.GUID != "ep-42" || e.PublishedAt == nil || e.PublishedAt.Unix() != 1759305600 || e.DurationS == nil || *e.DurationS != 1800 || !e.HasTranscript || e.MediaType != "audio/mpeg" {
		t.Errorf("ep-42 = %+v", e)
	}
	if first := got.Episodes[0]; first.HasTranscript || first.PublishedAt != nil || first.DurationS != nil {
		t.Errorf("ep-41 = %+v", first)
	}
	// has_transcript 一律出現在 JSON 裡；沒有的欄位省略。
	if body := rec.Body.String(); !strings.Contains(body, `"has_transcript":false`) {
		t.Errorf("body = %s", body)
	}
}

// ING-R6：新增單集回 201 與 pod: 代號；沒有 episodes 回 422 invalid_input。
func TestCreatePodcastEpisode(t *testing.T) {
	r := podcastRig(t, func(context.Context, string) error { return nil })
	rec := r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml","episodes":["ep-42"]}`)
	if rec.Code != 201 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	got := decode[struct {
		Items []IngestedItem `json:"items"`
	}](t, rec)
	if len(got.Items) != 1 || got.Items[0].Ref != "pod:show/ep-42" || got.Items[0].Platform != "podcast" || got.Items[0].ChannelID == "" ||
		got.Items[0].URL != "https://feeds.example.com/show.xml" || got.Items[0].AlreadyInLibrary {
		t.Fatalf("items = %+v", got.Items)
	}
	// 再新增一次：200 與 already_in_library。
	rec = r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml","episodes":["ep-42"]}`)
	if rec.Code != 200 {
		t.Fatalf("第二次：status = %d, body = %s", rec.Code, rec.Body)
	}

	rec = r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml"}`)
	if e := decode[Error](t, rec); rec.Code != 422 || e.Code != CodeInvalidInput || e.Message != "這是 Podcast feed，請在 episodes 指定要新增的集。" {
		t.Errorf("沒有 episodes：status = %d, %+v", rec.Code, e)
	}
	rec = r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml","episodes":["nope"]}`)
	if e := decode[Error](t, rec); rec.Code != 422 || e.Code != CodeInvalidInput || e.Message != "feed 中找不到這一集：nope。" {
		t.Errorf("找不到：status = %d, %+v", rec.Code, e)
	}
	rec = r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml","episodes":["ep-41"],"parts":[1]}`)
	if e := decode[Error](t, rec); rec.Code != 422 || e.Message != "Podcast 沒有分P 可以選擇。" {
		t.Errorf("parts：status = %d, %+v", rec.Code, e)
	}
	rec = r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml","episodes":["ep-41"],"profile":"fast"}`)
	if e := decode[Error](t, rec); rec.Code != 422 || e.Code != CodeInvalidInput || len(e.Errors) != 1 || e.Errors[0].Path != "profile" {
		t.Errorf("不存在的 profile：status = %d, %+v", rec.Code, e)
	}
}

// GRD-R2：音檔位址預檢被擋時新增回 422 blocked_address，不建立任何影音。
func TestCreatePodcastEpisodeBlockedAddress(t *testing.T) {
	r := podcastRig(t, func(context.Context, string) error {
		return fmt.Errorf("%w: 192.168.1.1", netguard.ErrBlockedAddress)
	})
	rec := r.owner("POST", "/api/items", `{"url":"https://feeds.example.com/show.xml","episodes":["ep-42"]}`)
	if e := decode[Error](t, rec); rec.Code != http.StatusUnprocessableEntity || e.Code != CodeBlockedAddress || e.Message != "這個網址指向內部網路，已封鎖。" {
		t.Fatalf("status = %d, error = %+v", rec.Code, e)
	}
	rec = r.owner("GET", "/api/items", "")
	if got := decode[struct {
		Items []Item `json:"items"`
	}](t, rec); len(got.Items) != 0 {
		t.Errorf("被擋時不該建立影音：%+v", got.Items)
	}
}
