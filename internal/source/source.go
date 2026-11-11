// Package source 辨識網址指向哪個平台的什麼內容（ING-R1），並定義各平台共用的錯誤類別（plan 第 5.2 節）。
// 各平台的實作在子套件，例如 youtube。
package source

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/source/youtube"
)

// 平台名稱（items.platform）。
const (
	PlatformYouTube  = "youtube"
	PlatformBilibili = "bilibili"
	PlatformPodcast  = "podcast"
	PlatformLocal    = "local"
)

// Kind 是網址指向的內容種類。
type Kind string

const (
	KindVideo    Kind = "video"
	KindPlaylist Kind = "playlist"
	KindChannel  Kind = "channel"
	KindFeed     Kind = "feed"
)

// Target 是辨識出的內容。
type Target struct {
	Platform string
	Kind     Kind
	// ExternalID 是平台上的 ID：YouTube 的影片 ID、Bilibili 的 BV 號；頻道與播放清單為其 ID 或 handle。
	ExternalID string
	// Part 是 Bilibili 的分P（從 1 起算），其他平台為 0。
	Part int
	// URL 是正規化的網址；呼叫外部工具時一律使用它，而不是使用者貼上的原始字串。
	URL string
}

// Recognize 辨識網址。無法辨識時 ok 為 false。
// Podcast RSS 沒有辦法只看網址判斷（見 docs/specs/ING-ingest.md 的待決問題），目前不辨識。
func Recognize(raw string) (t Target, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return Target{}, false
	}
	if yt, ok := youtube.Parse(u); ok {
		t = Target{Platform: PlatformYouTube, ExternalID: yt.ID}
		switch yt.Kind {
		case youtube.Video:
			t.Kind, t.URL = KindVideo, youtube.WatchURL(yt.ID)
		case youtube.Playlist:
			t.Kind = KindPlaylist
			t.URL = "https://www.youtube.com/playlist?list=" + url.QueryEscape(yt.ID)
		default:
			t.Kind, t.URL = KindChannel, u.String()
		}
		return t, true
	}
	return recognizeBilibili(u)
}

var bvid = regexp.MustCompile(`^BV[0-9A-Za-z]{3,20}$`)

// recognizeBilibili 只辨識網址；取得內容的部分之後才實作。
func recognizeBilibili(u *url.URL) (Target, bool) {
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "bilibili.com", "m.bilibili.com":
		id, ok := strings.CutPrefix(u.Path, "/video/")
		if id = strings.Trim(id, "/"); !ok || !bvid.MatchString(id) {
			return Target{}, false
		}
		part := 1
		if p := u.Query().Get("p"); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 {
				return Target{}, false
			}
			part = n
		}
		return Target{Platform: PlatformBilibili, Kind: KindVideo, ExternalID: id, Part: part,
			URL: fmt.Sprintf("https://www.bilibili.com/video/%s?p=%d", id, part)}, true
	case "space.bilibili.com":
		id, _, _ := strings.Cut(strings.Trim(u.Path, "/"), "/")
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return Target{}, false
		}
		return Target{Platform: PlatformBilibili, Kind: KindChannel, ExternalID: id, URL: u.String()}, true
	}
	return Target{}, false
}

// Ref 回傳影音的短代號（steps.md 第 2 節）：yt:<id>、bili:<bvid>/p<n>、pod:<feed>/<guid>、local:<path>。
func Ref(platform, externalID string, part int) string {
	switch platform {
	case PlatformYouTube:
		return "yt:" + externalID
	case PlatformBilibili:
		return fmt.Sprintf("bili:%s/p%d", externalID, max(part, 1))
	case PlatformPodcast:
		return "pod:" + externalID
	case PlatformLocal:
		return "local:" + externalID
	}
	return platform + ":" + externalID
}
