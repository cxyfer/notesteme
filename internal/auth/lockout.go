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
func (l *lockouts) remaining(addr string, now time.Time) time.Duration {
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
