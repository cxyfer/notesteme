package subscription

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// candidate 是探索到的一個條目（尚未決定）。
type candidate struct {
	Platform   string
	ExternalID string
	Part       int
	Title      string
	// PublishedAt 只有 RSS 與 feed 提供；flat-playlist 通常沒有。
	PublishedAt *time.Time
	DurationS   *int
	LiveStatus  string
	Shorts      bool
	// Position 是在清單中的位置（0 起算，頻道與 feed 新的在前，播放清單與合集依清單順序）。
	Position int
	// IngestURL、Episode 是交給 ingest 的網址與 Podcast 的 guid。
	IngestURL string
	Episode   string

	// 以下是評估時的狀態。fresh：第一次出現（基準清單之後才出現的才是新內容）；probed：已經補齊過；
	// seenAt：先前記錄的第一次出現時間；wasDeferred：先前因 backfill_per_check 延後（已在回補上限內）。
	fresh       bool
	probed      bool
	seenAt      *time.Time
	wasDeferred bool
}

func (c candidate) key() string { return fmt.Sprintf("%s|%s|%d", c.Platform, c.ExternalID, c.Part) }

// listing 是一次探索的結果。
type listing struct {
	Title string
	// ExternalID 是解析後的來源 ID（@handle 換成頻道 ID 之後的值）。
	ExternalID string
	Items      []candidate
}

// deferredError 是額度用完（SUB-R10）：這次檢查停在這裡，At 是額度恢復的時間。
type deferredError struct{ At time.Time }

func (e *deferredError) Error() string { return "rate limited" }

var (
	videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	bvidRe    = regexp.MustCompile(`^BV[0-9A-Za-z]{3,20}$`)
)

// rateKey 回傳平台的 bucket 與每分鐘的數量。
func (s *Service) rateKey(platform, feedURL string) (string, int) {
	switch platform {
	case PlatformYouTube:
		return "youtube", s.limit.RateLimits.YouTube
	case PlatformBilibili:
		return "bilibili", s.limit.RateLimits.Bilibili
	}
	host := feedURL
	if u, err := url.Parse(feedURL); err == nil {
		host = strings.ToLower(u.Hostname())
	}
	return "feed:" + host, s.limit.RateLimits.FeedHost
}

// take 取一顆額度；沒有額度時回傳 *deferredError。
func (s *Service) take(platform, feedURL string) error {
	key, n := s.rateKey(platform, feedURL)
	if ok, at := s.rate.Take(key, n); !ok {
		return &deferredError{At: at}
	}
	return nil
}

// throttled 在平台回 429／412 時清空並暫停 bucket，回傳恢復的時間（SUB-R10）。
func (s *Service) throttled(platform, feedURL string) time.Time {
	key, n := s.rateKey(platform, feedURL)
	return s.rate.Pause(key, n)
}

// discover 依訂閱的種類列出條目（SUB-R2）。listBackfill 為 true 時，頻道另外以 flat-playlist 取得 /videos 的清單。
func (s *Service) discover(ctx context.Context, sub store.Subscription, listBackfill bool, backfillLimit int) (listing, error) {
	switch sub.Kind {
	case KindChannel:
		return s.discoverChannel(ctx, sub, listBackfill, backfillLimit)
	case KindPlaylist:
		if err := s.take(PlatformYouTube, ""); err != nil {
			return listing{}, err
		}
		fl, err := s.list.run(ctx, extractorsYouTube, MaxEntries, "https://www.youtube.com/playlist?list="+url.QueryEscape(sub.ExternalID))
		if err != nil {
			return listing{}, err
		}
		return listing{Title: CleanText(fl.Title), ExternalID: sub.ExternalID, Items: flatCandidates(fl, PlatformYouTube)}, nil
	case KindCollection:
		mid, sid, _ := strings.Cut(sub.ExternalID, ":")
		if err := s.take(PlatformBilibili, ""); err != nil {
			return listing{}, err
		}
		fl, err := s.list.run(ctx, extractorsBilibili, MaxEntries, LegacyCollectionURL(mid, sid))
		if err != nil {
			return listing{}, err
		}
		return listing{Title: CleanText(fl.Title), ExternalID: sub.ExternalID, Items: flatCandidates(fl, PlatformBilibili)}, nil
	case KindFeed:
		return s.discoverFeed(ctx, sub)
	}
	return listing{}, &checkError{Class: errclass.ClassPermanent, Message: msgUnsupportedURL}
}

