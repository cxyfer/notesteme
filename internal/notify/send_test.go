package notify

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// testNet 把每個主機解析到指定的 IP（依序，模擬 DNS rebinding），連線一律導到 httptest server。
type testNet struct {
	mu    sync.Mutex
	ips   map[string][]string
	addr  string
	dials []netip.Addr
}

func (n *testNet) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	seq := n.ips[host]
	if len(seq) == 0 {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	ip := seq[0]
	if len(seq) > 1 {
		n.ips[host] = seq[1:]
	}
	return []netip.Addr{netip.MustParseAddr(ip)}, nil
}

func (n *testNet) Dial(ctx context.Context, t netguard.Target) (net.Conn, error) {
	n.mu.Lock()
	n.dials = append(n.dials, t.IP)
	n.mu.Unlock()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", n.addr)
}

func newTestClient(t *testing.T, h http.Handler) (*http.Client, *testNet) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	tn := &testNet{ips: map[string][]string{}, addr: srv.Listener.Addr().String()}
	return NewClient(netguard.Deps{Resolver: tn, Dialer: tn}), tn
}

func TestSendClassifiesResponses(t *testing.T) {
	var status int
	var hdr http.Header
	var mu sync.Mutex
	c, tn := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hdr = r.Header.Clone()
		st := status
		mu.Unlock()
		switch st {
		case 302:
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		case 429:
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
		case 299:
			w.WriteHeader(200)
			// 超大的回應只讀前 64 KiB，不會讓送出失敗或卡住。
			_, _ = w.Write([]byte(strings.Repeat("x", 4<<20)))
		default:
			w.WriteHeader(st)
		}
	}))
	now := time.Unix(1_800_000_000, 0)
	rq := buildRequest(KindWebhook, Config{"url": "http://hooks.example.com/x", "secret": "whsec-0123456789abcdef"}, []byte(`{"a":1}`), "item.done", "d1", "1.2.3", now)
	cases := []struct {
		status int
		ok     bool
		class  errclass.Class
		msg    string
	}{
		{204, true, "", ""},
		{299, true, "", ""},
		{302, false, errclass.ClassPermanent, MsgRedirect},
		{404, false, errclass.ClassPermanent, "通知端點回應 HTTP 404。"},
		{401, false, errclass.ClassPermanent, "通知端點回應 HTTP 401。"},
		{503, false, errclass.ClassRetryable, "通知端點回應 HTTP 503。"},
		{408, false, errclass.ClassRetryable, "通知端點回應 HTTP 408。"},
		{429, false, errclass.ClassRetryable, "通知端點回應 HTTP 429。"},
	}
	for _, cs := range cases {
		mu.Lock()
		status = cs.status
		mu.Unlock()
		o := send(context.Background(), c, KindWebhook, rq, now)
		if o.OK != cs.ok || o.Class != cs.class || o.Message != cs.msg {
			t.Errorf("HTTP %d → %+v", cs.status, o)
		}
		if cs.status == 429 && o.RetryAfter != 2*time.Minute {
			t.Errorf("Retry-After = %v", o.RetryAfter)
		}
	}
	// 轉址不跟隨：只連到 webhook 的主機，沒有連到 metadata。
	for _, ip := range tn.dials {
		if ip.String() == "169.254.169.254" {
			t.Error("不應跟隨轉址到 metadata")
		}
	}
	// 簽章與標頭（NOTIFY-R4、R5）。
	if hdr.Get("X-Notesteme-Signature") != Sign("whsec-0123456789abcdef", now.Unix(), []byte(`{"a":1}`)) ||
		hdr.Get("X-Notesteme-Timestamp") != "1800000000" || hdr.Get("X-Notesteme-Delivery") != "d1" ||
		hdr.Get("X-Notesteme-Event") != "item.done" || hdr.Get("User-Agent") != "Notesteme/1.2.3" || hdr.Get("Content-Type") != "application/json" {
		t.Errorf("標頭 = %v", hdr)
	}
}

