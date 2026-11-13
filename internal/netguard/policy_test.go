package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestCheckIPBlocksNonPublicAddresses(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.255.255.254", "::1", // loopback
		"10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1", // RFC 1918
		"169.254.0.1", "169.254.169.254", "fe80::1", // link-local
		"100.64.0.1", "100.127.255.254", // CGNAT
		"fc00::1", "fd12:3456::1", // unique local
		"0.0.0.0", "::", "0.1.2.3", // unspecified
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff05::2", // multicast
		"fd00:ec2::254", "100.100.100.200", // 雲端 metadata
		"::ffff:10.0.0.1", "::ffff:127.0.0.1", "::ffff:169.254.169.254", // IPv4-mapped
		"64:ff9b::a00:1", "64:ff9b::7f00:1", // NAT64 內嵌私有位址
		"255.255.255.255", "240.0.0.1",
	}
	for _, s := range blocked {
		if err := checkIP("", netip.MustParseAddr(s), Allowlist{}); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s 應被擋下：%v", s, err)
		}
	}
	allowed := []string{
		"93.184.216.34", "8.8.8.8", "1.1.1.1", "172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1",
		"2606:2800:220:1:248:1893:25c8:1946", "64:ff9b::808:808", "::ffff:8.8.8.8",
	}
	for _, s := range allowed {
		if err := checkIP("", netip.MustParseAddr(s), Allowlist{}); err != nil {
			t.Errorf("%s 應放行：%v", s, err)
		}
	}
}

func TestAllowlistOpensRangesButNeverMetadata(t *testing.T) {
	allow := Allowlist{Prefixes: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fd00::/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
	}}
	for _, s := range []string{"10.1.2.3", "169.254.1.1", "fd00::1", "100.64.0.1"} {
		if err := checkIP("", netip.MustParseAddr(s), allow); err != nil {
			t.Errorf("%s 在 allowlist 內，應放行：%v", s, err)
		}
	}
	for _, s := range []string{"169.254.169.254", "fd00:ec2::254", "100.100.100.200", "::ffff:169.254.169.254"} {
		if err := checkIP("", netip.MustParseAddr(s), allow); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("metadata 位址 %s 不能被 allowlist 開放：%v", s, err)
		}
	}
	if err := checkIP("", netip.MustParseAddr("192.168.1.1"), allow); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("不在 allowlist 內仍要擋下：%v", err)
	}
}

// 被擋下的位址不會交給 Dialer；字面值與解析結果都一樣。
func TestTransportBlocksBeforeDialing(t *testing.T) {
	for name, tc := range map[string]struct {
		url string
		ips []netip.Addr
	}{
		"字面值 loopback":      {url: "http://127.0.0.1/"},
		"字面值 metadata":      {url: "http://169.254.169.254/latest/meta-data/"},
		"字面值 IPv6 loopback": {url: "http://[::1]:8080/"},
		"字面值 IPv4-mapped":   {url: "http://[::ffff:10.0.0.1]/"},
		"主機解析到私有位址":         {url: "http://internal.example/", ips: []netip.Addr{netip.MustParseAddr("10.0.0.5")}},
		"主機解析到全是內網位址":       {url: "http://rebind.example/", ips: []netip.Addr{netip.MustParseAddr("192.168.0.1"), netip.MustParseAddr("fe80::1")}},
	} {
		t.Run(name, func(t *testing.T) {
			d := &recDialer{addr: "127.0.0.1:1"}
			tr := Transport(Deps{Resolver: stubResolver{ips: tc.ips}, Dialer: d})
			resp, err := (&http.Client{Transport: tr}).Get(tc.url)
			if err == nil {
				resp.Body.Close()
				t.Fatal("應被擋下")
			}
			if !errors.Is(err, ErrBlockedAddress) {
				t.Errorf("err = %v，應符合 ErrBlockedAddress", err)
			}
			if len(d.targets) != 0 {
				t.Errorf("不應呼叫 Dialer：%v", d.targets)
			}
		})
	}
}

// 解析結果混有內網與公開位址時，只連公開的那個。
func TestTransportSkipsBlockedAddressesAndDialsPublicOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	d := &recDialer{addr: srv.Listener.Addr().String()}
	public := netip.MustParseAddr("93.184.216.34")
	tr := Transport(Deps{Resolver: stubResolver{ips: []netip.Addr{netip.MustParseAddr("10.0.0.1"), public}}, Dialer: d})
	resp, err := (&http.Client{Transport: tr}).Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(d.targets) != 1 || d.targets[0].IP != public {
		t.Fatalf("targets = %v", d.targets)
	}
}

// 轉址的每一跳都經過同一個 dial：公開位址轉址到內網位址會被擋。
func TestRedirectToPrivateAddressIsBlocked(t *testing.T) {
	for _, target := range []string{"http://10.0.0.5/secret", "http://169.254.169.254/latest/meta-data/", "http://localhost:9/x"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		}))
		d := &recDialer{addr: srv.Listener.Addr().String()}
		res := stubResolverFunc(func(host string) []netip.Addr {
			if host == "localhost" {
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}
		})
		client := &http.Client{Transport: Transport(Deps{Resolver: res, Dialer: d})}
		resp, err := client.Get("http://public.example/start")
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%s：轉址到內網應被擋下", target)
		}
		if !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s：err = %v，應符合 ErrBlockedAddress", target, err)
		}
		if len(d.targets) != 1 || d.targets[0].Host != "public.example" {
			t.Errorf("%s：只有第一跳會連線：%v", target, d.targets)
		}
		srv.Close()
	}
}

type stubResolverFunc func(host string) []netip.Addr

func (f stubResolverFunc) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return f(host), nil
}

func TestTransportAllowlistPermitsConfiguredRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	d := &recDialer{addr: srv.Listener.Addr().String()}
	tr := Transport(Deps{
		Resolver:  stubResolver{ips: []netip.Addr{netip.MustParseAddr("10.0.0.5")}},
		Dialer:    d,
		Allowlist: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
	})
	resp, err := (&http.Client{Transport: tr}).Get("http://nas.lan/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// 同一份 allowlist 仍然擋 metadata。
	if _, err := (&http.Client{Transport: Transport(Deps{Dialer: d, Resolver: stubResolver{}, Allowlist: []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16")}})}).Get("http://169.254.169.254/"); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("err = %v", err)
	}
}

// 每次連線嘗試有 10 秒逾時；逾時透過 context 傳給 Dialer，測試不需要真的等待。
func TestDialPassesTimeoutToDialer(t *testing.T) {
	var deadline time.Time
	var ok bool
	d := dialerFunc(func(ctx context.Context, _ Target) (net.Conn, error) {
		deadline, ok = ctx.Deadline()
		return nil, errors.New("refused")
	})
	start := time.Now()
	_, _ = dial(context.Background(), stubResolver{ips: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}, d, Allowlist{}, "tcp", "example.com:80")
	if !ok {
		t.Fatal("Dialer 的 ctx 應有 deadline")
	}
	if got := deadline.Sub(start); got < 9*time.Second || got > 11*time.Second {
		t.Errorf("逾時 = %v，預期約 10 秒", got)
	}
}

type dialerFunc func(ctx context.Context, t Target) (net.Conn, error)

func (f dialerFunc) Dial(ctx context.Context, t Target) (net.Conn, error) { return f(ctx, t) }
