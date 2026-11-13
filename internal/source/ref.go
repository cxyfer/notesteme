package source

import (
	"net/url"
	"regexp"
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