func TestSendBlocksPrivateAndRebinding(t *testing.T) {
	c, tn := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	tn.ips["hooks.example.com"] = []string{"93.184.216.63", "192.168.1.1"}
	tn.ips["lan.example"] = []string{"10.0.0.5"}
	rq := func(host string) request {
		return buildRequest(KindWebhook, Config{"url": "http://" + host + "/x", "secret": "s"}, []byte("{}"), "test", "d", "v", time.Now())
	}
	if o := send(context.Background(), c, KindWebhook, rq("lan.example"), time.Now()); o.OK || o.Class != errclass.ClassPermanent || o.Message != MsgBlocked {
		t.Errorf("內網：%+v", o)
	}
	if o := send(context.Background(), c, KindWebhook, rq("hooks.example.com"), time.Now()); !o.OK {
		t.Errorf("第一次公開 IP：%+v", o)
	}
	// 第二次解析到內網：不能重用第一次的連線，要重新解析並封鎖。
	if o := send(context.Background(), c, KindWebhook, rq("hooks.example.com"), time.Now()); o.OK || o.Message != MsgBlocked {
		t.Errorf("DNS rebinding：%+v", o)
	}
	for _, ip := range tn.dials {
		if ip.IsPrivate() {
			t.Errorf("不應連到 %s", ip)
		}
	}
	// 其他 scheme 與太大的 body。
	if o := send(context.Background(), c, KindWebhook, request{url: "ftp://x/"}, time.Now()); o.Message != MsgScheme {
		t.Errorf("ftp：%+v", o)
	}
	if o := send(context.Background(), c, KindWebhook, request{url: "http://hooks.example.com/", body: make([]byte, MaxRequestBody+1)}, time.Now()); o.Message != MsgTooLarge || o.Class != errclass.ClassPermanent {
		t.Errorf("太大：%+v", o)
	}
}

func TestSendConnectionErrorsHideTheURL(t *testing.T) {
	c := NewClient(netguard.Deps{Resolver: &testNet{ips: map[string][]string{}}, Dialer: failDialer{}})
	rq := buildRequest(KindTelegram, Config{"bot_token": testBotToken, "chat_id": "1"}, []byte("{}"), "test", "d", "v", time.Now())
	if !strings.Contains(rq.url, testBotToken) {
		t.Fatal("telegram 的網址含 token")
	}
	o := send(context.Background(), c, KindTelegram, rq, time.Now())
	if o.OK || o.Class != errclass.ClassRetryable || o.Message != MsgConnect || strings.Contains(o.Message, "ABC") {
		t.Errorf("連線失敗：%+v", o)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if o := send(ctx, c, KindTelegram, rq, time.Now()); o.Message != MsgTimeout {
		t.Errorf("逾時：%+v", o)
	}
}

const testSecretVector = "whsec-0123456789abcdef"

type failDialer struct{}

func (failDialer) Dial(context.Context, netguard.Target) (net.Conn, error) {
	return nil, &net.OpError{Op: "dial", Err: errRefused{}}
}

type errRefused struct{}

func (errRefused) Error() string { return "connection refused" }

func TestEndpointURLsAndHeaders(t *testing.T) {
	cases := map[string]string{
		endpointURL(KindNtfy, Config{"server": "https://ntfy.sh"}):                              "https://ntfy.sh/",
		endpointURL(KindDiscord, Config{"webhook_url": testDiscord}):                            testDiscord + "?wait=true",
		endpointURL(KindTelegram, Config{"bot_token": "1:x"}):                                   "https://api.telegram.org/bot1:x/sendMessage",
		endpointURL(KindTelegram, Config{"bot_token": "1:x", "api_base": "https://tg.example"}): "https://tg.example/bot1:x/sendMessage",
		endpointURL(KindWebhook, Config{"url": "https://h/x?a=1"}):                              "https://h/x?a=1",
		endpointURL(KindDiscord, Config{"webhook_url": "https://h/x?thread_id=1"}):              "https://h/x?thread_id=1&wait=true",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("endpointURL = %q，預期 %q", got, want)
		}
	}
	if h := endpointHost(KindTelegram, Config{"bot_token": testBotToken}); h != "api.telegram.org" {
		t.Errorf("endpointHost = %q（不可含 token）", h)
	}
	rq := buildRequest(KindNtfy, Config{"server": "https://ntfy.sh", "token": "tk"}, nil, "e", "d", "v", time.Now())
	if rq.headers["Authorization"] != "Bearer tk" || rq.headers["X-Notesteme-Signature"] != "" {
		t.Errorf("ntfy 標頭 = %v", rq.headers)
	}
	if rq := buildRequest(KindNtfy, Config{"server": "https://ntfy.sh"}, nil, "e", "d", "v", time.Now()); rq.headers["Authorization"] != "" {
		t.Error("沒有 token 時不帶 Authorization")
	}
}

