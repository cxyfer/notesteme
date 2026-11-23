package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/cxyfer/notesteme/internal/auth"
)

// PrincipalInsecure 是無驗證模式（serve --insecure-no-auth，AUTH-R3）下每個請求的身分種類：
// 全部權限，與 session 一樣不是 token；和 PrincipalLocal 分開，之後若有只給本機 socket 的能力不會被它繼承。
const PrincipalInsecure = "insecure"

// IsLoopbackHost 回報 host（不含 port、可以帶方括號）是否為 localhost 或 loopback 的 IP 字面值。
// 不做 DNS 解析；不接受帶 zone 的 IPv6 位址與 IPv4-mapped 的位址。
func IsLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && !ip.Is4In6() && ip.IsLoopback()
}

// hostOnly 取出 Host 標頭的主機名稱（去掉 port）。
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

// insecureGuard 是無驗證模式在所有路徑（含 Web UI）之前的檢查。沒有 session 就沒有 CSRF token，
// 瀏覽器是唯一需要防的對象：
//   - Host 必須是 loopback 的主機名稱（localhost、127.0.0.1、[::1]）：擋下 DNS rebinding（攻擊者的網域解析到 127.0.0.1）。
//   - 會改變狀態的請求帶了 Origin 時必須與 Host 同源（Origin: null 也拒絕）；Sec-Fetch-Site 只接受 same-origin 與 none：
//     擋下其他網站（包含同一台電腦其他 port 的網頁）送出的表單與 fetch。curl 等不帶這兩個標頭的程式照常可用。
func insecureGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsLoopbackHost(hostOnly(r.Host)) {
			writeError(w, newError(http.StatusForbidden, CodeInsecureHost,
				"這個實例以 --insecure-no-auth 啟動，只接受以 localhost、127.0.0.1 或 [::1] 存取。"))
			return
		}
		if !safeMethod(r.Method) && !sameOriginRequest(r) {
			writeError(w, newError(http.StatusForbidden, CodeCSRF, "這個實例以 --insecure-no-auth 啟動，拒絕來自其他網站的請求。"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginRequest 回報會改變狀態的請求是否可以接受：沒有 Origin 與 Sec-Fetch-Site（非瀏覽器），
// 或兩者都表示同源。多個值一律拒絕。
func sameOriginRequest(r *http.Request) bool {
	if v := r.Header.Values("Sec-Fetch-Site"); len(v) > 1 || len(v) == 1 && v[0] != "same-origin" && v[0] != "none" {
		return false
	}
	origins := r.Header.Values("Origin")
	switch len(origins) {
	case 0:
		return true
	case 1:
	default:
		return false
	}
	u, err := url.Parse(origins[0])
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// insecureSessions 讓 ENTRY 的頁面路由（/add、/share）在無驗證模式下視為已登入。
type insecureSessions struct{}

func (insecureSessions) AuthenticateSession(context.Context, string) (auth.Principal, error) {
	return auth.Principal{Kind: PrincipalInsecure}, nil
}

// NoAuth 讓 entry.Pages 不看 session cookie（entry.Pages.signedIn）。
func (insecureSessions) NoAuth() bool { return true }
