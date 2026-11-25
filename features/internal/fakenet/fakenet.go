// Package fakenet 是驗收測試用的假網路（ADR-0015）。
//
// Net 同時實作 netguard.Resolver 與 netguard.Dialer：
//   - 解析：依 Resolves 登記的 IP 回傳；有 Handle 但沒登記 IP 的主機解析到 DefaultIP；
//     其他主機回 NXDOMAIN。
//   - 連線：依 Target 的主機與 IP 找到登記的 handler，轉到程序內的 httptest server。
//     443 埠使用 TLS，憑證由程序內的測試 CA 簽發（RootCAs 取得信任池）。
//   - 沒有登記的連線目標記為違規（Violations），並回傳錯誤。
//   - 另外提供 InstallTrap，讓繞過 netguard 的連線（http.DefaultTransport、
//     net.DefaultResolver）也會失敗並被記錄。
package fakenet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/cxyfer/notesteme/internal/netguard"
)

// DefaultIP 是有 handler 但沒指定 IP 的主機預設解析到的位址（公開位址）。
var DefaultIP = netip.MustParseAddr("93.184.216.34")

// Conn 是一次連線嘗試的紀錄（只要 Dialer 被呼叫就算，不論成功與否）。
type Conn struct {
	Host string
	IP   netip.Addr
	Port uint16
}

// Lookup 是一次 DNS 解析的紀錄。
type Lookup struct {
	Host string
	IPs  []netip.Addr
}

type host struct {
	name    string
	ips     []netip.Addr // Resolves 登記的序列；空代表使用 DefaultIP
	next    int          // 下一次解析要回傳的索引
	handler http.Handler
	routes  map[string]http.Handler // Route 登記的 path（含 query）對應 handler

	plain, tlsSrv *httptest.Server
}

// active 回報這個主機是否有 fake origin（Handle 或 Route 至少登記過一個）。
func (h *host) active() bool { return h.handler != nil || len(h.routes) > 0 }

