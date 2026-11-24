package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// GRD-R3、ADR-0014：預設規則漏掉的保留網段與雲端 metadata 位址。
func TestCheckIPBlocksEdgeRanges(t *testing.T) {
	for _, s := range []string{
		"64:ff9b:1::a9fe:a9fe", // RFC 8215 local-use NAT64（內嵌 169.254.169.254）
		"64:ff9b:1::7f00:1",    // local-use NAT64（內嵌 127.0.0.1）
		"64:ff9b:1:ffff::1",
		"::ffff:0:7f00:1",       // SIIT 的 IPv4-translated（RFC 2765）
		"::ffff:0:5db8:d822",    // 內嵌的是公開位址也擋：這個網段不會出現在公開網路
		"100::1",                // discard-only（RFC 6666）
		"100::ffff:ffff:ffff:1", // 同上，/64 的尾端
		"168.63.129.16",         // Azure WireServer
		"::ffff:168.63.129.16",  // IPv4-mapped 先還原
		"64:ff9b::a83f:8110",    // NAT64 內嵌 168.63.129.16
		"fd00:ec2::254",         // AWS IMDS（IPv6）
	} {
		if err := checkIP(s, netip.MustParseAddr(s), Allowlist{}); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s 沒有被擋：%v", s, err)
		}
	}
	// metadata 位址不能由 allowlist 開放。
	wide := Allowlist{Prefixes: []netip.Prefix{netip.MustParsePrefix("168.63.0.0/16"), netip.MustParsePrefix("fd00::/8")}}
	for _, s := range []string{"168.63.129.16", "fd00:ec2::254"} {
		if err := checkIP(s, netip.MustParseAddr(s), wide); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("allowlist 開放了 metadata 位址 %s", s)
		}
	}
	if _, err := ParseAllowlist([]string{"168.63.129.16"}); err == nil {
		t.Error("allowlist 不能直接列出 168.63.129.16")
	}
	// 相鄰的公開位址不受影響。
	for _, s := range []string{"168.63.129.17", "101::1", "64:ff9b:2::1", "93.184.216.34", "2606:2800:220:1::1"} {
		if err := checkIP(s, netip.MustParseAddr(s), Allowlist{}); err != nil {
			t.Errorf("%s 被誤擋：%v", s, err)
		}
	}
}

// GRD-R4：allow_private 只給「起點本身就在 allowlist」的請求使用。外部的 feed 或音檔主機
// 302 到 allowlist 的網段時，那一跳以預設規則檢查（被擋），notesteme 不會替外部主機對內網發 GET。
func TestRedirectFromPublicHostCannotUseAllowlist(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "evil.example":
			http.Redirect(w, r, "http://10.0.0.5/router-admin?reboot=1", http.StatusFound)
		case "nas.lan":
			http.Redirect(w, r, "http://10.0.0.5/feed.xml", http.StatusFound)
		default:
			hits++
			_, _ = w.Write([]byte("internal"))
		}
	}))
	defer srv.Close()
	resolver := stubResolverFunc(func(host string) []netip.Addr {
		if host == "nas.lan" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.7")}
		}
		return []netip.Addr{publicIP}
	})
	newClient := func() (*http.Client, *recDialer) {
		d := &recDialer{addr: srv.Listener.Addr().String()}
		return NewClient(Deps{Resolver: resolver, Dialer: d,
			AllowPrivate: Allowlist{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}}), d
	}

	c, d := newClient()
	resp, err := c.Get("http://evil.example/feed.xml")
	if err == nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrBlockedAddress) || hits != 0 {
		t.Fatalf("公開主機轉到 allowlist 的網段：err = %v，內網收到 %d 次請求", err, hits)
	}
	for _, tg := range d.targets {
		if tg.IP.IsPrivate() {
			t.Errorf("連到了內網：%+v", tg)
		}
	}

	// 起點本身在 allowlist（解析到 10.0.0.7）時，轉到 allowlist 內的另一個位址照常可用（每一跳仍重新檢查）。
	c, _ = newClient()
	resp, err = c.Get("http://nas.lan/rss")
	if err != nil {
		t.Fatalf("allowlist 內的轉址：%v", err)
	}
	resp.Body.Close()
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
	// 起點直接用 allowlist 內的 IP 也可以。
	c, _ = newClient()
	if resp, err = c.Get("http://10.0.0.5/direct"); err != nil {
		t.Fatalf("直接連 allowlist 內的 IP：%v", err)
	}
	resp.Body.Close()

	// 調整逾時的複本（轉錄、LLM provider 用）保留同樣的轉址限制。
	c, _ = newClient()
	for _, rt := range []http.RoundTripper{
		c.Transport.(*schemeTransport).WithResponseHeaderTimeout(time.Minute),
		c.Transport.(*schemeTransport).WithTimeouts(time.Minute, time.Minute),
	} {
		cc := &http.Client{Transport: rt}
		resp, err = cc.Get("http://evil.example/feed.xml")
		if err == nil {
			resp.Body.Close()
		}
		if !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("複本的轉址：%v", err)
		}
	}

	// 同一個 client 先連過內網（連線池裡有 10.0.0.5 的連線），之後的外部轉址也不能沿用那條連線。
	c, _ = newClient()
	if resp, err = c.Get("http://10.0.0.5/warm"); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	before := hits
	resp, err = c.Get("http://evil.example/feed.xml")
	if err == nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrBlockedAddress) || hits != before {
		t.Fatalf("沿用連線池轉到內網：err = %v，hits %d → %d", err, before, hits)
	}
}
