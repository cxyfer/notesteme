// Package subscription 是訂閱（SUB，ADR-0021 第 2 點）：建立時只依網址的格式辨識來源，
// 探索（discover）是排程工作 subscription.check，在排程器內同步執行，建立的影音交給既有的 ingest。
//
// feed 與 yt-dlp 的輸出一律當作不可信的輸入（SUB-R11）：HTTP 請求走 netguard、XML 含 <!DOCTYPE> 整份拒絕、
// 巢狀深度與條目數有上限、標題先清理再使用。
package subscription

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/source"
)

// 訂閱的種類（kind）。
const (
	KindChannel    = "channel"
	KindPlaylist   = "playlist"
	KindCollection = "collection"
	KindFeed       = "feed"
)

// 訂閱的平台；Podcast feed 沿用影音的平台名稱 podcast。
const (
	PlatformYouTube  = source.PlatformYouTube
	PlatformBilibili = source.PlatformBilibili
	PlatformPodcast  = source.PlatformPodcast
)

// 建立訂閱時網址的錯誤碼與訊息（SUB-R1，逐字）。
const (
	CodeUnsupportedURL  = "unsupported_url"
	CodeNotACollection  = "not_a_collection"
	CodeDuplicate       = "duplicate_subscription"
	msgUnsupportedURL   = "無法辨識這個網址。訂閱支援 YouTube 頻道與播放清單、Bilibili 合集與 Podcast RSS。"
	msgNotACollection   = "這是單一影片，請直接新增，不需要訂閱。"
	msgDuplicate        = "已經訂閱了這個來源。"
	msgURLRequired      = "請提供要訂閱的網址。"
	msgProfileNotExists = "處理方案不存在。"
)

// Source 是由網址辨識出的訂閱來源。
type Source struct {
	Kind       string
	Platform   string
	ExternalID string
	// URL 是正規化之後的網址（存進 subscriptions.url）。
	URL string
}

// URLError 是網址無法訂閱（422），Code 是 CodeUnsupportedURL 或 CodeNotACollection。
type URLError struct{ Code, Message string }

func (e *URLError) Error() string { return e.Message }

var (
	channelIDRe  = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)
	handleRe     = regexp.MustCompile(`^@[\p{L}\p{N}._-]{1,100}$`)
	playlistIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,128}$`)
	digitsRe     = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// ValidChannelID 回報 id 是否是 YouTube 頻道 ID（UC 開頭的 24 字元）。
func ValidChannelID(id string) bool { return channelIDRe.MatchString(id) }

// platformHost 回報主機是不是 YouTube 或 Bilibili（含子網域與短網址）：這些主機只接受已知的格式（SUB-R1）。
func platformHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be", "b23.tv", "bilibili.com":
		return true
	}
	return strings.HasSuffix(host, ".bilibili.com") || strings.HasSuffix(host, ".youtube.com")
}

// Recognize 依網址的格式辨識訂閱來源，不連外（SUB-R1）。
func Recognize(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
	}
	host := strings.ToLower(u.Hostname())
	if !platformHost(host) {
		feed, err := podcast.CanonicalURL(raw)
		if err != nil {
			return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
		}
		s := feed.String()
		// 正規化之後必須能再辨識成同一個網址（例如 "http://0::0" 會被正規化成無法解析的主機），否則不接受。
		if again, err := podcast.CanonicalURL(s); err != nil || again.String() != s {
			return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
		}
		return Source{Kind: KindFeed, Platform: PlatformPodcast, ExternalID: s, URL: s}, nil
	}
	// 單一影片（含 Bilibili 的分P）：直接新增，不需要訂閱。
	if t, ok := source.Recognize(raw); ok && t.Kind == source.KindVideo {
		return Source{}, &URLError{Code: CodeNotACollection, Message: msgNotACollection}
	}
	if strings.HasSuffix(host, "bilibili.com") && host != "bilibili.com" && host != "www.bilibili.com" && host != "m.bilibili.com" {
		return recognizeSpace(u)
	}
	if host == "b23.tv" || strings.Contains(host, "bilibili.com") {
		return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
	}
	return recognizeYouTube(u)
}

func recognizeYouTube(u *url.URL) (Source, error) {
	path := strings.TrimSuffix(u.Path, "/")
	if path == "/playlist" {
		if id := u.Query().Get("list"); playlistIDRe.MatchString(id) {
			return Source{Kind: KindPlaylist, Platform: PlatformYouTube, ExternalID: id,
				URL: "https://www.youtube.com/playlist?list=" + url.QueryEscape(id)}, nil
		}
		return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
	}
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	switch {
	case len(segs) >= 2 && segs[0] == "channel" && channelIDRe.MatchString(segs[1]) && (len(segs) == 2 || len(segs) == 3 && segs[2] == "videos"):
		return Source{Kind: KindChannel, Platform: PlatformYouTube, ExternalID: segs[1], URL: ChannelURL(segs[1])}, nil
	case len(segs) >= 1 && handleRe.MatchString(segs[0]) && (len(segs) == 1 || len(segs) == 2 && segs[1] == "videos"):
		return Source{Kind: KindChannel, Platform: PlatformYouTube, ExternalID: segs[0], URL: "https://www.youtube.com/" + segs[0]}, nil
	}
	return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
}

// recognizeSpace 辨識 Bilibili 合集：space.bilibili.com/<mid>/lists/<sid>?type=season 與舊格式
// space.bilibili.com/<mid>/channel/collectiondetail?sid=<sid>；UP 主空間與「系列」不支援（SUB 待決問題 S3）。
func recognizeSpace(u *url.URL) (Source, error) {
	if strings.ToLower(u.Hostname()) != "space.bilibili.com" {
		return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	var mid, sid string
	switch {
	case len(segs) == 3 && segs[1] == "lists" && u.Query().Get("type") == "season":
		mid, sid = segs[0], segs[2]
	case len(segs) == 3 && segs[1] == "channel" && segs[2] == "collectiondetail":
		mid, sid = segs[0], u.Query().Get("sid")
	}
	if !digitsRe.MatchString(mid) || !digitsRe.MatchString(sid) {
		return Source{}, &URLError{Code: CodeUnsupportedURL, Message: msgUnsupportedURL}
	}
	return Source{Kind: KindCollection, Platform: PlatformBilibili, ExternalID: mid + ":" + sid, URL: CollectionURL(mid, sid)}, nil
}

// ChannelURL 是頻道的正規網址。
func ChannelURL(id string) string { return "https://www.youtube.com/channel/" + id }

// CollectionURL 是 Bilibili 合集的正規網址（新格式）。
func CollectionURL(mid, sid string) string {
	return "https://space.bilibili.com/" + mid + "/lists/" + sid + "?type=season"
}
