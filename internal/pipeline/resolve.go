package pipeline

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/source"
)

// 辨識網址時的上限：抓取 feed 的逾時；內容大小用 netguard.LimitFeed（GRD-R5）。
const (
	resolveProbeTimeout = 30 * time.Second
	feedFetchTimeout    = 15 * time.Second
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
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || platformHost(u.Hostname()) {
		return Resolution{}, ErrUnrecognizedURL
	}
	feed, err := p.fetchFeed(ctx, u.String())
	if err != nil {
		if pe := asPolicyError(err); pe != nil {
			p.log.Warn("辨識網址時請求被防護擋下", "error", err.Error())
			return Resolution{}, pe
		}
		p.log.Debug("網址不是可辨識的 feed", "error", err.Error())
		return Resolution{}, ErrUnrecognizedURL
	}
	return Resolution{Platform: source.PlatformPodcast, Kind: source.KindFeed, URL: u.String(), Title: feed.Title, Episodes: feed.Episodes}, nil
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

type feedInfo struct {
	Title    string
	Episodes []Episode
}

var errNotFeed = errors.New("內容不是 RSS 或 Atom")

// fetchFeed 以對外 HTTP client 抓取網址並判斷內容是不是 RSS／Atom。
// 逾時與大小都有上限（LimitFeed）；超過上限時回傳錯誤，不截斷也不接受部分內容（GRD-R5）。
func (p *Pipeline) fetchFeed(ctx context.Context, rawURL string) (feedInfo, error) {
	if p.http == nil {
		return feedInfo{}, errors.New("沒有設定對外 HTTP client")
	}
	ctx, cancel := context.WithTimeout(ctx, feedFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return feedInfo{}, err
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	resp, err := p.http.Do(req)
	if err != nil {
		return feedInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return feedInfo{}, errors.New("HTTP " + resp.Status)
	}
	body, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		return feedInfo{}, err
	}
	return parseFeed(bytes.NewReader(body))
}

// parseFeed 判斷 r 是不是 RSS 2.0 或 Atom，並盡量取出標題與各集。
func parseFeed(r io.Reader) (feedInfo, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var root xml.StartElement
	for {
		tok, err := dec.Token()
		if err != nil {
			return feedInfo{}, errNotFeed
		}
		if se, ok := tok.(xml.StartElement); ok {
			root = se
			break
		}
	}
	name := strings.ToLower(root.Name.Local)
	switch {
	case name == "rss":
	case name == "feed" && strings.Contains(strings.ToLower(root.Name.Space), "atom"):
	default:
		return feedInfo{}, errNotFeed
	}
	// 根元素確認之後，標題與各集只是盡力而為：內容被截斷或格式不完整時回傳已經讀到的部分。
	info := feedInfo{}
	var path []string
	var cur *Episode
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, strings.ToLower(t.Name.Local))
			if t.Name.Local == "item" || t.Name.Local == "entry" {
				cur = &Episode{}
			}
		case xml.EndElement:
			if (t.Name.Local == "item" || t.Name.Local == "entry") && cur != nil {
				if cur.GUID != "" && len(info.Episodes) < maxFeedEpisodes {
					info.Episodes = append(info.Episodes, *cur)
				}
				cur = nil
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text == "" || len(path) == 0 {
				continue
			}
			leaf := path[len(path)-1]
			switch {
			case cur != nil && (leaf == "guid" || leaf == "id") && cur.GUID == "":
				cur.GUID = text
			case cur != nil && leaf == "title" && cur.Title == "":
				cur.Title = text
			case cur == nil && leaf == "title" && info.Title == "" && len(path) <= 3:
				info.Title = text
			}
		}
	}
	return info, nil
}
