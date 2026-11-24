// Package compat 是 CLI 與 serve 之間的版本檢查（NFR-CMP-3、NFR-CMP-4、CLI-R9）。
//
// CLI 對 serve 的每個請求帶 X-Notesteme-Version（自己的程式版本）；serve 收到這個標頭時，回應也帶自己的版本，
// 不相容就回 426。CLI 也檢查回應標頭：較舊、自己不會擋的 serve，也能由 CLI 指出要升級哪一邊。
//
// 相容的定義：major 相同，而且 minor 相差不超過 1（新版的 CLI 可以連到舊一個小版本的 serve，反之亦然）。
// 0.x 也用同一條規則（0.5 與 0.4 相容，0.5 與 0.3 不相容），不把 0.x 的 minor 當成 major：
// 那樣 0.5 的 CLI 連 0.4 的 serve 就不相容，會比 NFR-CMP-4 要求的更嚴（見 CLI 規格的待決問題）。
// 版本字串無法解析（例如開發版的 dev）或沒有帶時一律視為相容，不擋。
package compat

import (
	"fmt"
	"strconv"
	"strings"
)

// Header 是雙方交換程式版本的標頭。
const Header = "X-Notesteme-Version"

// Verdict 是版本檢查的結果。
type Verdict int

const (
	// Compatible 是相容（包含無法判斷）。
	Compatible Verdict = iota
	// ClientTooOld 是 CLI 比 serve 舊太多：要升級 CLI。
	ClientTooOld
	// ServerTooOld 是 serve 比 CLI 舊太多：要升級 serve，或換成相符版本的 CLI。
	ServerTooOld
)

type semver struct{ major, minor int }

// parse 解析 "v1.2.3"、"0.5.0-rc.1"、"0.5.0-3-gabc123"（git describe）等形式，只取 major 與 minor。
// 超過 64 bytes 的字串視為無法解析（對方送來的值會出現在錯誤訊息中）。
func parse(v string) (semver, bool) {
	if len(v) > 64 {
		return semver{}, false
	}
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semver{}, false
	}
	var n [3]int
	for i, p := range parts {
		x, err := strconv.Atoi(p)
		if err != nil || x < 0 || p == "" {
			return semver{}, false
		}
		n[i] = x
	}
	return semver{n[0], n[1]}, true
}

// Check 比較 CLI 與 serve 的版本。
func Check(client, server string) Verdict {
	c, okc := parse(client)
	s, oks := parse(server)
	if !okc || !oks {
		return Compatible
	}
	switch {
	case c.major < s.major:
		return ClientTooOld
	case c.major > s.major:
		return ServerTooOld
	case c.minor+1 < s.minor:
		return ClientTooOld
	case s.minor+1 < c.minor:
		return ServerTooOld
	}
	return Compatible
}

// Message 是不相容時給使用者看的訊息：指出要升級哪一邊。serve 的 426 與 CLI 自己的檢查用同一段文字。
func Message(v Verdict, client, server string) string {
	switch v {
	case ClientTooOld:
		return fmt.Sprintf("CLI 版本太舊（CLI %s，serve %s），請升級 CLI。", client, server)
	case ServerTooOld:
		s, _ := parse(server)
		return fmt.Sprintf("serve 版本太舊（serve %s，CLI %s），請升級 serve，或換成 %d.%d 版的 CLI。", server, client, s.major, s.minor)
	}
	return ""
}
