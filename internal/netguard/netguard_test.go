package netguard

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

type stubResolver struct {
	ips []netip.Addr
	err error
}

func (s stubResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return s.ips, s.err
}

// recDialer 記錄收到的 Target，並連到固定的本機位址。
type recDialer struct {
	targets []Target
	addr    string
	failFor map[netip.Addr]bool
}

func (d *recDialer) Dial(ctx context.Context, t Target) (net.Conn, error) {
	d.targets = append(d.targets, t)
	if d.failFor[t.IP] {
		return nil, errors.New("refused")
	}
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.addr)
}

func TestTransportResolvesThenDials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	d := &recDialer{addr: srv.Listener.Addr().String()}
	tr := Transport(Deps{
		Resolver: stubResolver{ips: []netip.Addr{netip.MustParseAddr("93.184.216.34")}},
		Dialer:   d,
	})
	c := &http.Client{Transport: tr}
	resp, err := c.Get("http://example.com:8080/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(d.targets) != 1 {
		t.Fatalf("targets = %v", d.targets)
	}
	got := d.targets[0]
	if got.Network != "tcp" || got.Host != "example.com" || got.Port != 8080 || got.IP.String() != "93.184.216.34" {
		t.Fatalf("target = %+v", got)
	}
	if got.Addr() != "93.184.216.34:8080" {
		t.Fatalf("Addr = %s", got.Addr())
	}
}

func TestTransportIPLiteralSkipsResolver(t *testing.T) {
	d := &recDialer{addr: "127.0.0.1:1", failFor: map[netip.Addr]bool{netip.MustParseAddr("1.2.3.4"): true}}
	tr := Transport(Deps{Resolver: stubResolver{err: errors.New("不應解析")}, Dialer: d})
	_, err := (&http.Client{Transport: tr}).Get("http://1.2.3.4/")
	if err == nil {
		t.Fatal("應失敗")
	}
	if len(d.targets) != 1 || d.targets[0].Host != "1.2.3.4" {
		t.Fatalf("targets = %v", d.targets)
	}
}

func TestTransportTriesNextIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	bad, good := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("93.184.216.34")
	d := &recDialer{addr: srv.Listener.Addr().String(), failFor: map[netip.Addr]bool{bad: true}}
	tr := Transport(Deps{Resolver: stubResolver{ips: []netip.Addr{bad, good}}, Dialer: d})
	resp, err := (&http.Client{Transport: tr}).Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(d.targets) != 2 {
		t.Fatalf("targets = %v", d.targets)
	}
}

func TestTransportErrors(t *testing.T) {
	d := &recDialer{addr: "127.0.0.1:1"}
	cases := map[string]Deps{
		"resolver error": {Resolver: stubResolver{err: errors.New("nxdomain")}, Dialer: d},
		"no addresses":   {Resolver: stubResolver{}, Dialer: d},
	}
	for name, deps := range cases {
		_, err := (&http.Client{Transport: Transport(deps)}).Get("http://example.com/")
		if err == nil {
			t.Errorf("%s: 應失敗", name)
		}
	}
	if len(d.targets) != 0 {
		t.Fatalf("不應呼叫 Dialer: %v", d.targets)
	}
}

func TestDialInvalidAddr(t *testing.T) {
	if _, err := dial(context.Background(), stubResolver{}, &recDialer{}, "tcp", "no-port"); err == nil {
		t.Fatal("缺少 port 應失敗")
	}
	if _, err := dial(context.Background(), stubResolver{}, &recDialer{}, "tcp", "a.test:99999"); err == nil {
		t.Fatal("port 超出範圍應失敗")
	}
}

func TestTransportRefusesWithoutDeps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	d := &recDialer{addr: srv.Listener.Addr().String()}
	cases := map[string]Deps{
		"全部為 nil":    {},
		"缺 Dialer":   {Resolver: stubResolver{ips: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}},
		"缺 Resolver": {Dialer: d},
	}
	for name, deps := range cases {
		// IP 字面值（指向真實可連的 httptest server）也不能退回系統 dialer。
		for _, url := range []string{srv.URL, "http://example.com/"} {
			resp, err := (&http.Client{Transport: Transport(deps)}).Get(url)
			if err == nil {
				resp.Body.Close()
				t.Fatalf("%s %s：應失敗", name, url)
			}
			if !errors.Is(err, ErrNoDialer) {
				t.Errorf("%s %s：err = %v，應為 ErrNoDialer", name, url, err)
			}
		}
	}
	if len(d.targets) != 0 {
		t.Fatalf("不應呼叫 Dialer: %v", d.targets)
	}
}

func TestSystemDeps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	deps := SystemDeps()
	if deps.Resolver == nil || deps.Dialer == nil {
		t.Fatalf("SystemDeps 應提供 Resolver 與 Dialer: %+v", deps)
	}
	// IP 字面值走系統 Dialer，不需要 DNS。
	resp, err := (&http.Client{Transport: Transport(deps)}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