func TestSignDetectsTampering(t *testing.T) {
	// 獨立計算的向量：hex(HMAC-SHA256("whsec-0123456789abcdef", "1800000000." + `{"a":1}`))。
	if got := Sign(testSecretVector, 1_800_000_000, []byte(`{"a":1}`)); got != "sha256=d056fd5474ba03ed9cdbe1076f08cb25926142f7e99899ec3944232a3357657a" {
		t.Errorf("Sign 的固定向量 = %s", got)
	}
	sig := Sign("secret-0123456789", 100, []byte(`{"a":1}`))
	if sig != Sign("secret-0123456789", 100, []byte(`{"a":1}`)) || !strings.HasPrefix(sig, "sha256=") || len(sig) != 7+64 {
		t.Fatalf("Sign = %q", sig)
	}
	for _, other := range []string{
		Sign("secret-0123456789", 101, []byte(`{"a":1}`)),
		Sign("secret-0123456789", 100, []byte(`{"a":2}`)),
		Sign("secret-012345678X", 100, []byte(`{"a":1}`)),
		Sign("secret-0123456789", 10, []byte(`0{"a":1}`)), // 時間與 body 的分隔不可混淆
	} {
		if other == sig {
			t.Error("竄改後的簽章不應相同")
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"120": 2 * time.Minute, "0": 0, "-5": 0, "": 0, "abc": 0, "99999999999999999999": 0,
		"86400": 6 * time.Hour, now.Add(90 * time.Second).Format(http.TimeFormat): 90 * time.Second,
		now.Add(-time.Hour).Format(http.TimeFormat): 0,
	}
	for in, want := range cases {
		if got := ParseRetryAfter(in, now); got != want {
			t.Errorf("ParseRetryAfter(%q) = %v，預期 %v", in, got, want)
		}
	}
	if d := telegramRetryAfter([]byte(`{"ok":false,"parameters":{"retry_after":30}}`)); d != 30*time.Second {
		t.Errorf("telegram retry_after = %v", d)
	}
	if d := telegramRetryAfter([]byte(`{"parameters":{"retry_after":1e12}}`)); d != 6*time.Hour {
		t.Errorf("上限：%v", d)
	}
	o := classifyResponse(KindTelegram, 429, "", []byte(`{"parameters":{"retry_after":300}}`), now)
	if o.RetryAfter != 5*time.Minute || o.Class != errclass.ClassRetryable {
		t.Errorf("telegram 429 = %+v", o)
	}
}

func FuzzParseRetryAfter(f *testing.F) {
	for _, s := range []string{"120", "-1", "Wed, 21 Oct 2015 07:28:00 GMT", "1e9", "", "9223372036854775807"} {
		f.Add(s, []byte(`{"parameters":{"retry_after":5}}`))
	}
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, header string, body []byte) {
		for _, d := range []time.Duration{ParseRetryAfter(header, now), telegramRetryAfter(body)} {
			if d < 0 || d > maxRetryAfter {
				t.Errorf("Retry-After 超出範圍：%v（%q %q）", d, header, body)
			}
		}
	})
}
