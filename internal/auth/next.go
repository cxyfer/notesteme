package auth

import (
	"net/url"
	"strings"
)

// MaxNextBytes 是登入後要前往的路徑（next）的上限（ENTRY-R6、AUTH-R17；ENTRY 待決問題 E3）。
const MaxNextBytes = 2048

// SafeNext 檢查登入後要前往的路徑，防止開放轉址（ENTRY-R6）；密碼登入與 OIDC（AUTH-R17）共用。
// 全部條件成立時原樣回傳，否則回 "/"：以 "/" 開頭、第二個字元不是 "/" 或 "\"、
// 不含 "\" 與 ASCII 控制字元、最長 MaxNextBytes bytes、以 URL 解析後沒有 scheme 也沒有主機。
func SafeNext(next string) string {
	const home = "/"
	if next == "" || len(next) > MaxNextBytes || next[0] != '/' {
		return home
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return home
	}
	for i := 0; i < len(next); i++ {
		if c := next[i]; c == '\\' || c < 0x20 || c == 0x7f {
			return home
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" {
		return home
	}
	// 解析後的路徑仍要以單一 "/" 開頭（例如 "/%2F%2Fx" 解碼後是 "//x"，瀏覽器不會解碼，但保守起見一併擋下）。
	if strings.HasPrefix(u.Path, "//") {
		return home
	}
	return next
}
