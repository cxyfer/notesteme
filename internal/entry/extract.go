// Package entry 是「其他新增入口」（ENTRY 規格）：bookmarklet 開啟的 /add、PWA 的 share target /share、
// 登入頁 /login 的頁面路由，以及 PWA 的 manifest 與 service worker。
//
// 這些路由都不改變狀態（ENTRY-R7）：不辨識網址、不建立影音，只把網址轉交給 UI 的「新增」抽屜，
// 由使用者確認後才開始處理。網址來自第三方頁面與其他 App，一律當不可信輸入。
package entry

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxURLChars 是分享或書籤帶進來的網址上限（ENTRY-R2、R4），以字元（Unicode code point）計。
const MaxURLChars = 2048

// urlPattern 是 ENTRY-R4 的 `https?://[^\s<>"']+`。
// scheme 不分大小寫；\s 之外也在 Unicode 的空白（\p{Z}，例如全形空白 U+3000、NBSP）停下：
// RE2 的 \s 只涵蓋 ASCII 空白（ENTRY 待決問題 E6）。
var urlPattern = regexp.MustCompile(`(?i)https?://[^\s\p{Z}<>"']+`)

// trailing 是擷取後反覆去掉的結尾標點（ENTRY-R4）。
const trailing = ".,;:!?)]}」』）。，、"

// ShareURL 依 ENTRY-R4 從 share target 的參數取出第一個可用的網址：
// url 參數是 http／https 時用它；否則取 text 中第一個網址；再否則取 title 中的。
// 沒有可用的網址時 ok 為 false。
//
// 決定（ENTRY 待決問題 E6）：三個來源都去掉結尾標點；一個來源中，去掉標點後沒有主機或超過 MaxURLChars
// 的候選視為沒有，繼續看同一個來源的下一個候選，再換下一個來源。
func ShareURL(rawURL, text, title string) (string, bool) {
	if u, ok := clean(strings.TrimSpace(rawURL)); ok && urlPattern.FindString(u) == u {
		return u, true
	}
	for _, src := range []string{text, title} {
		for _, m := range urlPattern.FindAllString(src, -1) {
			if u, ok := clean(m); ok {
				return u, true
			}
		}
	}
	return "", false
}

// clean 去掉結尾標點，再確認是有主機的 http／https 網址且不超過上限。
func clean(s string) (string, bool) {
	for {
		r, size := utf8.DecodeLastRuneInString(s)
		if size == 0 || !strings.ContainsRune(trailing, r) {
			break
		}
		s = s[:len(s)-size]
	}
	if !ValidURL(s) {
		return "", false
	}
	return s, true
}

// ValidURL 回報 s 是不是 /add 可以接受的網址（ENTRY-R2）：http 或 https、有主機、不超過 MaxURLChars 個字元、
// 不含空白與控制字元。UI 端（web/client/src/add/entry.ts）用同樣的條件。
func ValidURL(s string) bool {
	if s == "" || utf8.RuneCountInString(s) > MaxURLChars || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}
