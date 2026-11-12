// Package youtube 辨識 YouTube 網址，並透過 yt-dlp（子程序，ADR-0009）取得中繼資料與字幕。
package youtube

import (
	"net/url"
	"regexp"
	"strings"
)

// Kind 是 YouTube 網址指向的內容種類。
type Kind string

const (
	Video    Kind = "video"
	Playlist Kind = "playlist"
	Channel  Kind = "channel"
)

// Target 是辨識出的 YouTube 內容。
type Target struct {
	Kind Kind
	// ID 是影片 ID、播放清單 ID，或頻道的 handle／ID。
	ID string
}

var (
	videoID    = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)
	playlistID = regexp.MustCompile(`^[A-Za-z0-9_-]{3,128}$`)
	handle     = regexp.MustCompile(`^@[\p{L}\p{N}._-]{1,100}$`)
	channelID  = regexp.MustCompile(`^UC[A-Za-z0-9_-]{10,40}$`)
)

// WatchURL 回傳影片的正規網址；呼叫 yt-dlp 時使用它，不直接使用使用者貼上的字串。
func WatchURL(id string) string {
	return "https://www.youtube.com/watch?v=" + url.QueryEscape(id)
}

// Parse 辨識 YouTube 網址：watch、youtu.be、shorts、live、embed 是影片；playlist 是播放清單；
// @handle、/channel/、/c/、/user/ 是頻道。同時有影片與播放清單的 watch 網址視為影片。
func Parse(u *url.URL) (Target, bool) {
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	switch host {
	case "youtu.be":
		id := strings.Trim(u.Path, "/")
		if videoID.MatchString(id) {
			return Target{Kind: Video, ID: id}, true
		}
		return Target{}, false
	case "youtube.com", "m.youtube.com", "music.youtube.com":
	default:
		return Target{}, false
	}

	path := strings.TrimSuffix(u.Path, "/")
	switch {
	case path == "/watch":
		if id := u.Query().Get("v"); videoID.MatchString(id) {
			return Target{Kind: Video, ID: id}, true
		}
		return Target{}, false
	case path == "/playlist":
		if id := u.Query().Get("list"); playlistID.MatchString(id) {
			return Target{Kind: Playlist, ID: id}, true
		}
		return Target{}, false
	}
	for _, prefix := range []string{"/shorts/", "/live/", "/embed/"} {
		if id, ok := strings.CutPrefix(path, prefix); ok {
			if videoID.MatchString(id) {
				return Target{Kind: Video, ID: id}, true
			}
			return Target{}, false
		}
	}
	// 頻道：/@handle（之後可以接 /videos 等分頁）、/channel/UC…、/c/名稱、/user/名稱。
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if handle.MatchString(first) {
		return Target{Kind: Channel, ID: first}, true
	}
	for _, prefix := range []string{"/channel/", "/c/", "/user/"} {
		if rest, ok := strings.CutPrefix(path, prefix); ok {
			id, _, _ := strings.Cut(rest, "/")
			if prefix == "/channel/" && !channelID.MatchString(id) {
				return Target{}, false
			}
			if id != "" {
				return Target{Kind: Channel, ID: id}, true
			}
		}
	}
	return Target{}, false
}
