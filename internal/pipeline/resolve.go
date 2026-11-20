package pipeline

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/source"
)

// 辨識網址時的上限：yt-dlp Probe 的逾時，以及 resolve 最多列出幾集（feed 的抓取逾時與大小見 podcast.Fetch）。
const (
	resolveProbeTimeout = 30 * time.Second
	maxFeedEpisodes     = 500
)

// settingsAccountsLink 是 cookie 的設定頁（ING-R9）。
const settingsAccountsLink = "/settings/accounts"

// cookieAvailabilities 是需要 cookie 才能取得的 availability（ING-R9）。
var cookieAvailabilities = []string{"needs_auth", "subscriber_only", "premium_only"}

// Episode 是 Podcast feed 中的一集。
type Episode struct {
	GUID  string
	Title string
	// PublishedAt、DurationS（itunes:duration，秒）、MediaType（enclosure 的 type）沒有或不認得時為零值。
	PublishedAt *time.Time
	DurationS   *float64
	MediaType   string
	// HasTranscript：feed 附了我們讀得懂格式的字幕（<podcast:transcript> 的 vtt、srt、json）。
	HasTranscript bool
}

// Resolution 是網址辨識的結果（POST /api/resolve）。
type Resolution struct {
	Platform   string
	Kind       source.Kind
	ExternalID string
	URL        string
	Title      string
	Part       int
	// Subscribable：頻道與個人空間不能直接新增，要建立訂閱（ING-R5）。
	Subscribable bool
	// Availability 是 YouTube 影片的公開狀態；Probe 失敗或不是 YouTube 影片時為空。
	Availability string
	// RequiresCookie 與 SettingsLink：內容需要 cookie 才能取得（ING-R9）。
	RequiresCookie bool
	SettingsLink   string
	// Episodes 是 Podcast feed 列出的各集。
	Episodes []Episode
}

// Resolve 辨識網址指向什麼內容，不建立任何影音（ING-R1、ING-R5、ING-R9）。
//
// YouTube 與 Bilibili 只看網址；YouTube 影片另外以 yt-dlp 盡力取得 availability（失敗不影響辨識結果）。
// 其他 http(s) 網址透過對外 HTTP client 實際抓取，內容是 RSS／Atom 才是 Podcast feed。
// 無法辨識時回傳 ErrUnrecognizedURL；連線被防護擋下（內部網路、轉址太多、回應太大）時回傳 *PolicyError。
func (p *Pipeline) Resolve(ctx context.Context, raw string) (Resolution, error) {
	raw = strings.TrimSpace(raw)
	if t, ok := source.Recognize(raw); ok {
		res := Resolution{Platform: t.Platform, Kind: t.Kind, ExternalID: t.ExternalID, URL: t.URL, Part: t.Part,
			Subscribable: t.Kind == source.KindChannel}
		if t.Platform == source.PlatformYouTube && t.Kind == source.KindVideo && p.yt != nil {
			p.flagCookie(ctx, &res)
		}
		// TODO(ING-R9)：Bilibili 的 AI 字幕需要 cookie，等 Bilibili 的 Probe 實作後在這裡標示。
		return res, nil
	}
	u, err := podcast.CanonicalURL(raw)
	if err != nil || platformHost(u.Hostname()) {
		return Resolution{}, ErrUnrecognizedURL
	}
	feed, err := p.fetchFeed(ctx, u.String())
	if err != nil {
		return Resolution{}, p.feedError(err)
	}
	// 只列前 maxFeedEpisodes 集；要新增更舊的集，直接以 guid 指定（ING-R6）。
	eps := feed.Episodes[:min(len(feed.Episodes), maxFeedEpisodes)]
	res := Resolution{Platform: source.PlatformPodcast, Kind: source.KindFeed, URL: u.String(), Title: feed.Title}
	for _, e := range eps {
		res.Episodes = append(res.Episodes, episodeInfo(e))
	}
	return res, nil
}

// episodeInfo 把 feed 的一集轉成辨識結果中的形式。HasTranscript 只算我們讀得懂的格式（vtt、srt、json）。
func episodeInfo(e podcast.Episode) Episode {
	out := Episode{GUID: e.GUID, Title: e.Title, PublishedAt: e.PublishedAt, DurationS: e.DurationS}
	if e.Enclosure != nil {
		out.MediaType = e.Enclosure.Type
	}
	for _, t := range e.Transcripts {
		if podcast.TranscriptFormat(t) != "" {
			out.HasTranscript = true
			break
		}
	}
	return out
}

// flagCookie 以 Probe 取得影片的 availability；需要 cookie 時標示 requires_cookie 與設定頁連結。
func (p *Pipeline) flagCookie(ctx context.Context, res *Resolution) {
	ctx, cancel := context.WithTimeout(ctx, resolveProbeTimeout)
	defer cancel()
	info, err := p.yt.Probe(ctx, res.ExternalID)
	if err != nil {
		p.log.Debug("辨識網址時 Probe 失敗，略過 availability", "error", err.Error())
		return
	}
	res.Availability = info.Availability
	if slices.Contains(cookieAvailabilities, info.Availability) {
		res.RequiresCookie, res.SettingsLink = true, settingsAccountsLink
	}
}

// platformHost 回報主機是不是 YouTube 或 Bilibili：這些平台只看網址，不抓取內容。
func platformHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".") // FQDN 尾端的 "." 是同一個主機
	for _, d := range []string{"youtube.com", "youtu.be", "bilibili.com", "b23.tv"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// fetchFeed 以對外 HTTP client 抓取網址並判斷內容是不是 RSS／Atom（podcast.Fetch）。
// 逾時與大小都有上限（LimitFeed）；超過上限時回傳錯誤，不截斷也不接受部分內容（GRD-R5）。
func (p *Pipeline) fetchFeed(ctx context.Context, rawURL string) (*podcast.Feed, error) {
	return podcast.Fetch(ctx, p.http, rawURL)
}

// feedError 把抓取 feed 的錯誤對應成使用者看得到的錯誤：防護擋下（內部網路、轉址太多、回應太大）是
// *PolicyError；其他（連不上、非 2xx、不是 feed）都是 ErrUnrecognizedURL，原因只記在 log。
func (p *Pipeline) feedError(err error) error {
	if pe := asPolicyError(err); pe != nil {
		p.log.Warn("抓取 feed 時請求被防護擋下", "error", err.Error())
		return pe
	}
	p.log.Debug("網址不是可辨識的 feed", "error", err.Error())
	return ErrUnrecognizedURL
}