// serve 依請求的 path 加 query 找到 handler：先比對 Route，找不到時退回 Handle 的 handler，
// 兩者都沒有就回 404。呼叫時不可持有 n.mu。
func (n *Net) serve(h *host, w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	rt := h.routes[routeKey(r.URL.EscapedPath(), r.URL.RawQuery)]
	fallback := h.handler
	observe := n.observer
	n.mu.Unlock()
	if observe != nil {
		observe(h.name, r)
	}
	switch {
	case rt != nil:
		rt.ServeHTTP(w, r)
	case fallback != nil:
		fallback.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

// routeKey 是路由的比對鍵：path 加上（非空時）query，兩者都必須完全相同。
func routeKey(path, rawQuery string) string {
	if path == "" {
		path = "/"
	}
	if rawQuery != "" {
		return path + "?" + rawQuery
	}
	return path
}

func (h *host) addrs() []netip.Addr {
	if len(h.ips) == 0 {
		return []netip.Addr{DefaultIP}
	}
	return h.ips
}

func (h *host) hasIP(ip netip.Addr) bool {
	for _, a := range h.addrs() {
		if a == ip {
			return true
		}
	}
	return false
}

// Net 是假的 DNS 與網路。零值不可用，請用 New。並行安全。
type Net struct {
	mu         sync.Mutex
	hosts      map[string]*host
	conns      []Conn
	lookups    []Lookup
	violations []string
	closed     bool
	// observer 在每個請求交給 handler 之前被呼叫（見 Observe）。
	observer func(host string, r *http.Request)
}

// Observe 登記一個觀察者：每個送到 fake origin 的請求在交給 handler 之前都會先經過它
// （故障注入的 harness 用來把付費呼叫寫進程序外的計數檔，見 features/fault）。
// 觀察者可以讀取 r.Body，但讀完要換成內容相同的新 Body。nil 取消登記。
func (n *Net) Observe(fn func(host string, r *http.Request)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.observer = fn
}

// ErrClosed 表示 Net 已經 Close，不再啟動 fake origin。
var ErrClosed = errors.New("fakenet: closed")

var (
	_ netguard.Resolver = (*Net)(nil)
	_ netguard.Dialer   = (*Net)(nil)
)

// New 建立空的假網路。
func New() *Net { return &Net{hosts: map[string]*host{}} }

// RootCAs 回傳信任測試 CA 的憑證池。
func (n *Net) RootCAs() *x509.CertPool { return RootCAs() }

// Deps 回傳可直接交給 netguard.Transport 的依賴。
func (n *Net) Deps() netguard.Deps {
	return netguard.Deps{Resolver: n, Dialer: n, RootCAs: n.RootCAs()}
}

func (n *Net) getHost(name string) *host {
	name = strings.ToLower(name)
	h := n.hosts[name]
	if h == nil {
		h = &host{name: name}
		n.hosts[name] = h
	}
	return h
}

// Resolves 登記主機的解析結果。每次解析依序回傳一個 IP，用完後一直回傳最後一個，
// 可模擬 DNS rebinding（例如先公開 IP、再 127.0.0.1）。重複呼叫會取代原有序列。
// ips 不是合法的 IP 字串時回傳錯誤。
func (n *Net) Resolves(hostName string, ips ...string) error {
	addrs := make([]netip.Addr, 0, len(ips))
	for _, s := range ips {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		if err != nil {
			return fmt.Errorf("fakenet: invalid IP %q for host %q", s, hostName)
		}
		addrs = append(addrs, a.Unmap())
	}
	if len(addrs) == 0 {
		return fmt.Errorf("fakenet: Resolves(%q) needs at least one IP", hostName)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	h := n.getHost(hostName)
	h.ips, h.next = addrs, 0
	return nil
}

// Handle 登記主機的 fake origin。沒有指定 IP 的主機預設解析到 DefaultIP。
// 重複呼叫會取代 handler。
func (n *Net) Handle(hostName string, handler http.Handler) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.getHost(hostName).handler = handler
}

// Route 登記 rawURL 所指的 fake origin 路由：依主機分組，以 path 加 query 完全相同才算比對成功
// （不分 scheme 與埠）。沒有登記的路徑回 404。可與 Handle 共存：同一主機先比對 Route，
// 找不到才交給 Handle 的 handler；沒有 Handle 時回 404。重複登記同一個路由會取代 handler。
// 只登記 Route 的主機與 Handle 一樣有 fake origin：沒指定 IP 時解析到 DefaultIP。
func (n *Net) Route(rawURL string, handler http.Handler) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("fakenet: invalid URL %q", rawURL)
	}
	if handler == nil {
		return fmt.Errorf("fakenet: Route(%q) needs a handler", rawURL)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	h := n.getHost(u.Hostname())
	if h.routes == nil {
		h.routes = map[string]http.Handler{}
	}
	h.routes[routeKey(u.EscapedPath(), u.RawQuery)] = handler
	return nil
}

// LookupNetIP 實作 netguard.Resolver。
func (n *Net) LookupNetIP(_ context.Context, network, hostName string) ([]netip.Addr, error) {
	hostName = strings.ToLower(strings.TrimSuffix(hostName, "."))
	n.mu.Lock()
	defer n.mu.Unlock()
	h := n.hosts[hostName]
	if h == nil || (len(h.ips) == 0 && !h.active()) {
		return nil, &net.DNSError{Err: "no such host", Name: hostName, IsNotFound: true}
	}
	// 先挑出這次要回傳的 IP（不推進序列）；依 network 過濾後為空時，
	// 不推進 rebinding 序列，否則 ip4 與 ip6 的並行查詢會吃掉彼此的序列位置。
	var ip netip.Addr
	advance := false
	if len(h.ips) == 0 {
		ip = DefaultIP
	} else {
		i := h.next
		if i >= len(h.ips) {
			i = len(h.ips) - 1
		} else {
			advance = true
		}
		ip = h.ips[i]
	}
	if (network == "ip4" && !ip.Is4()) || (network == "ip6" && !ip.Is6()) {
		return nil, nil
	}
	if advance {
		h.next++
	}
	n.lookups = append(n.lookups, Lookup{Host: hostName, IPs: []netip.Addr{ip}})
	return []netip.Addr{ip}, nil
}

// originFor 依 Target 找到登記的 host；呼叫時需持有 n.mu。
// 先以主機名稱比對，目標是 IP 字面值時改以 IP 比對。
func (n *Net) originFor(t netguard.Target) *host {
	if h := n.hosts[strings.ToLower(t.Host)]; h != nil && h.active() && h.hasIP(t.IP) {
		return h
	}
	if _, err := netip.ParseAddr(t.Host); err != nil {
		return nil
	}
	names := make([]string, 0, len(n.hosts))
	for name := range n.hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if h := n.hosts[name]; h.active() && h.hasIP(t.IP) {
			return h
		}
	}
	return nil
}

