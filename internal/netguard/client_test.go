package netguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

var publicIP = netip.MustParseAddr("93.184.216.34")

// redirectChain 建立 /0 → /1 → … → /n 的轉址，最後一個路徑回 200。
func redirectChain(n int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var i int
		_, _ = fmt.Sscanf(r.URL.Path, "/%d", &i)
		if i < n {
			http.Redirect(w, r, fmt.Sprintf("/%d", i+1), http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "done")
	})
}

func newTestClient(t *testing.T, h http.Handler) (*http.Client, *recDialer) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	d := &recDialer{addr: srv.Listener.Addr().String()}
	return NewClient(Deps{Resolver: stubResolver{ips: []netip.Addr{publicIP}}, Dialer: d}), d
}

// 5 次轉址可以跟隨，第 6 次才拒絕（len(via) 含最初的請求，所以門檻是 > MaxRedirects）。
func TestNewClientFollowsFiveRedirectsButNotSix(t *testing.T) {
	c, _ := newTestClient(t, redirectChain(MaxRedirects))
	resp, err := c.Get("http://example.com/0")
	if err != nil {
		t.Fatalf("5 次轉址應可跟隨：%v", err)
	}
	resp.Body.Close()

	c, _ = newTestClient(t, redirectChain(MaxRedirects+1))
	resp, err = c.Get("http://example.com/0")
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("6 次轉址：err = %v，應為 ErrTooManyRedirects", err)
	}
}

func TestNewClientRejectsRedirectToOtherScheme(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/file", http.StatusFound)
	}))
	if _, err := c.Get("http://example.com/"); !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewClientBlocksRedirectToPrivateAddress(t *testing.T) {
	c, d := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.1.1/admin", http.StatusFound)
	}))
	if _, err := c.Get("http://example.com/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err = %v", err)
	}
	for _, tg := range d.targets {
		if tg.IP.String() == "192.168.1.1" {
			t.Errorf("不應連到內網位址：%+v", tg)
		}
	}
}

func respWith(body string, contentLength int64) *http.Response {
	return &http.Response{Body: io.NopCloser(strings.NewReader(body)), ContentLength: contentLength}
}

func TestLimitBody(t *testing.T) {
	t.Run("剛好等於上限可以讀完", func(t *testing.T) {
		b, err := io.ReadAll(LimitBody(respWith("0123456789", 10), 10))
		if err != nil || string(b) != "0123456789" {
			t.Fatalf("b = %q, err = %v", b, err)
		}
	})
	t.Run("超過 1 byte 就失敗，且不交出多的那一個 byte", func(t *testing.T) {
		b, err := io.ReadAll(LimitBody(respWith("0123456789A", -1), 10))
		var tl *TooLargeError
		if !errors.Is(err, ErrTooLarge) || !errors.As(err, &tl) || tl.Limit != 10 {
			t.Fatalf("err = %v", err)
		}
		if len(b) > 10 {
			t.Errorf("讀到 %d bytes，不應超過上限", len(b))
		}
	})
	t.Run("Content-Length 超過上限時一個 byte 都不讀", func(t *testing.T) {
		src := &countingReader{r: strings.NewReader(strings.Repeat("x", 100))}
		resp := &http.Response{Body: io.NopCloser(src), ContentLength: 100}
		n, err := LimitBody(resp, 10).Read(make([]byte, 8))
		if n != 0 || !errors.Is(err, ErrTooLarge) || src.n != 0 {
			t.Fatalf("n = %d, err = %v, 已讀 %d", n, err, src.n)
		}
	})
	t.Run("未知長度的串流超過上限", func(t *testing.T) {
		_, err := io.ReadAll(LimitBody(respWith(strings.Repeat("x", 5000), -1), 4096))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("用小 buffer 逐段讀也能分辨剛好與超過", func(t *testing.T) {
		r := LimitBody(respWith("abcdef", -1), 5)
		buf := make([]byte, 2)
		var got []byte
		var err error
		for err == nil {
			var n int
			n, err = r.Read(buf)
			got = append(got, buf[:n]...)
		}
		if !errors.Is(err, ErrTooLarge) || string(got) != "abcde" {
			t.Fatalf("got = %q, err = %v", got, err)
		}
	})
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestPolicyMessage(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("外層：%w", err) }
	cases := []struct {
		err  error
		want string
		ok   bool
	}{
		{wrap(fmt.Errorf("%w: 10.0.0.1", ErrBlockedAddress)), "這個網址指向內部網路，已封鎖。", true},
		{wrap(ErrTooManyRedirects), "轉址超過 5 次。", true},
		{wrap(&TooLargeError{Limit: LimitFeed}), "回應超過大小上限（10 MiB）。", true},
		{wrap(&TooLargeError{Limit: LimitAudio}), "回應超過大小上限（2 GiB）。", true},
		{wrap(ErrTooLarge), "回應超過大小上限。", true},
		{wrap(fmt.Errorf("%w：%q", ErrUnsupportedScheme, "ftp")), "只支援 http 與 https 網址。", true},
		{errors.New("HTTP 500"), "", false},
		{nil, "", false},
	}
	for _, tc := range cases {
		got, ok := PolicyMessage(tc.err)
		if got != tc.want || ok != tc.ok {
			t.Errorf("PolicyMessage(%v) = %q, %v；want %q, %v", tc.err, got, ok, tc.want, tc.ok)
		}
	}
}
