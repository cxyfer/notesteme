package auth

import (
	"container/list"
	"sync"
	"time"
)

// 登入鎖定的記憶體上限（AUTH-R6）。
const (
	// maxLockoutRecords 是同時保留的來源位址紀錄上限。
	// 計數只存在記憶體，攻擊者可以用大量不同的來源位址（特別是 IPv6）製造紀錄；
	// 沒有上限就會無限成長。超過時淘汰最舊的紀錄，代價是被淘汰位址的計數歸零，
	// 這只會讓攻擊者多得到幾次嘗試，不會讓 owner 被鎖在外面。
	maxLockoutRecords = 10_000
	// failureTTL 是失敗計數自最近一次失敗起保留多久；之後視為沒有紀錄。
	// 鎖定本身在 lockDuration 後到期，到期的紀錄一併清除。
	failureTTL = time.Hour
)

// lockoutEntry 是單一來源位址的失敗計數與鎖定狀態。
type lockoutEntry struct {
	addr        string
	failures    int
	lockedUntil time.Time
	// expires 是這筆紀錄可以被清除的時間。
	expires time.Time
}

// lockouts 以來源位址為 key 記錄登入失敗（AUTH-R6）。只存在記憶體，重啟後歸零。
// 紀錄依「最近一次更新」排序（最舊的在 list 前端），超過上限時從前端淘汰。
type lockouts struct {
	mu      sync.Mutex
	max     int
	entries map[string]*list.Element // 值為 *lockoutEntry
	order   *list.List
	// lastSweep 是上次清除過期紀錄的時間；清除是 O(n)，所以最多每 lockDuration 做一次。
	lastSweep time.Time
}

func newLockouts(max int) *lockouts {
	return &lockouts{max: max, entries: map[string]*list.Element{}, order: list.New()}
}

// remaining 回傳 addr 的鎖定還剩多久；沒有鎖定時為 0。
// addr 一律先轉成 sourceKey（IPv6 以 /64 計算），fail、reset 也一樣。
func (l *lockouts) remaining(addr string, now time.Time) time.Duration {
	addr = sourceKey(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.live(addr, now)
	if e == nil {
		return 0
	}
	return max(0, e.lockedUntil.Sub(now))
}

// fail 記錄 addr 的一次密碼錯誤；連續達到 maxFailures 次時開始鎖定並重新計數。
func (l *lockouts) fail(addr string, now time.Time) {
	addr = sourceKey(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	e := l.live(addr, now)
	if e == nil {
		l.makeRoom()
		e = &lockoutEntry{addr: addr}
		l.entries[addr] = l.order.PushBack(e)
	} else {
		l.order.MoveToBack(l.entries[addr])
	}
	e.failures++
	e.expires = now.Add(failureTTL)
	if e.failures >= maxFailures {
		e.failures = 0
		e.lockedUntil = now.Add(lockDuration)
		e.expires = e.lockedUntil
	}
}

// reset 在登入成功時清除 addr 的計數。
func (l *lockouts) reset(addr string) {
	addr = sourceKey(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.remove(addr)
}

// size 回傳目前保留的紀錄數（測試用）。
func (l *lockouts) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// live 回傳未過期的紀錄；過期的順手清除。呼叫端須持有 mu。
func (l *lockouts) live(addr string, now time.Time) *lockoutEntry {
	el, ok := l.entries[addr]
	if !ok {
		return nil
	}
	e := el.Value.(*lockoutEntry)
	if !now.Before(e.expires) {
		l.remove(addr)
		return nil
	}
	return e
}

// sweep 清除所有過期的紀錄。呼叫端須持有 mu。
func (l *lockouts) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < lockDuration {
		return
	}
	l.lastSweep = now
	for el := l.order.Front(); el != nil; {
		next := el.Next()
		if e := el.Value.(*lockoutEntry); !now.Before(e.expires) {
			l.remove(e.addr)
		}
		el = next
	}
}

// makeRoom 在新增紀錄前確保總筆數不超過上限：淘汰最舊的。呼叫端須持有 mu。
func (l *lockouts) makeRoom() {
	for len(l.entries) >= l.max {
		front := l.order.Front()
		if front == nil {
			return
		}
		l.remove(front.Value.(*lockoutEntry).addr)
	}
}

func (l *lockouts) remove(addr string) {
	if el, ok := l.entries[addr]; ok {
		l.order.Remove(el)
		delete(l.entries, addr)
	}
}

// MaxOIDCStartsPerSource 是同一個來源（sourceKey）在 OIDCLoginTTL 內最多可以開始幾次 OIDC 登入（AUTH-R17）。
// start 是免驗證的端點，沒有這個上限時，一個來源就能佔滿 MaxPendingOIDCLogins，讓 owner 拿到 429。
const MaxOIDCStartsPerSource = 20

// startLimiter 記錄每個來源最近 OIDCLoginTTL 內開始的 OIDC 登入時間。只存在記憶體，有筆數上限。
type startLimiter struct {
	mu      sync.Mutex
	max     int // 來源筆數上限
	window  time.Duration
	perKey  int
	entries map[string][]time.Time
}

func newStartLimiter(maxKeys, perKey int, window time.Duration) *startLimiter {
	return &startLimiter{max: maxKeys, window: window, perKey: perKey, entries: map[string][]time.Time{}}
}

// allow 在 key 還沒到上限時記錄一次並回傳 true。
func (l *startLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	live := l.live(key, now)
	if len(live) >= l.perKey {
		return false
	}
	if _, ok := l.entries[key]; !ok && len(l.entries) >= l.max {
		// 先清掉過期的；仍然太多時淘汰最久沒有活動的來源（只會讓它少計幾次，不會擋住別人）。
		for k := range l.entries {
			l.live(k, now)
		}
		if len(l.entries) >= l.max {
			var oldest string
			var at time.Time
			for k, ts := range l.entries {
				if last := ts[len(ts)-1]; oldest == "" || last.Before(at) {
					oldest, at = k, last
				}
			}
			delete(l.entries, oldest)
		}
	}
	l.entries[key] = append(live, now)
	return true
}

// live 回傳 key 在時間窗內的紀錄，並清掉過期的。呼叫端須持有 mu。
func (l *startLimiter) live(key string, now time.Time) []time.Time {
	ts := l.entries[key]
	i := 0
	for i < len(ts) && !now.Before(ts[i].Add(l.window)) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.entries, key)
		return nil
	}
	l.entries[key] = ts
	return ts
}
