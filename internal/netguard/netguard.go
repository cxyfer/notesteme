// Package netguard 定義對外 HTTP 請求的注入點（ADR-0014、ADR-0015）。
//
// netguard 自己解析 DNS、檢查 IP，只把通過檢查的 Target 交給注入的 Dialer。
// 測試把 Resolver 與 Dialer 換成 fakenet，真實的檢查路徑不變，只有最後一跳被替換。
//
// IP 政策在 dial 階段檢查實際要連線的 IP：擋下 loopback、私有網段、link-local、CGNAT、
// unspecified、multicast 與雲端 metadata 位址；allowlist 可以開放前面的網段，但不能開放 metadata。
package netguard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"time"
)

// Resolver 解析主機名稱。*net.Resolver 本身就符合這個介面。
type Resolver interface {
	// LookupNetIP 查詢 host 的 IP。network 為 "ip"、"ip4" 或 "ip6"。
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Target 是通過檢查、準備連線的目標。
type Target struct {
	Network string     // "tcp"
	Host    string     // 原本的主機名稱（供 SNI、記錄使用）；IP 字面值時等於 IP 字串
	IP      netip.Addr // 實際要連的 IP
	Port    uint16
}

// Addr 回傳 "ip:port" 形式的位址。
func (t Target) Addr() string {
	return net.JoinHostPort(t.IP.String(), strconv.Itoa(int(t.Port)))
}

// Dialer 對 Target 建立連線。
type Dialer interface {
	Dial(ctx context.Context, t Target) (net.Conn, error)
}

// Deps 是 netguard 需要的外部依賴，由 app.Env 注入。Resolver 與 Dialer 都必須提供。
type Deps struct {
	Resolver Resolver
	Dialer   Dialer
	// RootCAs 為 TLS 驗證用的根憑證；nil 代表使用系統的根憑證。
	RootCAs *x509.CertPool
	// Allowlist 是明確允許連線的位址範圍（之後由設定檔提供，預設為空），
	// 例如家用網路內自架的 Podcast feed。雲端 metadata 位址永遠不會被它開放（ADR-0014）。
	Allowlist []netip.Prefix
}

// ErrBlockedAddress 表示目的 IP 被 IP 政策擋下（ADR-0014）。
// 轉址的每一跳都經過同一個 dial，所以轉址到內網位址也會得到這個錯誤；用 errors.Is 判斷。
var ErrBlockedAddress = errors.New("netguard: 目的位址被封鎖")

// dialTimeout 是每次連線嘗試的逾時。
const dialTimeout = 10 * time.Second

// metadataAddrs 是雲端 metadata 服務的位址，任何情況都不允許連線（含 allowlist）。
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), // AWS、GCP、Azure 等
	netip.MustParseAddr("fd00:ec2::254"),   // AWS IPv6
	netip.MustParseAddr("100.100.100.200"), // Alibaba Cloud
}

// blockedPrefixes 是 netip.Addr 的 IsLoopback、IsPrivate、IsLinkLocal 等判斷沒有涵蓋的保留網段。
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // 「本網路」
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT（RFC 6598）
	netip.MustParsePrefix("240.0.0.0/4"),   // 保留（含廣播位址）
}

// nat64 是 NAT64 前綴（RFC 6052）；內嵌的 IPv4 位址要依同一套政策檢查。
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

// checkIP 依政策檢查要連線的 IP；被擋下時回傳包含 ErrBlockedAddress 的錯誤。
// 擋下：loopback、RFC 1918 私有網段與 fc00::/7、link-local（169.254.0.0/16、fe80::/10）、
// CGNAT、unspecified、multicast、保留網段與雲端 metadata 位址。
// allow 內的位址放行，但 metadata 位址不受 allowlist 影響。
func checkIP(ip netip.Addr, allow []netip.Prefix) error {
	ip = ip.Unmap().WithZone("")
	if !ip.IsValid() {
		return fmt.Errorf("%w: 無效的位址", ErrBlockedAddress)
	}
	if nat64.Contains(ip) {
		b := ip.As16()
		// 內嵌的 IPv4 被擋下，這個 NAT64 位址就也被擋下。
		return checkIP(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), allow)
	}
	if slices.Contains(metadataAddrs, ip) {
		return fmt.Errorf("%w: %s 是雲端 metadata 位址", ErrBlockedAddress, ip)
	}
	for _, p := range allow {
		if p.Contains(ip) {
			return nil
		}
	}
	blocked := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
	if !blocked {
		for _, p := range blockedPrefixes {
			if p.Contains(ip) {
				blocked = true
				break
			}
		}
	}
	if blocked {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
	}
	return nil
}

// ErrNoDialer 表示 Deps 缺少 Resolver 或 Dialer：不會退回系統的 resolver 與 dialer，
// 以免測試或設定遺漏時悄悄繞過注入點（NFR-TEST-1）。production 要明確傳入 SystemDeps()。
var ErrNoDialer = errors.New("netguard: Deps 缺少 Resolver 或 Dialer，拒絕連線（production 請使用 SystemDeps）")

// systemDialer 使用系統網路直接連線，只供 SystemDeps 使用。
type systemDialer struct{}

func (systemDialer) Dial(ctx context.Context, t Target) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	return d.DialContext(ctx, t.Network, t.Addr())
}

// SystemDeps 回傳 production 使用的依賴：系統的 resolver、dialer 與根憑證。
func SystemDeps() Deps {
	return Deps{Resolver: net.DefaultResolver, Dialer: systemDialer{}}
}

// Transport 回傳依 d 建立的 *http.Transport：
// DialContext 先用 Resolver 解析主機，再把 Target 交給 Dialer；TLS 使用 RootCAs。
// Resolver 或 Dialer 為 nil 時，每次 dial 都回傳 ErrNoDialer。
//
// IP 政策（ADR-0014）在 dial 階段對「實際要連線的 IP」檢查，見 checkIP：
// 主機名稱解析後的每個 IP、IP 字面值、以及轉址每一跳的目的地都經過同一個檢查，
// 通過的才交給 Dialer；全部被擋下時錯誤符合 ErrBlockedAddress。
// 每次連線嘗試有 10 秒逾時。
func Transport(d Deps) *http.Transport {
	res, dialer, allow := d.Resolver, d.Dialer, slices.Clone(d.Allowlist)
	return &http.Transport{
		// 不使用環境變數的 proxy，避免繞過檢查。
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if res == nil || dialer == nil {
				return nil, ErrNoDialer
			}
			return dial(ctx, res, dialer, allow, network, addr)
		},
		TLSClientConfig: &tls.Config{RootCAs: d.RootCAs, MinVersion: tls.VersionTLS12},
	}
}

func dial(ctx context.Context, res Resolver, dialer Dialer, allow []netip.Prefix, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("netguard: invalid port %q", portStr)
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = res.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("netguard: no addresses for %q", host)
	}
	var errs []error
	for _, ip := range ips {
		ip = ip.Unmap()
		// 只連通過政策的 IP；被擋下的略過，全部被擋下時錯誤符合 ErrBlockedAddress。
		if err := checkIP(ip, allow); err != nil {
			errs = append(errs, fmt.Errorf("%w（主機 %s）", err, host))
			continue
		}
		conn, err := dialWithTimeout(ctx, dialer, Target{Network: "tcp", Host: host, IP: ip, Port: uint16(port)})
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// dialWithTimeout 對單一 Target 連線，逾時 dialTimeout。
func dialWithTimeout(ctx context.Context, dialer Dialer, t Target) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	return dialer.Dial(ctx, t)
}
