package hubapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
)

// 一般限流（HACC-R18）：每個帳號（session 與 PAT 合計）與每個未登入的 IP 各自一個固定的一分鐘視窗。
// 計數只在這個程序的記憶體中（多個 Hub 程序共用不在範圍內，ADR-0031 第 5 點），時間取自注入的 clock。
// IP 取自連線的 RemoteAddr；server.trusted_proxies 中的代理連進來時，hub 的 handler 已依 X-Forwarded-For 改寫它
// （安全修補 A，HACC 待決問題 A14；預設不信任 X-Forwarded-For，同 A12）。

// CodeRateLimited 是超過限流的代碼。
const CodeRateLimited = "rate_limited"

const rateWindow = time.Minute

// rateLimiter 是固定視窗的計數器。
type rateLimiter struct {
	clock clock.Clock
	mu    sync.Mutex
	// start 是目前視窗的開始；視窗改變時清空 counts（不會無限累積）。
	start  time.Time
	counts map[string]int
}

func newRateLimiter(clk clock.Clock) *rateLimiter {
	if clk == nil {
		clk = clock.Real{}
	}
	return &rateLimiter{clock: clk, counts: map[string]int{}}
}

// allow 為 key 計入一個請求；超過 limit 時回傳要等待的時間（至少 1 秒）。limit <= 0 代表不限。
func (l *rateLimiter) allow(key string, limit int) (time.Duration, bool) {
	if limit <= 0 {
		return 0, true
	}
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	start := now.Truncate(rateWindow)
	if !start.Equal(l.start) {
		l.start = start
		clear(l.counts)
	}
	l.counts[key]++
	if l.counts[key] <= limit {
		return 0, true
	}
	return max(start.Add(rateWindow).Sub(now), time.Second), false
}

// errRateLimited 是 429，附 retry_after_s（寫出時另外設定 Retry-After 標頭）。
func errRateLimited(wait time.Duration) *apierr.Error {
	secs := int(math.Ceil(wait.Seconds()))
	e := apierr.New(http.StatusTooManyRequests, CodeRateLimited, fmt.Sprintf("請求太頻繁，請在 %d 秒後再試。", secs))
	e.RetryAfterS = &secs
	return e
}

// retryAfterError 讓 Huma handler 回傳的 429 也帶 Retry-After 標頭（Huma 依 HeadersError 設定標頭）；
// 狀態碼與本體同 *apierr.Error。
type retryAfterError struct{ e *apierr.Error }

func (r retryAfterError) Error() string                { return r.e.Error() }
func (r retryAfterError) GetStatus() int               { return r.e.GetStatus() }
func (r retryAfterError) Unwrap() error                { return r.e }
func (r retryAfterError) MarshalJSON() ([]byte, error) { return json.Marshal(r.e) }

// GetHeaders 實作 huma.HeadersError。
func (r retryAfterError) GetHeaders() http.Header {
	h := http.Header{}
	if r.e.RetryAfterS != nil {
		h.Set("Retry-After", strconv.Itoa(*r.e.RetryAfterS))
	}
	return h
}

// handlerRateLimited 是 handler 內的限流錯誤（429，附 retry_after_s 與 Retry-After）。
func handlerRateLimited(wait time.Duration) error { return retryAfterError{errRateLimited(wait)} }

// clientIP 回傳連線的來源位址（不含連接埠）。
func clientIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

func accountKey(userID string) string { return "user:" + userID }

// ipKey 是未登入請求的限流鍵：IPv6 以 /64 為單位（一般用戶端一次就拿到整個 /64，逐一換位址就能繞過限流，安全修補 A）。
func ipKey(remoteAddr string) string {
	ip := clientIP(remoteAddr)
	if a, err := netip.ParseAddr(ip); err == nil {
		a = a.Unmap()
		if a.Is6() {
			p, _ := a.Prefix(64)
			return "ip:" + p.String()
		}
		return "ip:" + a.String()
	}
	return "ip:" + ip
}

// limitAnonymous 以 IP 計入一個未登入的請求。
func (rt *routes) limitAnonymous(remoteAddr string) *apierr.Error {
	if wait, ok := rt.limiter.allow(ipKey(remoteAddr), rt.rateLimits().AnonymousRequestsPerMinute); !ok {
		return errRateLimited(wait)
	}
	return nil
}

// limitAccount 以帳號計入一個請求。
func (rt *routes) limitAccount(userID string) *apierr.Error {
	if wait, ok := rt.limiter.allow(accountKey(userID), rt.rateLimits().RequestsPerMinute); !ok {
		return errRateLimited(wait)
	}
	return nil
}

// limitRaw 讓不經過 Huma 的瀏覽器流程（/auth/<provider>/start|callback）也受 IP 限流。
func (rt *routes) limitRaw(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if e := rt.limitAnonymous(r.RemoteAddr); e != nil {
			writeRawErr(w, e)
			return
		}
		next(w, r)
	}
}
