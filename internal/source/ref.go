package source

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/source/youtube"
)

// biliRef 是 bili:<bvid>/p<n>；BV 號本身的格式交給 Recognize 驗證。
var biliRef = regexp.MustCompile(`^bili:([^/]+)/p([0-9]+)$`)

// ParseRef 把影音的短代號（Ref 的反向）轉成平台網址：yt:<id>、bili:<bvid>/p<n>。
// 其他種類（pod:、local:）沒有網址，回傳 ok=false；不是短代號的字串也是。
//
// 代號中的 ID 是否合法由 Recognize 判斷，這裡不另外檢查格式（例如長度），
// 所以 ID 規則改變時只需要改一個地方。
func ParseRef(ref string) (string, bool) {
	var raw string
	if id, ok := strings.CutPrefix(ref, "yt:"); ok {
		raw = youtube.WatchURL(id)
	} else if m := biliRef.FindStringSubmatch(ref); m != nil {
		raw = "https://www.bilibili.com/video/" + url.PathEscape(m[1]) + "?p=" + m[2]
	} else {
		return "", false
	}
	t, ok := Recognize(raw)
	if !ok || t.Kind != KindVideo {
		return "", false
	}
	return t.URL, true
}

// SplitRef 是 Ref 的反向：把短代號拆成平台、external_id 與分P（Bilibili 從 1 起算，其他平台是 0）。
// 不是短代號（例如影音 ID）時 ok 為 false。
func SplitRef(ref string) (platform, externalID string, part int, ok bool) {
	switch {
	case strings.HasPrefix(ref, "yt:"):
		platform, externalID = PlatformYouTube, strings.TrimPrefix(ref, "yt:")
	case strings.HasPrefix(ref, "bili:"):
		m := biliRef.FindStringSubmatch(ref)
		if m == nil {
			return "", "", 0, false
		}
		n, err := strconv.Atoi(m[2])
		if err != nil || n < 1 {
			return "", "", 0, false
		}
		return PlatformBilibili, m[1], n, true
	case strings.HasPrefix(ref, "pod:"):
		platform, externalID = PlatformPodcast, strings.TrimPrefix(ref, "pod:")
	case strings.HasPrefix(ref, "local:"):
		platform, externalID = PlatformLocal, strings.TrimPrefix(ref, "local:")
	default:
		return "", "", 0, false
	}
	if externalID == "" {
		return "", "", 0, false
	}
	return platform, externalID, 0, true
}