// Dial 實作 netguard.Dialer：記錄連線，再轉到對應 fake origin 的本機 server。
func (n *Net) Dial(ctx context.Context, t netguard.Target) (net.Conn, error) {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil, ErrClosed
	}
	n.conns = append(n.conns, Conn{Host: strings.ToLower(t.Host), IP: t.IP, Port: t.Port})
	h := n.originFor(t)
	if h == nil {
		msg := fmt.Sprintf("unexpected connection to %s (%s):%d: no origin registered", t.Host, t.IP, t.Port)
		n.violations = append(n.violations, msg)
		n.mu.Unlock()
		return nil, errors.New("fakenet: " + msg)
	}
	srv, err := n.serverLocked(h, t.Port == 443)
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
}

// serverLocked 惰性啟動 host 的 httptest server；呼叫時需持有 n.mu。
func (n *Net) serverLocked(h *host, useTLS bool) (*httptest.Server, error) {
	if useTLS && h.tlsSrv != nil {
		return h.tlsSrv, nil
	}
	if !useTLS && h.plain != nil {
		return h.plain, nil
	}
	// 路由與 handler 在請求時才取得，讓之後重新 Handle 或 Route 也會生效。
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.serve(h, w, r)
	})
	srv := httptest.NewUnstartedServer(dispatch)
	if useTLS {
		var extra []net.IP
		for _, a := range h.addrs() {
			extra = append(extra, net.IP(a.AsSlice()))
		}
		name := h.name
		cfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
			// 依 SNI 簽發憑證；沒有 SNI（IP 字面值）時使用登記的主機名稱。
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				sni := hello.ServerName
				if sni == "" {
					sni = name
				}
				return issueLeaf(strings.ToLower(sni), extra)
			},
		}
		srv.Listener = tls.NewListener(srv.Listener, cfg)
	}
	srv.Start()
	if useTLS {
		h.tlsSrv = srv
	} else {
		h.plain = srv
	}
	return srv, nil
}

// ConnectedTo 回報是否曾對指定的主機或 IP 嘗試建立連線。
// 只看 Dialer 被呼叫的紀錄；DNS 解析本身不算連線。
func (n *Net) ConnectedTo(hostOrIP string) bool {
	hostOrIP = strings.TrimSpace(hostOrIP)
	ip, ipErr := netip.ParseAddr(hostOrIP)
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, c := range n.conns {
		if ipErr == nil && c.IP == ip.Unmap() {
			return true
		}
		if strings.EqualFold(c.Host, hostOrIP) {
			return true
		}
	}
	return false
}

// Connections 回傳所有連線嘗試的紀錄（複本）。
func (n *Net) Connections() []Conn {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Conn(nil), n.conns...)
}

// Lookups 回傳所有 DNS 解析的紀錄（複本）。
func (n *Net) Lookups() []Lookup {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Lookup(nil), n.lookups...)
}

// Violations 回傳沒有登記的連線目標（複本）。
func (n *Net) Violations() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.violations...)
}

// Close 關閉所有已啟動的 fake origin server。Close 之後的 Dial 回傳 ErrClosed，不會重新啟動 server。
func (n *Net) Close() {
	n.mu.Lock()
	n.closed = true
	var srvs []*httptest.Server
	for _, h := range n.hosts {
		if h.plain != nil {
			srvs = append(srvs, h.plain)
			h.plain = nil
		}
		if h.tlsSrv != nil {
			srvs = append(srvs, h.tlsSrv)
			h.tlsSrv = nil
		}
	}
	n.mu.Unlock()
	for _, s := range srvs {
		s.CloseClientConnections()
		s.Close()
	}
}
