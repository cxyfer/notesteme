package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/netguard"
)

type fixedResolver struct{ ip netip.Addr }

func (r fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{r.ip}, nil
}

// toDialer 不管 Target 是什麼，都連到測試 server。
type toDialer struct {
	addr    string
	targets []netguard.Target
}

func (d *toDialer) Dial(ctx context.Context, t netguard.Target) (net.Conn, error) {
	d.targets = append(d.targets, t)
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.addr)
}

func chainServer(t *testing.T, redirects int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var i int
		_, _ = fmt.Sscanf(r.URL.Path, "/%d", &i)
		if i < redirects {
			http.Redirect(w, r, fmt.Sprintf("/%d", i+1), http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv
}

// GRD-R4：最多轉址 5 次。5 次可以跟隨，第 6 次才拒絕。
func TestOutboundClientAllowsExactlyFiveRedirects(t *testing.T) {
	for redirects, wantOK := range map[int]bool{4: true, 5: true, 6: false} {
		srv := chainServer(t, redirects)
		d := &toDialer{addr: srv.Listener.Addr().String()}
		c := netguard.NewClient(netguard.Deps{Resolver: fixedResolver{ip: netip.MustParseAddr("93.184.216.34")}, Dialer: d})
		resp, err := c.Get("http://example.com/0")
		if resp != nil {
			resp.Body.Close()
		}
		if (err == nil) != wantOK {
			t.Errorf("%d 次轉址：err = %v，want ok = %v", redirects, err, wantOK)
		}
	}
}

// 設定檔的 allowlist 併入 server 的 netguard 依賴；env 原本提供的項目保留。
func TestNetDepsMergesConfigAllowlist(t *testing.T) {
	base := netguard.Deps{AllowPrivate: netguard.Allowlist{
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Hosts: []string{"env.lan"},
	}}
	cfg := &config.Config{AllowPrivate: netguard.Allowlist{
		Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}, Hosts: []string{"rsshub.lan"},
	}}
	got := netDeps(base, cfg)
	if want := []string{"env.lan", "rsshub.lan"}; !slices.Equal(got.AllowPrivate.Hosts, want) {
		t.Errorf("hosts = %v, want %v", got.AllowPrivate.Hosts, want)
	}
	if len(got.AllowPrivate.Prefixes) != 2 {
		t.Errorf("prefixes = %v", got.AllowPrivate.Prefixes)
	}
	if len(base.AllowPrivate.Hosts) != 1 || len(base.AllowPrivate.Prefixes) != 1 {
		t.Errorf("不應改動呼叫端的 Deps：%+v", base.AllowPrivate)
	}
}

// 端到端：設定檔 network.allow_private 讓 server 的對外 client 連得到自架的內網主機。
func TestServerOutboundHonorsConfigAllowlist(t *testing.T) {
	srv := chainServer(t, 0)
	private := netip.MustParseAddr("192.168.1.20")
	dialer := &toDialer{addr: srv.Listener.Addr().String()}
	cfg := &config.Config{AllowPrivate: netguard.Allowlist{Hosts: []string{"rsshub.lan"}}}
	c := netguard.NewClient(netDeps(netguard.Deps{Resolver: fixedResolver{ip: private}, Dialer: dialer}, cfg))

	resp, err := c.Get("http://rsshub.lan/0")
	if err != nil {
		t.Fatalf("allowlist 內的主機：%v", err)
	}
	resp.Body.Close()
	if _, err := c.Get("http://other.lan/0"); err == nil {
		t.Error("不在 allowlist 的主機解析到內網位址，應被擋下")
	}
}
