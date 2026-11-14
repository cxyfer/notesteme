// Package cookies 處理使用者上傳的平台 cookie（SEC-R4、SEC-R7、SRC-R8）：
// 正規化成 Netscape 格式、加密存放、執行時寫成權限 0600 的暫存檔交給 yt-dlp。
//
// 所有錯誤訊息都不含 cookie 的值；明文只存在於記憶體與 release 之前的暫存檔。
package cookies

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

// MaxContentSize 是上傳內容的大小上限（bytes）。
const MaxContentSize = 64 << 10

// 平台識別（與 source.Platform* 的值相同；這裡不引入 source 套件，避免依賴方向反過來）。
const (
	PlatformYouTube  = "youtube"
	PlatformBilibili = "bilibili"
)

// platformDomains 是白名單：key 是平台，value 是該平台的 cookie 網域（不含開頭的點）。
// 平台名稱也是 secrets 表的 name（AAD 是 kind/name），所以不得含 "/"。
var platformDomains = map[string]string{
	PlatformYouTube:  "youtube.com",
	PlatformBilibili: "bilibili.com",
}

// ValidPlatform 回報 platform 是否在白名單中。
func ValidPlatform(platform string) bool {
	_, ok := platformDomains[platform]
	return ok
}

// 正規化的錯誤；API 依它們對應到 422 的 code。訊息不含 cookie 的值。
var (
	ErrUnknownPlatform = errors.New("不支援這個平台，目前只接受 youtube 與 bilibili。")
	ErrEmpty           = errors.New("cookie 內容是空的。")
	ErrTooLarge        = errors.New("cookie 內容超過 64 KiB。")
	ErrNoCookies       = errors.New("內容中沒有這個平台的 cookie。")
	ErrMissingSESSDATA = errors.New("bilibili 的 cookie 缺少 SESSDATA。")
	ErrInvalidFormat   = errors.New("cookie 內容含有不合法的字元。")
)

const netscapeHeader = "# Netscape HTTP Cookie File\n"

// httpOnlyPrefix 是瀏覽器匯出 HttpOnly cookie 時加在網域前的標記（yt-dlp 也認得）。
const httpOnlyPrefix = "#HttpOnly_"

// cookie 是正規化後的一筆 cookie。
type cookie struct {
	httpOnly bool
	domain   string
	subs     string // TRUE 或 FALSE：是否適用子網域
	path     string
	secure   string // TRUE 或 FALSE
	expiry   string // Unix 秒，0 代表 session cookie
	name     string
	value    string
}

func (c cookie) line() string {
	d := c.domain
	if c.httpOnly {
		d = httpOnlyPrefix + d
	}
	return strings.Join([]string{d, c.subs, c.path, c.secure, c.expiry, c.name, c.value}, "\t") + "\n"
}

// Normalize 把使用者上傳的內容轉成 Netscape cookie 檔，回傳內容與 cookie 數量。
//
// content 可以是 Netscape 格式，或瀏覽器標頭的 "name=value; …"（可帶 "Cookie:" 前綴）：
//   - 標頭格式的 cookie 用平台網域（.youtube.com、.bilibili.com）、path /、secure TRUE、expiry 0。
//   - Netscape 格式只保留平台網域（含子網域）的 cookie，其他網域一律丟掉。
//
// 錯誤見 Err* 變數；失敗時回傳的內容為空，錯誤訊息不含 cookie 的值。
func Normalize(platform, content string) (string, int, error) {
	domain, ok := platformDomains[platform]
	if !ok {
		return "", 0, ErrUnknownPlatform
	}
	if len(content) > MaxContentSize {
		return "", 0, ErrTooLarge
	}
	if strings.TrimSpace(content) == "" {
		return "", 0, ErrEmpty
	}

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var cookies []cookie
	if looksLikeNetscape(lines) {
		var err error
		if cookies, err = parseNetscape(lines, domain); err != nil {
			return "", 0, err
		}
	} else {
		var err error
		if cookies, err = parseHeader(lines, domain); err != nil {
			return "", 0, err
		}
	}
	if len(cookies) == 0 {
		return "", 0, ErrNoCookies
	}
	if platform == PlatformBilibili {
		found := false
		for _, c := range cookies {
			found = found || c.name == "SESSDATA"
		}
		if !found {
			return "", 0, ErrMissingSESSDATA
		}
	}

	var sb strings.Builder
	sb.WriteString(netscapeHeader)
	for _, c := range cookies {
		sb.WriteString(c.line())
	}
	return sb.String(), len(cookies), nil
}

