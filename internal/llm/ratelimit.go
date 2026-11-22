package llm

import (
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// Limits 是一個 provider 的速率上限（llm.providers.<id>.rpm、tpm，LLM-R7）；0 代表不限制。
type Limits struct {
	RPM int
	TPM int
}

// Limiter 是每個 provider 各一組的 token bucket（以 GCRA 實作：容量是每分鐘的上限，等速補滿）。
// 時間來自注入的 clock，全部用整數的 time.Duration 運算：剛好過了一分鐘就一定補滿，不會差一點點（NFR-TEST-2）。
// 程序內共用；並行安全。
type Limiter struct {
	clk clock.Clock
	mu  sync.Mutex
	// tat 是各 provider 的「理論到達時間」：bucket 在這個時間點會剛好補滿。
	reqTAT map[string]time.Time
	tokTAT map[string]time.Time
}

// NewLimiter 建立 Limiter。
func NewLimiter(clk clock.Clock) *Limiter {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Limiter{clk: clk, reqTAT: map[string]time.Time{}, tokTAT: map[string]time.Time{}}
}

// cost 是用掉 n 個單位所需的補充時間（每分鐘補 perMinute 個）。
func cost(n, perMinute int) time.Duration {
	return time.Duration(int64(time.Minute) * int64(n) / int64(perMinute))
}

// admit 檢查一個 bucket 能不能再用掉 n 個單位：可以時回傳新的 tat；不行時回傳要等多久。
// n 超過容量時，只要 bucket 是滿的就放行（否則永遠不會通過）。
func admit(tat, now time.Time, n, perMinute int) (next time.Time, wait time.Duration) {
	base := tat
	if base.Before(now) {
		base = now
	}
	next = base.Add(cost(n, perMinute))
	if over := next.Sub(now) - time.Minute; over > 0 {
		if n > perMinute && !tat.After(now) {
			return next, 0
		}
		return time.Time{}, over
	}
	return next, 0
}

// Reserve 為一個請求預扣額度（1 個請求、tokens 個 token）。額度不夠時不扣，回傳要等待的時間；
// 回傳 0 代表已扣除，可以送出。lim 的 RPM 與 TPM 都是 0 時一律放行。
func (l *Limiter) Reserve(id string, lim Limits, tokens int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	var reqNext, tokNext time.Time
	var wait time.Duration
	if lim.RPM > 0 {
		var w time.Duration
		reqNext, w = admit(l.reqTAT[id], now, 1, lim.RPM)
		wait = max(wait, w)
	}
	if lim.TPM > 0 && tokens > 0 {
		var w time.Duration
		tokNext, w = admit(l.tokTAT[id], now, tokens, lim.TPM)
		wait = max(wait, w)
	}
	if wait > 0 {
		return wait
	}
	if lim.RPM > 0 {
		l.reqTAT[id] = reqNext
	}
	if lim.TPM > 0 && tokens > 0 {
		l.tokTAT[id] = tokNext
	}
	return 0
}

// Charge 不檢查額度就扣除（可以透支）：同一個 chunk 的驗證重試要立刻送出（LLM-R5），
// 透支的額度由之後的請求等待補回。
func (l *Limiter) Charge(id string, lim Limits, tokens int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	charge := func(m map[string]time.Time, n, perMinute int) {
		base := m[id]
		if base.Before(now) {
			base = now
		}
		m[id] = base.Add(cost(n, perMinute))
	}
	if lim.RPM > 0 {
		charge(l.reqTAT, 1, lim.RPM)
	}
	if lim.TPM > 0 && tokens > 0 {
		charge(l.tokTAT, tokens, lim.TPM)
	}
}
