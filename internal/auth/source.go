package auth

import (
	"net"
	"net/netip"
	"strings"
)

// SourceAddr 決定登入的來源位址（AUTH-R6），用來分開計算失敗次數。
//
// 預設取連線位址 remoteAddr（http.Request.RemoteAddr）。連線來自 trusted 中的位址
// （反向代理）時，改用 X-Forwarded-For 中最右邊一個不在 trusted 內的位址：
// 每一層可信任的代理會把它看到的連線位址附加在右側，所以從右往左走到第一個不可信任的位址，
// 就是最後一個可信任的代理所看到的 client；更左邊的內容是 client 自己填的，可以偽造，不採用。
// 連線不在 trusted 內時一律忽略 X-Forwarded-For，標頭不存在、格式不對或每一個位址都可信任時，
// 也回到連線位址。forwardedFor 為同一個請求所有 X-Forwarded-For 標頭值。
func SourceAddr(remoteAddr string, forwardedFor []string, trusted []netip.Prefix) string {
	conn := hostOf(remoteAddr)
	connAddr, err := netip.ParseAddr(conn)
	if err != nil || !inPrefixes(connAddr.Unmap(), trusted) {
		return conn
	}
	var parts []string
	for _, v := range forwardedFor {
		parts = append(parts, strings.Split(v, ",")...)
	}
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return conn
		}
		a = a.Unmap().WithZone("")
		if !inPrefixes(a, trusted) {
			return a.String()
		}
	}
	return conn
}

// hostOf 取出 host:port 中的位址；沒有 port 時原樣回傳。
func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func inPrefixes(a netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// sourceKey 是以來源位址計數（登入鎖定、OIDC start 的限流）時的 key：IPv4（含 IPv4-mapped）是位址本身，
// IPv6 是所在的 /64。一般的網路至少分配一個 /64，以完整位址計算的話，換一個位址就能多得一輪嘗試。
// 不是 IP 的值原樣使用。
func sourceKey(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return a.String()
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}
