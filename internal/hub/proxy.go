package hub

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// withTrustedProxies 依 server.trusted_proxies 改寫 r.RemoteAddr（安全修補 A，HACC 待決問題 A14）：
// 只有 TCP 對端在 trusted 中時才看 X-Forwarded-For，從右邊往左取第一個不在 trusted 中的位址當成用戶端；
// 全部都在 trusted 中時取最左邊可信任的那一個；遇到無法解析的項目就停（它左邊的都可能是偽造的）。
// 之後的限流與下架申請都以 r.RemoteAddr 為準；Hub 不依請求的 scheme 決定任何事（cookie 一律 Secure、網址取自 public_url），
// 所以不需要 X-Forwarded-Proto。
func withTrustedProxies(trusted []netip.Prefix, next http.Handler) http.Handler {
	if len(trusted) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip, ok := clientFromForwarded(r.RemoteAddr, r.Header.Values("X-Forwarded-For"), trusted); ok {
			r2 := r.Clone(r.Context())
			r2.RemoteAddr = net.JoinHostPort(ip.String(), "0")
			r = r2
		}
		next.ServeHTTP(w, r)
	})
}

func inTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func parseIP(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// clientFromForwarded 回傳依 X-Forwarded-For 決定的用戶端位址；對端不可信任、沒有標頭或格式不正確時 ok 為 false。
func clientFromForwarded(remoteAddr string, xff []string, trusted []netip.Prefix) (netip.Addr, bool) {
	peer, ok := parseIP(remoteAddr)
	if !ok || !inTrusted(peer, trusted) {
		return netip.Addr{}, false
	}
	// 由右往左：最右邊是最近的代理加上的，左邊的部分可能是用戶端自己偽造的，所以遇到第一個不可信任的位址就停。
	var parts []string
	for _, h := range xff {
		parts = append(parts, strings.Split(h, ",")...)
	}
	var last netip.Addr
	for i := len(parts) - 1; i >= 0; i-- {
		a, ok := parseIP(parts[i])
		if !ok {
			break
		}
		if !inTrusted(a, trusted) {
			return a, true
		}
		last = a
	}
	// 全部都是可信任的代理（或左邊無法解析）：取最左邊解析得到的那一個。
	return last, last.IsValid()
}
