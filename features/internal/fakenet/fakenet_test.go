package fakenet

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/netguard"
)

func hello(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body+" host="+r.Host)
	})
}

func get(t *testing.T, c *http.Client, url string) (string, error) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func newClient(n *Net) *http.Client {
	return &http.Client{Transport: netguard.Transport(n.Deps()), Timeout: 10 * time.Second}
}

func TestResolvesSequenceAndDefaults(t *testing.T) {
	n := New()
	defer n.Close()
	if err := n.Resolves("rebind.test", "93.184.216.34", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	n.Handle("example.com", hello("x"))

	want := []string{"93.184.216.34", "127.0.0.1", "127.0.0.1"}
	for i, w := range want {
		got, err := n.LookupNetIP(context.Background(), "ip", "rebind.test")
		if err != nil || len(got) != 1 || got[0] != netip.MustParseAddr(w) {
			t.Fatalf("lookup %d = %v, %v; want %s", i, got, err, w)
		}
	}
	got, err := n.LookupNetIP(context.Background(), "ip", "Example.COM.")
	if err != nil || got[0] != DefaultIP {
		t.Fatalf("預設 IP = %v, %v", got, err)
	}
	// ip6 查詢不應回傳 IPv4。
	if got, _ := n.LookupNetIP(context.Background(), "ip6", "example.com"); len(got) != 0 {
		t.Fatalf("ip6 = %v", got)
	}
}

func TestNXDOMAIN(t *testing.T) {
	n := New()
	_, err := n.LookupNetIP(context.Background(), "ip", "nope.invalid")
	var dnsErr *net.DNSError
	if !errorsAs(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Fatalf("err = %v, want NXDOMAIN", err)
	}
	if len(n.Violations()) != 0 {
		t.Fatalf("NXDOMAIN 不算違規: %v", n.Violations())
	}
}

func errorsAs(err error, target **net.DNSError) bool {
	e, ok := err.(*net.DNSError)
	if ok {
		*target = e
	}
	return ok
}

func TestResolvesInvalidIP(t *testing.T) {
	n := New()
	if err := n.Resolves("a.test", "not-an-ip"); err == nil {
		t.Fatal("應回傳錯誤")
	}
	if err := n.Resolves("a.test"); err == nil {
		t.Fatal("沒有 IP 應回傳錯誤")
	}
}

func TestHTTPAndHTTPSThroughNetguard(t *testing.T) {
	n := New()
	defer n.Close()
	n.Handle("example.com", hello("hi"))
	c := newClient(n)

	for _, url := range []string{"http://example.com/a", "https://example.com/a"} {
		body, err := get(t, c, url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		if body != "hi host=example.com" {
			t.Fatalf("GET %s body = %q", url, body)
		}
	}
	if !n.ConnectedTo("example.com") || !n.ConnectedTo("93.184.216.34") {
		t.Fatalf("應有連線紀錄: %v", n.Connections())
	}
	if n.ConnectedTo("127.0.0.1") || n.ConnectedTo("other.test") {
		t.Fatal("不應有其他連線紀錄")
	}
	if len(n.Violations()) != 0 {
		t.Fatalf("violations = %v", n.Violations())
	}
	if len(n.Lookups()) == 0 {
		t.Fatal("應有解析紀錄")
	}
}

func TestHTTPSRejectsUntrustedCA(t *testing.T) {
	n := New()
	defer n.Close()
	n.Handle("example.com", hello("hi"))
	// 沒有帶測試 CA 的 client 應無法驗證憑證。
	d := n.Deps()
	d.RootCAs = nil
	c := &http.Client{Transport: netguard.Transport(d), Timeout: 10 * time.Second}
	if _, err := get(t, c, "https://example.com/"); err == nil {
		t.Fatal("未信任測試 CA 時應失敗")
	}
}

func TestIPLiteralDial(t *testing.T) {
	n := New()
	defer n.Close()
	n.Handle("example.com", hello("hi"))
	body, err := get(t, newClient(n), "http://93.184.216.34/")
	if err != nil || !strings.HasPrefix(body, "hi") {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}

func TestUnregisteredTargetIsViolation(t *testing.T) {
	n := New()
	defer n.Close()
	n.Handle("example.com", hello("hi"))
	// 主機解析到沒有登記 origin 的內網位址：Dialer 被呼叫時記為違規。
	if err := n.Resolves("internal.test", "10.0.0.5"); err != nil {
		t.Fatal(err)
	}
	// netguard 預設就會擋下內網位址；這裡 allowlist 10.0.0.0/8，讓請求到得了 Dialer，才能測 fakenet 自己的違規記錄。
	d := n.Deps()
	d.Allowlist = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	c := &http.Client{Transport: netguard.Transport(d), Timeout: 10 * time.Second}
	if _, err := get(t, c, "http://internal.test/"); err == nil {
		t.Fatal("應失敗")
	}
	v := n.Violations()
	if len(v) != 1 || !strings.Contains(v[0], "10.0.0.5") {
		t.Fatalf("violations = %v", v)
	}
	if !n.ConnectedTo("10.0.0.5") || !n.ConnectedTo("internal.test") {
		t.Fatal("連線嘗試應被記錄")
	}

	// 已登記主機的 IP 不在登記清單內（例如 rebinding 到 127.0.0.1 但沒登記）。
	_, err := n.Dial(context.Background(), netguard.Target{
		Network: "tcp", Host: "example.com", IP: netip.MustParseAddr("127.0.0.1"), Port: 80,
	})
	if err == nil || len(n.Violations()) != 2 {
		t.Fatalf("err = %v, violations = %v", err, n.Violations())
	}
}

func TestRehandleTakesEffect(t *testing.T) {
	n := New()
	defer n.Close()
	n.Handle("example.com", hello("one"))
	c := newClient(n)
	if b, _ := get(t, c, "http://example.com/"); !strings.HasPrefix(b, "one") {
		t.Fatalf("body = %q", b)
	}
	n.Handle("example.com", hello("two"))
	c.CloseIdleConnections()
	if b, _ := get(t, c, "http://example.com/"); !strings.HasPrefix(b, "two") {
		t.Fatalf("body = %q", b)
	}
}

func TestTrapBlocksDefaultTransportAndResolver(t *testing.T) {
	origTransport, origResolver := http.DefaultTransport, net.DefaultResolver
	DrainTrapViolations()
	restore := InstallTrap()

	if _, err := http.Get("http://example.com"); err == nil { //nolint:noctx
		t.Fatal("trap 生效時 http.Get 應失敗")
	}
	if _, err := net.DefaultResolver.LookupHost(context.Background(), "example.com"); err == nil {
		t.Fatal("trap 生效時 DefaultResolver 應失敗")
	}
	v := TrapViolations()
	if len(v) < 2 {
		t.Fatalf("violations = %v", v)
	}
	var sawHTTP, sawDNS bool
	for _, s := range v {
		sawHTTP = sawHTTP || strings.Contains(s, "http://example.com")
		sawDNS = sawDNS || strings.Contains(s, "DNS lookup")
	}
	if !sawHTTP || !sawDNS {
		t.Fatalf("violations = %v", v)
	}

	// trap 生效時，經過 netguard 的已登記主機仍可連線。
	n := New()
	defer n.Close()
	n.Handle("example.com", hello("ok"))
	for _, url := range []string{"http://example.com/", "https://example.com/"} {
		if _, err := get(t, newClient(n), url); err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
	}

	restore()
	if http.DefaultTransport != origTransport || net.DefaultResolver != origResolver {
		t.Fatal("restore 應還原全域設定")
	}
	if got := DrainTrapViolations(); len(got) != len(v) {
		t.Fatalf("drain = %v, want %d items", got, len(v))
	}
	if len(TrapViolations()) != 0 {
		t.Fatal("drain 後應清空")
	}
}

func TestLookupFilteredEmptyDoesNotAdvanceSequence(t *testing.T) {
	n := New()
	defer n.Close()
	if err := n.Resolves("rebind.test", "93.184.216.34", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// ip6 過濾後為空：不應推進序列，也不記錄成一次解析。
	for i := 0; i < 3; i++ {
		if got, err := n.LookupNetIP(ctx, "ip6", "rebind.test"); err != nil || len(got) != 0 {
			t.Fatalf("ip6 = %v, %v", got, err)
		}
	}
	if len(n.Lookups()) != 0 {
		t.Fatalf("過濾為空的查詢不應記錄: %v", n.Lookups())
	}
	for i, w := range []string{"93.184.216.34", "127.0.0.1"} {
		got, err := n.LookupNetIP(ctx, "ip4", "rebind.test")
		if err != nil || len(got) != 1 || got[0] != netip.MustParseAddr(w) {
			t.Fatalf("lookup %d = %v, %v; want %s", i, got, err, w)
		}
	}
}

func TestDialAfterCloseDoesNotRestartServer(t *testing.T) {
	n := New()
	n.Handle("example.com", hello("x"))
	if body, err := get(t, newClient(n), "http://example.com/"); err != nil || !strings.HasPrefix(body, "x") {
		t.Fatalf("body=%q err=%v", body, err)
	}
	n.Close()
	before := len(n.Connections())
	_, err := n.Dial(context.Background(), netguard.Target{Network: "tcp", Host: "example.com", IP: DefaultIP, Port: 80})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v，應為 ErrClosed", err)
	}
	for _, h := range n.hosts {
		if h.plain != nil || h.tlsSrv != nil {
			t.Fatal("Close 之後不應重新啟動 server")
		}
	}
	if len(n.Connections()) != before || len(n.Violations()) != 0 {
		t.Fatal("Close 之後的 Dial 不應留下連線或違規紀錄")
	}
}
