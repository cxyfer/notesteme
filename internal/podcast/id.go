package podcast

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// ParseDuration 解析 itunes:duration：秒數（"1800"、"1800.5"）、"mm:ss"（"30:00"）或 "hh:mm:ss"（"0:30:00"）。
// 秒可以帶小數；格式不認得、負數、超過三段都回傳 false。
func ParseDuration(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, false
	}
	total := 0.0
	for i, part := range parts {
		last := i == len(parts)-1
		digits := strings.Replace(part, ".", "", 1)
		if !last && strings.Contains(part, ".") || digits == "" || strings.Trim(digits, "0123456789") != "" {
			return 0, false
		}
		v, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + v
	}
	return total, true
}

// CanonicalURL 正規化 feed 網址（它是 feed 的身分）：只接受有主機的 http、https；
// scheme、主機轉小寫，去掉預設埠與 fragment，保留 path 與 query。
func CanonicalURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("podcast: 網址不正確：%w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("podcast: 只接受 http 與 https 網址")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, errors.New("podcast: 網址沒有主機")
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]" // IPv6 字面值
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	u.Fragment, u.RawFragment = "", ""
	// 主機根目錄的 feed：`https://example.com` 與 `https://example.com/` 是同一個資源，統一成有 `/` 的形式。
	if u.Path == "" && u.RawPath == "" && u.Opaque == "" {
		u.Path = "/"
	}
	return u, nil
}

// 副檔名與泛用的名稱不適合當 feed 的名字。
var (
	slugExts     = []string{".xml", ".rss", ".atom", ".php", ".aspx"}
	genericSlugs = map[string]bool{
		"rss": true, "feed": true, "feeds": true, "podcast": true, "podcasts": true,
		"atom": true, "index": true, "itunes": true, "xml": true,
	}
	genericHostLabels = map[string]bool{"www": true, "feed": true, "feeds": true, "rss": true}
)

const maxSlugLen = 40

// slugify 把字串轉成 feedKey 的形式：小寫，非 [a-z0-9] 的連續字元換成 "-"，去掉頭尾的 "-"，最多 40 字元。
func slugify(s string) string {
	out := slugifyFull(s)
	if len(out) > maxSlugLen {
		out = strings.TrimRight(out[:maxSlugLen], "-")
	}
	return out
}

// FeedSlug 由 feed 網址產生 feedKey 的候選：path 最後一個非空的段落（去掉 .xml、.rss、.atom、.php、.aspx）；
// 空或是泛用的名稱（rss、feed、podcast 等）就往前取一段；都不行時用主機名稱中第一個不是 www、feed、feeds、rss 的 label，
// 再不行用 "feed"。結果只含 [a-z0-9-]，最多 40 字元。
func FeedSlug(u *url.URL) string {
	segs := strings.Split(u.Path, "/")
	for i := len(segs) - 1; i >= 0; i-- {
		seg := strings.ToLower(strings.TrimSpace(segs[i]))
		if seg == "" {
			continue
		}
		for _, ext := range slugExts {
			if strings.HasSuffix(seg, ext) {
				seg = strings.TrimSuffix(seg, ext)
				break
			}
		}
		full := slugifyFull(seg)
		if full == "" || genericSlugs[full] {
			continue
		}
		return slugify(seg)
	}
	for _, label := range strings.Split(strings.ToLower(u.Hostname()), ".") {
		if s := slugify(label); s != "" && !genericHostLabels[s] {
			return s
		}
	}
	return "feed"
}

// slugifyFull 同 slugify，但不截斷；泛用名稱的判斷要看完整的名稱。
func slugifyFull(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// ExternalID 組出單集的 external_id：<feedKey>/<guid>。
func ExternalID(feedKey, guid string) string { return feedKey + "/" + guid }

// SplitExternalID 拆開單集的 external_id；guid 可以含 "/"，以第一個 "/" 切開。兩邊都不可為空。
func SplitExternalID(id string) (feedKey, guid string, ok bool) {
	feedKey, guid, found := strings.Cut(id, "/")
	if !found || feedKey == "" || guid == "" {
		return "", "", false
	}
	return feedKey, guid, true
}

// TranscriptFormat 回傳字幕的格式：vtt、srt、json；不支援的格式（html、plain 等）回傳空字串。
// 先看 type；type 空白或不認得時，改看網址的副檔名。
func TranscriptFormat(t Transcript) string {
	typ := strings.ToLower(strings.TrimSpace(t.Type))
	if i := strings.IndexByte(typ, ';'); i >= 0 {
		typ = strings.TrimSpace(typ[:i])
	}
	switch typ {
	case "text/vtt":
		return "vtt"
	case "application/x-subrip", "application/srt", "text/srt":
		return "srt"
	case "application/json", "text/json":
		return "json"
	case "text/html", "application/xhtml+xml", "text/plain":
		return ""
	}
	p := t.URL
	if u, err := url.Parse(t.URL); err == nil {
		p = u.Path
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".vtt":
		return "vtt"
	case ".srt":
		return "srt"
	case ".json":
		return "json"
	}
	return ""
}

// Lang 把語言代碼正規化成 BCP 47 的大小寫：en-us → en-US、zh_hant_tw → zh-Hant-TW。
// 空字串或不像語言代碼的輸入回傳空字串。
func Lang(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "_", "-")
	if s == "" {
		return ""
	}
	subs := strings.Split(s, "-")
	if n := len(subs[0]); n < 2 || n > 3 || !isAlpha(subs[0]) {
		return ""
	}
	subs[0] = strings.ToLower(subs[0])
	for i := 1; i < len(subs); i++ {
		sub := subs[i]
		if sub == "" || len(sub) > 8 || strings.Trim(strings.ToLower(sub), "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
			return ""
		}
		switch {
		case len(sub) == 4 && isAlpha(sub):
			subs[i] = strings.ToUpper(sub[:1]) + strings.ToLower(sub[1:])
		case len(sub) == 2 && isAlpha(sub):
			subs[i] = strings.ToUpper(sub)
		default:
			subs[i] = strings.ToLower(sub)
		}
	}
	return strings.Join(subs, "-")
}

func isAlpha(s string) bool {
	return s != "" && strings.Trim(strings.ToLower(s), "abcdefghijklmnopqrstuvwxyz") == ""
}
