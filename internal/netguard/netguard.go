// Package netguard 定義對外 HTTP 請求的注入點（ADR-0014、ADR-0015）。
//
// netguard 自己解析 DNS、檢查 IP，只把通過檢查的 Target 交給注入的 Dialer。
// 測試把 Resolver 與 Dialer 換成 fakenet，真實的檢查路徑不變，只有最後一跳被替換。
//
// 目前這裡只有型別、介面與最小的 Transport 輔助函式；IP 政策（私有位址、
// loopback、link-local、metadata 位址等）由之後的 GRD 切片加入。
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
	"strconv"
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
}

// ErrNoDialer 表示 Deps 缺少 Resolver 或 Dialer：不會退回系統的 resolver 與 dialer，
// 以免測試或設定遺漏時悄悄繞過注入點（NFR-TEST-1）。production 要明確傳入 SystemDeps()。
var ErrNoDialer = errors.New("netguard: Deps 缺少 Resolver 或 Dialer，拒絕連線（production 請使用 SystemDeps）")

// systemDialer 使用系統網路直接連線，只供 SystemDeps 使用。
type systemDialer struct{}

func (systemDialer) Dial(ctx context.Context, t Target) (net.Conn, error) {
	var d net.Dialer
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
// TODO(GRD)：目前不做任何 IP 檢查；之後由 GRD 在「解析後、Dial 前」加上 IP 政策，
// 並在 production 的 Dialer 以 net.Dialer.Control 再檢查實際連線的 IP。
func Transport(d Deps) *http.Transport {
	res, dialer := d.Resolver, d.Dialer
	return &http.Transport{
		// 不使用環境變數的 proxy，避免繞過檢查。
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if res == nil || dialer == nil {
				return nil, ErrNoDialer
			}
			return dial(ctx, res, dialer, network, addr)
		},
		TLSClientConfig: &tls.Config{RootCAs: d.RootCAs, MinVersion: tls.VersionTLS12},
	}
}

func dial(ctx context.Context, res Resolver, dialer Dialer, network, addr string) (net.Conn, error) {
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
		conn, err := dialer.Dial(ctx, Target{Network: "tcp", Host: host, IP: ip.Unmap(), Port: uint16(port)})
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}
