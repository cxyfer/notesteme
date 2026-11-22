package netguard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// seqResolver 依序回傳每次解析的結果（用來模擬 DNS rebinding）；超過時重複最後一筆。
type seqResolver struct {
	answers [][]netip.Addr
	calls   int
}

func (s *seqResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	i := s.calls
	if i >= len(s.answers) {
		i = len(s.answers) - 1
	}
	s.calls++
	return s.answers[i], nil
}

func ips(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

func TestCheckURL(t *testing.T) {
	public, private, meta := "93.184.216.34", "192.168.1.1", "169.254.169.254"
	dnsErr := errors.New("nxdomain")
	tests := []struct {
		name        string
		url         string
		resolver    Resolver
		allow       Allowlist
		legacyAllow []netip.Prefix
		blocked     bool  // 預期 ErrBlockedAddress
		wantErr     error // 預期的其他錯誤（errors.Is）
		ok          bool
	}{
		{name: "公開位址", url: "https://media.example.com/a.mp3", resolver: stubResolver{ips: ips(public)}, ok: true},
		{name: "全部私有", url: "https://media.example.com/a.mp3", resolver: stubResolver{ips: ips(private, "10.0.0.5")}, blocked: true},
		{name: "混合：有一個公開就放行", url: "https://media.example.com/a.mp3", resolver: stubResolver{ips: ips(private, public)}, ok: true},
		{name: "metadata 位址", url: "http://media.example.com/a.mp3", resolver: stubResolver{ips: ips(meta)}, blocked: true},
		{name: "IP 字面值：私有", url: "http://192.168.1.1/a.mp3", resolver: stubResolver{err: errors.New("不應解析")}, blocked: true},
		{name: "IP 字面值：loopback IPv6", url: "http://[::1]:8080/a.mp3", resolver: stubResolver{err: errors.New("不應解析")}, blocked: true},
		{name: "IP 字面值：公開", url: "https://93.184.216.34/a.mp3", resolver: stubResolver{err: errors.New("不應解析")}, ok: true},
		{name: "IP 字面值：metadata", url: "http://169.254.169.254/latest", resolver: stubResolver{}, blocked: true},
		{name: "allowlist 網段", url: "https://nas.lan/a.mp3", resolver: stubResolver{ips: ips(private)},
			allow: Allowlist{Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}, ok: true},
		{name: "allowlist 主機", url: "https://nas.lan/a.mp3", resolver: stubResolver{ips: ips(private)},
			allow: Allowlist{Hosts: []string{"nas.lan"}}, ok: true},
		{name: "舊的 Allowlist 欄位", url: "https://nas.lan/a.mp3", resolver: stubResolver{ips: ips(private)},
			legacyAllow: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}, ok: true},
		{name: "allowlist 開不了 metadata", url: "https://nas.lan/a.mp3", resolver: stubResolver{ips: ips(meta)},
			allow: Allowlist{Hosts: []string{"nas.lan"}, Prefixes: []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16")}}, blocked: true},
		{name: "scheme 不是 http", url: "ftp://media.example.com/a.mp3", resolver: stubResolver{ips: ips(public)}, wantErr: ErrUnsupportedScheme},
		{name: "沒有 scheme", url: "//media.example.com/a.mp3", resolver: stubResolver{ips: ips(public)}, wantErr: ErrUnsupportedScheme},
		{name: "DNS 錯誤", url: "https://nope.example.com/a.mp3", resolver: stubResolver{err: dnsErr}, wantErr: dnsErr},
		{name: "沒有位址", url: "https://nope.example.com/a.mp3", resolver: stubResolver{}},
		{name: "沒有 Resolver", url: "https://media.example.com/a.mp3", resolver: nil, wantErr: ErrNoDialer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckURL(context.Background(), Deps{Resolver: tt.resolver, AllowPrivate: tt.allow, Allowlist: tt.legacyAllow}, tt.url)
			switch {
			case tt.ok:
				if err != nil {
					t.Fatalf("應通過：%v", err)
				}
			case tt.blocked:
				if !errors.Is(err, ErrBlockedAddress) {
					t.Fatalf("應被擋：%v", err)
				}
				if msg, ok := PolicyMessage(err); !ok || msg != "這個網址指向內部網路，已封鎖。" {
					t.Errorf("PolicyMessage = %q, %v", msg, ok)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v，預期 %v", err, tt.wantErr)
				}
				if errors.Is(err, ErrBlockedAddress) {
					t.Error("不該算被擋")
				}
			default:
				if err == nil || errors.Is(err, ErrBlockedAddress) {
					t.Fatalf("應回傳非封鎖的錯誤：%v", err)
				}
			}
		})
	}
}

// CheckURL 不連線：只解析，不呼叫 Dialer（Deps 甚至不需要 Dialer）。
func TestCheckURLDoesNotDial(t *testing.T) {
	res := &seqResolver{answers: [][]netip.Addr{ips("93.184.216.34")}}
	if err := CheckURL(context.Background(), Deps{Resolver: res}, "https://media.example.com/a.mp3"); err != nil {
		t.Fatal(err)
	}
	if res.calls != 1 {
		t.Errorf("解析 %d 次，預期 1 次", res.calls)
	}
}

// GRD-R2：第一次解析是公開位址、之後換成私有位址（DNS rebinding）。連線時重新解析並檢查，
// 第二次 dial 被擋下，Dialer 不會收到私有 IP；預檢通過也不能取代連線時的檢查。
func TestTransportRechecksAfterRebinding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer srv.Close()

	d := &recDialer{addr: srv.Listener.Addr().String()}
	res := &seqResolver{answers: [][]netip.Addr{ips("93.184.216.34"), ips("192.168.1.1")}}
	tr := Transport(Deps{Resolver: res, Dialer: d})
	c := &http.Client{Transport: tr}

	resp, err := c.Get("http://rebind.example.com/a.mp3")
	if err != nil {
		t.Fatalf("第一次應通過：%v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	// 關掉 keep-alive 的連線，第二個請求才會重新 dial（重新解析）。
	tr.CloseIdleConnections()

	_, err = c.Get("http://rebind.example.com/a.mp3")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("第二次應被擋：%v", err)
	}
	if res.calls != 2 {
		t.Errorf("解析 %d 次，預期每次 dial 都重新解析（2 次）", res.calls)
	}
	if len(d.targets) != 1 || d.targets[0].IP.String() != "93.184.216.34" {
		t.Fatalf("Dialer 收到的目標 = %v，只該有第一次的公開 IP", d.targets)
	}
	for _, tg := range d.targets {
		if tg.IP.IsPrivate() {
			t.Errorf("Dialer 收到私有 IP：%v", tg)
		}
	}
}
