package subscription

import (
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// throttlePause 是平台回 429（Bilibili 412）之後整個 bucket 暫停的時間（SUB-R10）。
const throttlePause = 15 * time.Minute

// limiter 是探索的 token bucket（SUB-R10）：每個 key（youtube、bilibili、feed:<主機>）一個，
// 容量等於每分鐘的數量，以注入的 clock 計時，精度到毫秒（scheduled_jobs 的時間精度）。只存在記憶體。
type limiter struct {
	clock clock.Clock
	mu    sync.Mutex
	b     map[string]*bucket
}

type bucket struct {
	perMinute int
	tokens    int
	// last 是 tokens 最後一次補充的時間點（補到整數顆為止）。
	last time.Time
	// pausedUntil 之前一律沒有額度。
	pausedUntil time.Time
}

func newLimiter(clk clock.Clock) *limiter { return &limiter{clock: clk, b: map[string]*bucket{}} }

func (b *bucket) interval() time.Duration { return time.Minute / time.Duration(b.perMinute) }

// refill 依經過的時間補充額度。
func (b *bucket) refill(now time.Time) {
	if now.Before(b.last) {
		return
	}
	iv := b.interval()
	n := int(now.Sub(b.last) / iv)
	if n <= 0 {
		return
	}
	b.tokens += n
	b.last = b.last.Add(time.Duration(n) * iv)
	if b.tokens >= b.perMinute {
		b.tokens, b.last = b.perMinute, now
	}
}

func (l *limiter) get(key string, perMinute int, now time.Time) *bucket {
	if perMinute < 1 {
		perMinute = 1
	}
	b := l.b[key]
	if b == nil {
		b = &bucket{perMinute: perMinute, tokens: perMinute, last: now}
		l.b[key] = b
	}
	if b.perMinute != perMinute {
		b.perMinute = perMinute
		b.tokens = min(b.tokens, perMinute)
	}
	return b
}

// Take 取一顆額度；沒有額度時 ok 為 false，retryAt 是下一顆額度出現的時間。
func (l *limiter) Take(key string, perMinute int) (ok bool, retryAt time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	b := l.get(key, perMinute, now)
	if now.Before(b.pausedUntil) {
		return false, b.pausedUntil
	}
	b.refill(now)
	if b.tokens > 0 {
		b.tokens--
		return true, time.Time{}
	}
	return false, b.last.Add(b.interval())
}

// Pause 清空 bucket 並暫停 throttlePause；回傳恢復的時間（第一顆額度出現的時間）。
func (l *limiter) Pause(key string, perMinute int) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	b := l.get(key, perMinute, now)
	b.pausedUntil = now.Add(throttlePause)
	// 恢復時剛好補到一顆。
	b.tokens, b.last = 0, b.pausedUntil.Add(-b.interval())
	return b.pausedUntil
}
