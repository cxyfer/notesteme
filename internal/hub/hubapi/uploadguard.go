package hubapi

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/clock"
)

// 上傳的保護（安全修補 B，HREP 待決問題 R11）：
//   - 全域的並行名額：每個分享最多佔 max_upload_bytes 的上傳加上 max_total_bytes 的解壓內容，
//     名額滿了不排隊，直接回 429 rate_limited（ADR-0032 第 1 點：限流一律 429）。
//     每個帳號同時最多佔 1 個，而且讀取本體有期限（shareReadTimeout），一個帳號的慢速上傳不能佔住所有名額。
//   - 失敗的分享也計入每小時的次數：成功的分享記在 DB（Hub 重新啟動後不歸零），失敗的只記在這個程序的記憶體。

// maxConcurrentShares 是同時處理的分享數；maxSharesPerAccount 是每個帳號同時的分享數。
const (
	maxConcurrentShares = 2
	maxSharesPerAccount = 1
)

// shareReadTimeout 是讀取分享本體的期限（http.Server 只有 ReadHeaderTimeout）：32 MiB 約需 110 KB/s。
const shareReadTimeout = 5 * time.Minute

// shareBusyRetry 是名額滿時建議的等待時間。
const shareBusyRetry = 5 * time.Second

const shareFailureWindow = time.Hour

const msgShareBusy = "Hub 正在處理其他上傳，請在 %d 秒後再試。"

// errShareBusy 是名額滿時的 429（附 retry_after_s 與 Retry-After）。
func errShareBusy() *apierr.Error {
	secs := int(shareBusyRetry / time.Second)
	e := apierr.New(http.StatusTooManyRequests, CodeRateLimited, fmt.Sprintf(msgShareBusy, secs))
	e.RetryAfterS = &secs
	return e
}

type uploadGuard struct {
	slots chan struct{}
	clock clock.Clock
	mu    sync.Mutex
	// failures 是每個帳號失敗的分享時間（遞增）。
	failures map[string][]time.Time
	// active 是每個帳號正在處理的分享數。
	active map[string]int
}

func newUploadGuard(clk clock.Clock) *uploadGuard {
	if clk == nil {
		clk = clock.Real{}
	}
	return &uploadGuard{slots: make(chan struct{}, maxConcurrentShares), clock: clk, failures: map[string][]time.Time{}, active: map[string]int{}}
}

// acquire 替 userID 取得一個上傳名額；全域或這個帳號的名額滿了回傳 false（不等待）。
func (g *uploadGuard) acquire(userID string) (release func(), ok bool) {
	g.mu.Lock()
	if g.active[userID] >= maxSharesPerAccount {
		g.mu.Unlock()
		return nil, false
	}
	select {
	case g.slots <- struct{}{}:
	default:
		g.mu.Unlock()
		return nil, false
	}
	g.active[userID]++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if g.active[userID]--; g.active[userID] <= 0 {
				delete(g.active, userID)
			}
			g.mu.Unlock()
			<-g.slots
		})
	}, true
}

// recordFailure 記錄 userID 一次失敗的分享。
func (g *uploadGuard) recordFailure(userID string) {
	now := g.clock.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prune(now)
	g.failures[userID] = append(g.failures[userID], now)
}

// recentFailures 回傳 userID 最近一小時失敗的次數與最早的一次。
func (g *uploadGuard) recentFailures(userID string) (int, time.Time) {
	now := g.clock.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prune(now)
	ts := g.failures[userID]
	if len(ts) == 0 {
		return 0, time.Time{}
	}
	return len(ts), ts[0]
}

// prune 清掉一小時以前的紀錄（呼叫端持有 mu）。
func (g *uploadGuard) prune(now time.Time) {
	for k, ts := range g.failures {
		i := 0
		for i < len(ts) && !ts[i].After(now.Add(-shareFailureWindow)) {
			i++
		}
		if i == len(ts) {
			delete(g.failures, k)
		} else if i > 0 {
			g.failures[k] = ts[i:]
		}
	}
}

// countsAsFailure 回報這個錯誤是不是「讀過本體之後，因為內容或請求不正確」而失敗的分享：
// 限流（429）與 Hub 自己的錯誤（5xx）不計入。
func countsAsFailure(e *apierr.Error) bool {
	switch e.GetStatus() {
	case http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return true
	}
	return false
}
