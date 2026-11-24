package hub

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// 安全修補 A：只有可信任的對端才採用 X-Forwarded-For，而且從右邊取第一個不可信任的位址（左邊可能是偽造的）。
func TestClientFromForwarded(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16"), netip.MustParsePrefix("10.0.0.1/32")}
	for _, c := range []struct {
		name, peer string
		xff        []string
		want       string
	}{
		{"沒有設定的對端不採用", "203.0.113.9:5000", []string{"198.51.100.1"}, ""},
		{"可信任的對端", "172.18.0.1:5000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"偽造的左邊不採用", "172.18.0.1:5000", []string{"1.1.1.1, 198.51.100.1"}, "198.51.100.1"},
		{"跳過可信任的代理", "172.18.0.1:5000", []string{"198.51.100.1, 10.0.0.1"}, "198.51.100.1"},
		{"多個標頭", "172.18.0.1:5000", []string{"9.9.9.9", "198.51.100.2"}, "198.51.100.2"},
		{"左邊無法解析", "172.18.0.1:5000", []string{"garbage, 198.51.100.3"}, "198.51.100.3"},
		{"右邊無法解析", "172.18.0.1:5000", []string{"198.51.100.3, garbage"}, ""},
		{"全部可信任", "172.18.0.1:5000", []string{"10.0.0.1, 172.18.0.5"}, "10.0.0.1"},
		{"沒有標頭", "172.18.0.1:5000", nil, ""},
		{"IPv6", "172.18.0.1:5000", []string{"[2001:db8::1]:443"}, "2001:db8::1"},
	} {
		got, ok := clientFromForwarded(c.peer, c.xff, trusted)
		if (c.want == "") != !ok || (ok && got.String() != c.want) {
			t.Errorf("%s：%v %v，預期 %q", c.name, got, ok, c.want)
		}
	}
}

func TestWithTrustedProxiesRewritesRemoteAddr(t *testing.T) {
	var seen string
	h := withTrustedProxies([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "198.51.100.7:0" {
		t.Fatalf("RemoteAddr = %q", seen)
	}
	r.RemoteAddr = "192.0.2.1:1234"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "192.0.2.1:1234" {
		t.Fatalf("不可信任的對端被改寫：%q", seen)
	}
	if withTrustedProxies(nil, h) == nil {
		t.Fatal("nil")
	}
}
