package fakenet

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func status(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRouteMatchesPathAndQuery(t *testing.T) {
	n := New()
	defer n.Close()
	if err := n.Route("https://feeds.example.com/a.xml", hello("A")); err != nil {
		t.Fatal(err)
	}
	if err := n.Route("https://feeds.example.com/a.xml?page=2", hello("A2")); err != nil {
		t.Fatal(err)
	}
	if err := n.Route("http://feeds.example.com", hello("root")); err != nil {
		t.Fatal(err)
	}
	c := newClient(n)

	for _, tc := range []struct {
		url, prefix string
		code        int
	}{
		{"https://feeds.example.com/a.xml", "A host=", 200},
		{"https://feeds.example.com/a.xml?page=2", "A2 host=", 200},
		{"https://feeds.example.com/", "root host=", 200},
		// 沒有登記的 path 或 query：404。
		{"https://feeds.example.com/b.xml", "", 404},
		{"https://feeds.example.com/a.xml?page=3", "", 404},
		{"https://feeds.example.com/a.xml/", "", 404},
	} {
		code, body := status(t, c, tc.url)
		if code != tc.code || (tc.code == 200 && !strings.HasPrefix(body, tc.prefix)) {
			t.Errorf("GET %s = %d %q, want %d %q", tc.url, code, body, tc.code, tc.prefix)
		}
	}
}

func TestRouteHostsAreIndependent(t *testing.T) {
	n := New()
	defer n.Close()
	_ = n.Route("https://one.example.com/x", hello("one"))
	_ = n.Route("https://two.example.com/y", hello("two"))
	c := newClient(n)
	if code, _ := status(t, c, "https://one.example.com/y"); code != 404 {
		t.Fatalf("one /y = %d，主機之間的路由不應互通", code)
	}
	if code, _ := status(t, c, "https://two.example.com/y"); code != 200 {
		t.Fatalf("two /y = %d", code)
	}
}

func TestRouteCoexistsWithHandle(t *testing.T) {
	n := New()
	defer n.Close()
	n.Handle("mixed.example.com", hello("fallback"))
	_ = n.Route("https://mixed.example.com/special", hello("special"))
	c := newClient(n)
	if _, body := status(t, c, "https://mixed.example.com/special"); !strings.HasPrefix(body, "special") {
		t.Fatalf("Route 應優先：%q", body)
	}
	if _, body := status(t, c, "https://mixed.example.com/other"); !strings.HasPrefix(body, "fallback") {
		t.Fatalf("沒有 Route 時應交給 Handle：%q", body)
	}
}

func TestRouteHostUsesResolvesAndDefaultIP(t *testing.T) {
	n := New()
	defer n.Close()
	_ = n.Route("http://rsshub.lan/feed", hello("lan"))
	if err := n.Resolves("rsshub.lan", "192.168.1.20"); err != nil {
		t.Fatal(err)
	}
	_ = n.Route("http://plain.example.com/feed", hello("plain"))

	got, err := n.LookupNetIP(context.Background(), "ip", "plain.example.com")
	if err != nil || len(got) != 1 || got[0] != DefaultIP {
		t.Fatalf("只有 Route 的主機應解析到 DefaultIP：%v, %v", got, err)
	}
	// 私網 IP 由 netguard 擋下，所以只確認解析結果與路由登記不互相覆蓋。
	got, err = n.LookupNetIP(context.Background(), "ip", "rsshub.lan")
	if err != nil || len(got) != 1 || got[0].String() != "192.168.1.20" {
		t.Fatalf("rsshub.lan = %v, %v", got, err)
	}
}

func TestRouteRejectsBadInput(t *testing.T) {
	n := New()
	defer n.Close()
	if err := n.Route("not a url", hello("x")); err == nil {
		t.Error("沒有主機的網址應該回傳錯誤")
	}
	if err := n.Route("https://x.example.com/", nil); err == nil {
		t.Error("nil handler 應該回傳錯誤")
	}
}

func TestRedirect(t *testing.T) {
	n := New()
	defer n.Close()
	_ = n.Route("https://a.example.com/1", Redirect("https://a.example.com/2"))
	c := newClient(n)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get("https://a.example.com/1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://a.example.com/2" {
		t.Fatalf("%d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestFixedSizeFeed(t *testing.T) {
	n := New()
	defer n.Close()
	c := newClient(n)

	for _, size := range []int{SizedFeedMin, SizedFeedMin + 1, 5000, 1 << 20, 3<<20 + 7} {
		h, err := FixedSizeFeed(size)
		if err != nil {
			t.Fatal(err)
		}
		if err := n.Route("https://big.example.com/f.xml", h); err != nil {
			t.Fatal(err)
		}
		resp, err := c.Get("https://big.example.com/f.xml")
		if err != nil {
			t.Fatal(err)
		}
		if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(size) {
			t.Errorf("size %d: Content-Length = %q", size, got)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || len(body) != size {
			t.Fatalf("size %d: 讀到 %d bytes，err=%v", size, len(body), err)
		}
		// 必須是合法的 RSS，程式才不會因為「不是 feed」而提前回 422。
		var feed struct {
			XMLName xml.Name `xml:"rss"`
			Title   string   `xml:"channel>title"`
		}
		if err := xml.Unmarshal(body, &feed); err != nil || feed.Title == "" {
			t.Fatalf("size %d: 不是合法的 RSS：%v %+v", size, err, feed)
		}
	}
}

func TestFixedSizeFeedTooSmall(t *testing.T) {
	if _, err := FixedSizeFeed(SizedFeedMin - 1); err == nil {
		t.Fatal("小於最小長度應回傳錯誤")
	}
}