// looksLikeNetscape 回報內容是否是 Netscape 格式：有任何一行以 tab 分成 7 欄。
func looksLikeNetscape(lines []string) bool {
	for _, l := range lines {
		if len(strings.Split(strings.TrimPrefix(l, httpOnlyPrefix), "\t")) == 7 {
			return true
		}
	}
	return false
}

// parseNetscape 讀出 Netscape 格式中屬於 domain 的 cookie；註解、空行、格式不符的行與其他網域都略過。
// 保留下來的 cookie 的網域、path、名稱、值含控制字元（例如 \r、NUL、ESC）時回傳 ErrInvalidFormat，與標頭格式一致：
// 這些內容會原樣寫進 cookie 檔，控制字元會讓欄位錯位，或在 yt-dlp 的 stderr 與 log 中造成終端機跳脫。
func parseNetscape(lines []string, domain string) ([]cookie, error) {
	var out []cookie
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		httpOnly := strings.HasPrefix(l, httpOnlyPrefix)
		l = strings.TrimPrefix(l, httpOnlyPrefix)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) != 7 {
			continue
		}
		c := cookie{httpOnly: httpOnly, domain: f[0], subs: strings.ToUpper(f[1]), path: f[2], secure: strings.ToUpper(f[3]),
			expiry: f[4], name: f[5], value: f[6]}
		if !domainMatches(c.domain, domain) || c.name == "" || !validBool(c.subs) || !validBool(c.secure) || c.path == "" {
			continue
		}
		if _, err := strconv.ParseInt(c.expiry, 10, 64); err != nil {
			continue
		}
		if hasControl(c.domain) || hasControl(c.path) || hasControl(c.name) || hasControl(c.value) {
			return nil, ErrInvalidFormat
		}
		out = append(out, c)
	}
	return out, nil
}

func validBool(s string) bool { return s == "TRUE" || s == "FALSE" }

// domainMatches 回報 cookie 的網域是否是 base 或它的子網域（不分大小寫、忽略開頭的點）。
// 只比對完整的網域層級：notyoutube.com 與 youtube.com.evil.example 都不符。
func domainMatches(cookieDomain, base string) bool {
	d := strings.ToLower(strings.TrimPrefix(cookieDomain, "."))
	return d == base || strings.HasSuffix(d, "."+base)
}

// parseHeader 讀出 "name=value; name=value" 的標頭格式（每一行都可以帶 "Cookie:" 前綴）。
func parseHeader(lines []string, domain string) ([]cookie, error) {
	var out []cookie
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if len(l) >= len("cookie:") && strings.EqualFold(l[:len("cookie:")], "cookie:") {
			l = strings.TrimSpace(l[len("cookie:"):])
		}
		for _, part := range strings.Split(l, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, ok := strings.Cut(part, "=")
			name, value = strings.TrimSpace(name), strings.TrimSpace(value)
			if !ok || name == "" {
				continue
			}
			// 值會寫進以 tab 分欄、以換行分行的檔案：控制字元（含 tab）會讓欄位錯位，一律拒絕。
			if hasControl(name) || hasControl(value) {
				return nil, ErrInvalidFormat
			}
			out = append(out, cookie{domain: "." + domain, subs: "TRUE", path: "/", secure: "TRUE", expiry: "0", name: name, value: value})
		}
	}
	return out, nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}
