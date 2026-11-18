package netguard

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Allowlist 是設定檔 network.allow_private 解析後的結果（GRD-R3）：
// 明確允許連線的網段與主機名稱。零值代表沒有任何例外。
type Allowlist struct {
	// Prefixes 是允許的網段；單一 IP 以 /32、/128 表示。
	Prefixes []netip.Prefix
	// Hosts 是允許的主機名稱（小寫、沒有結尾的點），比對網址中的主機名稱，完全相同才算。
	Hosts []string
}

// containsIP 回報 ip 是否在 allowlist 的網段內。
func (a Allowlist) containsIP(ip netip.Addr) bool {
	for _, p := range a.Prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// containsHost 回報主機名稱是否在 allowlist 內；不分大小寫，不支援萬用字元。
func (a Allowlist) containsHost(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	return slices.ContainsFunc(a.Hosts, func(h string) bool { return normalizeHost(h) == host })
}

// normalizeHost 轉成小寫並去掉結尾的點（FQDN 與一般寫法視為同一個主機）。
func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

// EntryError 表示 allowlist 的某一項不合法。Index 是該項在清單中的位置（從 0 起算）。
type EntryError struct {
	Index int
	Value string
	// Metadata 在項目本身就是雲端 metadata 位址時有值（GRD-R3：一律封鎖，不能加入 allowlist）。
	Metadata netip.Addr
}

func (e *EntryError) Error() string {
	if e.Metadata.IsValid() {
		return fmt.Sprintf("allowlist[%d]：%s 是雲端 metadata 位址，一律封鎖，不能加入 allowlist。", e.Index, e.Metadata)
	}
	return fmt.Sprintf("allowlist[%d] 不是有效的 CIDR、IP 或主機名稱（不支援萬用字元）：%q", e.Index, e.Value)
}

// ParseAllowlist 解析 allowlist 的每一項，依序嘗試：CIDR → 單一 IP（轉成 /32、/128）→ 主機名稱。
// 主機名稱只接受 LDH 格式（字母、數字、連字號），轉成小寫並去掉結尾的點，不支援萬用字元。
// 項目本身就是 metadata 位址時回傳 *EntryError；網段只是包含 metadata 位址時接受，dial 時照樣擋下。
func ParseAllowlist(entries []string) (Allowlist, error) {
	var out Allowlist
	for i, raw := range entries {
		v := strings.TrimSpace(raw)
		if p, err := netip.ParsePrefix(v); err == nil {
			p = netip.PrefixFrom(p.Addr().Unmap(), unmapBits(p)).Masked()
			// 只有「正好一個位址」的網段才算直接列出 metadata 位址。
			if p.Bits() == p.Addr().BitLen() && isMetadata(p.Addr()) {
				return Allowlist{}, &EntryError{Index: i, Value: raw, Metadata: p.Addr()}
			}
			out.Prefixes = append(out.Prefixes, p)
			continue
		}
		if ip, err := netip.ParseAddr(v); err == nil && ip.Zone() == "" {
			ip = ip.Unmap()
			if isMetadata(ip) {
				return Allowlist{}, &EntryError{Index: i, Value: raw, Metadata: ip}
			}
			out.Prefixes = append(out.Prefixes, netip.PrefixFrom(ip, ip.BitLen()))
			continue
		}
		if h := normalizeHost(v); validHostname(h) {
			out.Hosts = append(out.Hosts, h)
			continue
		}
		return Allowlist{}, &EntryError{Index: i, Value: raw}
	}
	return out, nil
}

// unmapBits 回傳 IPv4-mapped 網段轉成 IPv4 之後的遮罩長度（::ffff:10.0.0.0/104 → 10.0.0.0/8）。
func unmapBits(p netip.Prefix) int {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return p.Bits() - 96
	}
	return p.Bits()
}

// isMetadata 回報 ip 是不是雲端 metadata 位址（含 IPv4-mapped 與 NAT64 內嵌的形式）。
func isMetadata(ip netip.Addr) bool {
	ip = ip.Unmap()
	if nat64.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return slices.Contains(metadataAddrs, ip)
}

// validHostname 回報 h 是不是 LDH 格式的主機名稱。最後一個標籤不能全是數字，
// 避免 "1.2.3" 這種不完整的 IP 被當成主機名稱。
func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range []byte(l) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}
