package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// 同一個網址得到同一個 key；同 slug 不同網址得到 -2、-3；頻道只建立一次。
func TestPodcastFeedKey(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()

	a, err := w.PodcastFeedKey(ctx, "https://a.example.com/show.xml", "show", "Show A")
	if err != nil || a != "show" {
		t.Fatalf("第一個 feed：%q, %v", a, err)
	}
	again, err := w.PodcastFeedKey(ctx, "https://a.example.com/show.xml", "show", "Show A")
	if err != nil || again != "show" {
		t.Fatalf("同一個網址：%q, %v", again, err)
	}
	// 同網址但 slug 算出不同的值（例如演算法之後改了）：仍沿用既有的 key。
	if got, err := w.PodcastFeedKey(ctx, "https://a.example.com/show.xml", "other", "Show A"); err != nil || got != "show" {
		t.Fatalf("沿用既有的 key：%q, %v", got, err)
	}
	b, err := w.PodcastFeedKey(ctx, "https://b.example.com/show.xml", "show", "Show B")
	if err != nil || b != "show-2" {
		t.Fatalf("同 slug 不同網址：%q, %v", b, err)
	}
	c, err := w.PodcastFeedKey(ctx, "https://c.example.com/show.xml", "show", "Show C")
	if err != nil || c != "show-3" {
		t.Fatalf("第三個：%q, %v", c, err)
	}
	// http 與 https 是不同的 feed。
	if d, err := w.PodcastFeedKey(ctx, "http://a.example.com/show.xml", "show", ""); err != nil || d != "show-4" {
		t.Fatalf("不同 scheme：%q, %v", d, err)
	}

	ch, err := w.ChannelByKey(ctx, "podcast", "show-2")
	if err != nil || ch.URL != "https://b.example.com/show.xml" || ch.Title != "Show B" || ch.Platform != "podcast" {
		t.Fatalf("channel = %+v, %v", ch, err)
	}
	if _, err := w.ChannelByKey(ctx, "podcast", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
	// 其他平台的頻道不影響 podcast 的 key。
	if _, err := w.UpsertChannel(ctx, Channel{Platform: "youtube", ExternalID: "tech", Title: "T"}); err != nil {
		t.Fatal(err)
	}
	if k, err := w.PodcastFeedKey(ctx, "https://x.example.com/tech.xml", "tech", "Tech"); err != nil || k != "tech" {
		t.Errorf("tech：%q, %v", k, err)
	}
	if k, err := w.PodcastFeedKey(ctx, "https://x.example.com/z", "", ""); err != nil || k != "feed" {
		t.Errorf("空 slug：%q, %v", k, err)
	}
}

func TestPodcastFeedKeyExhausted(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	for i := 1; i <= maxFeedKeySuffix; i++ {
		if _, err := w.PodcastFeedKey(ctx, fmt.Sprintf("https://h%d.example.com/show.xml", i), "show", ""); err != nil {
			t.Fatalf("第 %d 個：%v", i, err)
		}
	}
	if k, err := w.PodcastFeedKey(ctx, "https://h100.example.com/show.xml", "show", ""); err != nil || k != "show-100" {
		t.Fatalf("已存在的仍可沿用：%q, %v", k, err)
	}
	if k, err := w.PodcastFeedKey(ctx, "https://h101.example.com/show.xml", "show", ""); err == nil {
		t.Fatalf("超過上限應失敗：%q", k)
	}
}

func TestSetItemChannel(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	it, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "podcast", ExternalID: "show/ep-1", URL: "https://a.example.com/show.xml"})
	if err != nil {
		t.Fatal(err)
	}
	chID, err := w.UpsertChannel(ctx, Channel{Platform: "podcast", ExternalID: "show", URL: "https://a.example.com/show.xml"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetItemChannel(ctx, it.ID, chID); err != nil {
		t.Fatal(err)
	}
	if got, err := w.ItemByID(ctx, it.ID); err != nil || got.ChannelID != chID {
		t.Errorf("item = %+v, %v", got, err)
	}
	if err := w.SetItemChannel(ctx, "missing", chID); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的影音：%v", err)
	}
}
