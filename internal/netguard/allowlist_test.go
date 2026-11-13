package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

func TestParseAllowlistEntries(t *testing.T) {
	got, err := ParseAllowlist([]string{
		"192.168.0.0/16", "10.1.2.3/8", // CIDR 會遮罩
		"10.0.0.5", "fd00::1", "::ffff:10.0.0.9", // 單一 IP 轉成 /32、/128
		"RSSHub.LAN", "nas.example.com.", " feeds.home ", // 主機名稱：小寫、去掉結尾的點
		"169.254.0.0/16", // 網段包含 metadata 位址時可以接受
	})
	if err != nil {
		t.Fatal(err)
	}
	var prefixes []string
	for _, p := range got.Prefixes {
		prefixes = append(prefixes, p.String())
	}
	wantPrefixes := []string{"192.168.0.0/16", "10.0.0.0/8", "10.0.0.5/32", "fd00::1/128", "10.0.0.9/32", "169.254.0.0/16"}
	if !slices.Equal(prefixes, wantPrefixes) {
		t.Errorf("prefixes = %v, want %v", prefixes, wantPrefixes)
	}
	if want := []string{"rsshub.lan", "nas.example.com", "feeds.home"}; !slices.Equal(got.Hosts, want) {
		t.Errorf("hosts = %v, want %v", got.Hosts, want)
	}
}

func TestParseAllowlistEmpty(t *testing.T) {
	got, err := ParseAllowlist(nil)
	if err != nil || len(got.Prefixes) != 0 || len(got.Hosts) != 0 {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
}

func TestParseAllowlistRejectsMetadataEntries(t *testing.T) {
	for _, v := range []string{"169.254.169.254", "169.254.169.254/32", "fd00:ec2::254", "::ffff:169.254.169.254", "100.100.100.200"} {
		_, err := ParseAllowlist([]string{"10.0.0.1", v})
		var ee *EntryError
		if !errors.As(err, &ee) || ee.Index != 1 || !ee.Metadata.IsValid() {
			t.Errorf("%s：err = %v，應為 metadata 的 *EntryError（index 1）", v, err)
		}
	}
}

func TestParseAllowlistRejectsInvalidEntries(t *testing.T) {
	for _, v := range []string{"", "*.lan", "*", "-bad.lan", "bad-.lan", "a..b", "under_score.lan", "http://x.lan", "x.lan:80", "1.2.3", "10.0.0.256", "fe80::1%eth0", strings.Repeat("a", 64) + ".lan"} {
		_, err := ParseAllowlist([]string{v})
		var ee *EntryError
		if !errors.As(err, &ee) || ee.Index != 0 || ee.Metadata.IsValid() {
			t.Errorf("%q：err = %v，應為一般的 *EntryError", v, err)
		}
	}
}

func TestEntryErrorMessages(t *testing.T) {
	_, err := ParseAllowlist([]string{"169.254.169.254"})
	want := "allowlist[0]：169.254.169.254 是雲端 metadata 位址，一律封鎖，不能加入 allowlist。"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %s", err, want)
	}
}

// 判斷順序：metadata → IP 網段 → 主機名稱 → 預設規則。
func TestCheckIPHostAllowlist(t *testing.T) {
	allow := Allowlist{Hosts: []string{"rsshub.lan"}}
	priv := netip.MustParseAddr("192.168.1.20")
	for host, wantOK := range map[string]bool{
		"rsshub.lan":      true,
		"RSSHub.LAN":      true,  // 不分大小寫
		"rsshub.lan.":     true,  // 結尾的點
		"evil.rsshub.lan": false, // 不支援萬用字元，也不是子網域比對
		"lan":             false,
		"":                false,
	} {
		err := checkIP(host, priv, allow)
		if (err == nil) != wantOK {
			t.Errorf("host %q：err = %v，want ok = %v", host, err, wantOK)
		}
	}
	// metadata 位址不受主機名稱 allowlist 影響。
	for _, s := range []string{"169.254.169.254", "fd00:ec2::254", "::ffff:169.254.169.254", "64:ff9b::a9fe:a9fe"} {
		if err := checkIP("rsshub.lan", netip.MustParseAddr(s), allow); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s：err = %v，metadata 仍要擋下", s, err)
		}
	}
	// NAT64 內嵌的私有位址：主機名稱在 allowlist 時同樣放行。
	if err := checkIP("rsshub.lan", netip.MustParseAddr("64:ff9b::c0a8:114"), allow); err != nil {
		t.Errorf("NAT64 內嵌 192.168.1.20：%v", err)
	}
}

func TestDialUsesTargetHostForAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	d := &recDialer{addr: srv.Listener.Addr().String()}
	deps := Deps{
		Resolver:     stubResolver{ips: []netip.Addr{netip.MustParseAddr("192.168.1.20")}},
		Dialer:       d,
		AllowPrivate: Allowlist{Hosts: []string{"rsshub.lan"}},
	}
	resp, err := (&http.Client{Transport: Transport(deps)}).Get("http://RSSHub.lan/feed")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// 仍然只連剛檢查過的那個 IP，Target.Host 保留網址中的主機名稱。
	if len(d.targets) != 1 || d.targets[0].IP.String() != "192.168.1.20" {
		t.Fatalf("targets = %+v", d.targets)
	}
	// 同一個內網 IP，換一個不在名單中的主機名稱就被擋下。
	if _, err := (&http.Client{Transport: Transport(deps)}).Get("http://other.lan/feed"); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("err = %v", err)
	}
}

// 轉址每一跳都重新檢查：allowlist 的主機轉址到內網的其他位址會被擋下。
func TestAllowlistedHostRedirectToPrivateIsBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.1.1/admin", http.StatusFound)
	}))
	defer srv.Close()
	d := &recDialer{addr: srv.Listener.Addr().String()}
	c := NewClient(Deps{
		Resolver:     stubResolver{ips: []netip.Addr{netip.MustParseAddr("192.168.1.20")}},
		Dialer:       d,
		AllowPrivate: Allowlist{Hosts: []string{"rsshub.lan"}},
	})
	_, err := c.Get("http://rsshub.lan/feed")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v", err)
	}
	if len(d.targets) != 1 {
		t.Errorf("只有第一跳會連線：%+v", d.targets)
	}
}

// 主機名稱解析出多個 IP 時，只有全部被擋才算 ErrBlockedAddress。
func TestDialBlockedOnlyWhenAllAddressesBlocked(t *testing.T) {
	good := netip.MustParseAddr("93.184.216.34")
	priv := netip.MustParseAddr("10.0.0.5")
	d := &recDialer{addr: "127.0.0.1:1", failFor: map[netip.Addr]bool{good: true}}

	_, err := (&http.Client{Transport: Transport(Deps{Resolver: stubResolver{ips: []netip.Addr{priv, good}}, Dialer: d})}).Get("http://mixed.example/")
	if err == nil || errors.Is(err, ErrBlockedAddress) {
		t.Errorf("一個被擋、一個連線失敗：err = %v，不應算被封鎖", err)
	}
	if len(d.targets) != 1 || d.targets[0].IP != good {
		t.Errorf("只應連到通過檢查的 IP：%+v", d.targets)
	}

	_, err = (&http.Client{Transport: Transport(Deps{Resolver: stubResolver{ips: []netip.Addr{priv, netip.MustParseAddr("fe80::1")}}, Dialer: d})}).Get("http://allblocked.example/")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("全部被擋：err = %v", err)
	}
}