// discoverChannel 讀頻道的 RSS（最新約 15 部）；需要回補時再以 flat-playlist 列出 /videos。
// @handle 先以 yt-dlp 換成頻道 ID（第一次檢查，SUB-R1）。
func (s *Service) discoverChannel(ctx context.Context, sub store.Subscription, listBackfill bool, backfillLimit int) (listing, error) {
	out := listing{ExternalID: sub.ExternalID}
	if strings.HasPrefix(out.ExternalID, "@") {
		if err := s.take(PlatformYouTube, ""); err != nil {
			return out, err
		}
		fl, err := s.list.run(ctx, extractorsYouTube, 1, "https://www.youtube.com/"+out.ExternalID)
		if err != nil {
			return out, err
		}
		if !ValidChannelID(fl.ChannelID) {
			return out, &checkError{Class: errclass.ClassPermanent, Message: msgListGone}
		}
		out.ExternalID = fl.ChannelID
		out.Title = CleanText(fl.Channel)
	}
	if err := s.take(PlatformYouTube, ""); err != nil {
		return out, err
	}
	feed, err := fetchFeed(ctx, s.d.HTTP, "https://www.youtube.com/feeds/videos.xml?channel_id="+url.QueryEscape(out.ExternalID))
	if err != nil {
		return out, err
	}
	if t := CleanText(feed.Title); t != "" {
		out.Title = t
	}
	seen := map[string]bool{}
	var rss []candidate
	for _, ep := range feed.Episodes {
		c, ok := atomCandidate(ep)
		if !ok || seen[c.ExternalID] {
			continue
		}
		seen[c.ExternalID] = true
		rss = append(rss, c)
	}
	if !listBackfill {
		for i := range rss {
			rss[i].Position = i
		}
		out.Items = rss
		return out, nil
	}
	if err := s.take(PlatformYouTube, ""); err != nil {
		return out, err
	}
	fl, err := s.list.run(ctx, extractorsYouTube, backfillLimit, ChannelVideosURL(out.ExternalID))
	if err != nil {
		return out, err
	}
	// 依 /videos 的順序（新的在前）；只在 RSS 出現的接在後面（依 RSS 的順序）。RSS 的發布時間補到同一部影片上。
	byID := map[string]candidate{}
	for _, c := range rss {
		byID[c.ExternalID] = c
	}
	used := map[string]bool{}
	for _, c := range flatCandidates(fl, PlatformYouTube) {
		if used[c.ExternalID] {
			continue
		}
		used[c.ExternalID] = true
		if r, ok := byID[c.ExternalID]; ok {
			c.PublishedAt, c.Shorts = r.PublishedAt, r.Shorts
			if c.Title == "" {
				c.Title = r.Title
			}
		}
		c.Position = len(out.Items)
		out.Items = append(out.Items, c)
	}
	for _, c := range rss {
		if !used[c.ExternalID] {
			c.Position = len(out.Items)
			out.Items = append(out.Items, c)
		}
	}
	return out, nil
}

// atomCandidate 把 YouTube RSS 的一筆轉成條目：影片 ID 取自 <id>（yt:video:<ID>）或連結；
// 連結是 /shorts/<ID> 的是 Shorts（SUB 待決問題 S6）。連結不是 http／https 的略過（SUB-R11）。
func atomCandidate(ep podcast.Episode) (candidate, bool) {
	id := strings.TrimPrefix(ep.GUID, "yt:video:")
	link := strings.TrimSpace(ep.Link)
	shorts := false
	if link != "" {
		if !safeLink(link) {
			return candidate{}, false
		}
		if u, err := url.Parse(link); err == nil {
			if rest, ok := strings.CutPrefix(u.Path, "/shorts/"); ok {
				shorts = true
				if !videoIDRe.MatchString(id) {
					id = strings.Trim(rest, "/")
				}
			} else if v := u.Query().Get("v"); !videoIDRe.MatchString(id) {
				id = v
			}
		}
	}
	if !videoIDRe.MatchString(id) {
		return candidate{}, false
	}
	return candidate{
		Platform: PlatformYouTube, ExternalID: id, Title: CleanText(ep.Title), PublishedAt: ep.PublishedAt,
		Shorts: shorts, IngestURL: "https://www.youtube.com/watch?v=" + id,
	}, true
}

// flatCandidates 把 flat-playlist 的條目轉成候選（不可信的輸入：ID 不合法、網址不是 http／https 的略過）。
func flatCandidates(fl *flatList, platform string) []candidate {
	var out []candidate
	seen := map[string]bool{}
	for _, e := range fl.Entries {
		if e == nil || len(out) >= MaxEntries {
			continue
		}
		if e.URL != "" && !safeLink(e.URL) {
			continue
		}
		c := candidate{Platform: platform, ExternalID: e.ID, Title: CleanText(e.Title), LiveStatus: e.LiveStatus, Position: len(out)}
		switch platform {
		case PlatformYouTube:
			if !videoIDRe.MatchString(e.ID) {
				continue
			}
			c.IngestURL = "https://www.youtube.com/watch?v=" + e.ID
			if u, err := url.Parse(e.URL); err == nil && strings.HasPrefix(u.Path, "/shorts/") {
				c.Shorts = true
			}
		case PlatformBilibili:
			if !bvidRe.MatchString(e.ID) {
				continue
			}
			// 合集的條目是一個 BV 號，以第 1 個分P 識別；建立時依 ING 的規則展開全部分P（SUB-R5）。
			c.Part = 1
			c.IngestURL = "https://www.bilibili.com/video/" + e.ID
		}
		if seen[c.ExternalID] {
			continue
		}
		seen[c.ExternalID] = true
		if e.Duration != nil && *e.Duration >= 0 && *e.Duration < 1e7 {
			d := int(*e.Duration + 0.5)
			c.DurationS = &d
		}
		if e.Timestamp != nil && *e.Timestamp > 0 {
			t := time.Unix(*e.Timestamp, 0).UTC()
			c.PublishedAt = &t
		}
		out = append(out, c)
	}
	return out
}

// discoverFeed 讀 Podcast feed；每集以 <feedKey>/<guid> 識別（ING-R6），feedKey 在這裡取得（必要時建立頻道）。
func (s *Service) discoverFeed(ctx context.Context, sub store.Subscription) (listing, error) {
	if err := s.take(PlatformPodcast, sub.URL); err != nil {
		return listing{}, err
	}
	feed, err := fetchFeed(ctx, s.d.HTTP, sub.URL)
	if err != nil {
		return listing{}, err
	}
	out := listing{Title: CleanText(feed.Title), ExternalID: sub.ExternalID}
	u, err := url.Parse(sub.URL)
	if err != nil {
		return out, &checkError{Class: errclass.ClassPermanent, Message: msgUnsupportedURL}
	}
	var key string
	if err := s.d.Store.Tx(ctx, func(r *store.Repo) error {
		var err error
		key, err = r.PodcastFeedKey(ctx, sub.URL, podcast.FeedSlug(u), out.Title)
		return err
	}); err != nil {
		return out, err
	}
	for _, ep := range feed.Episodes {
		if ep.Enclosure == nil || !safeLink(ep.Enclosure.URL) || (ep.Link != "" && !safeLink(ep.Link)) {
			continue // SUB-R11：網址與 enclosure 不是 http／https 的條目略過
		}
		out.Items = append(out.Items, candidate{
			Platform: PlatformPodcast, ExternalID: podcast.ExternalID(key, ep.GUID), Title: CleanText(ep.Title),
			PublishedAt: ep.PublishedAt, DurationS: durationOf(ep.DurationS), Position: len(out.Items),
			IngestURL: sub.URL, Episode: ep.GUID,
		})
	}
	return out, nil
}

func durationOf(f *float64) *int {
	if f == nil || *f < 0 || *f > 1e7 {
		return nil
	}
	d := int(*f + 0.5)
	return &d
}

// entryRef 是條目的短代號（同影音，source.Ref）。
func entryRef(platform, externalID string, part int) string {
	return source.Ref(platform, externalID, part)
}

// TakePlatform 取平台（youtube、bilibili）的 rate limit 額度（SUB-R10）；重新 Probe 字幕軌（SRC-R19）與訂閱的探索共用同一個 bucket。
// 沒有額度時 ok 為 false，retryAt 是下一顆額度出現的時間。
func (s *Service) TakePlatform(platform string) (ok bool, retryAt time.Time) {
	key, n := s.rateKey(platform, "")
	return s.rate.Take(key, n)
}
