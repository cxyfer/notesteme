package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake 是測試用的時鐘，時間只會因 Set 或 Advance 而改變。並行安全。
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

// NewFake 建立從 t 開始的 fake 時鐘。
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

// Now 回傳 fake 時鐘目前的時間。
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer 建立計時器；d <= 0 時立即到期。
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{f: f, c: make(chan time.Time, 1), deadline: f.now.Add(d)}
	if d <= 0 {
		t.fired = true
		t.c <- f.now
		return t
	}
	f.timers = append(f.timers, t)
	return t
}

// Set 把時間設為 t，並觸發所有在 t 以前到期的計時器。
// 時間往回設時不會觸發任何計時器。
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
	f.fireDueLocked()
}

// Advance 把時間往前推 d，並觸發到期的計時器（依到期時間先後）。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	f.fireDueLocked()
}

// fireDueLocked 觸發已到期的計時器；呼叫時需持有 f.mu。
func (f *Fake) fireDueLocked() {
	sort.SliceStable(f.timers, func(i, j int) bool {
		return f.timers[i].deadline.Before(f.timers[j].deadline)
	})
	rest := f.timers[:0]
	for _, t := range f.timers {
		if t.deadline.After(f.now) {
			rest = append(rest, t)
			continue
		}
		t.fired = true
		// channel 有 1 格緩衝，且每個計時器只送一次，不會阻塞。
		t.c <- t.deadline
	}
	f.timers = rest
}

type fakeTimer struct {
	f        *Fake
	c        chan time.Time
	deadline time.Time
	fired    bool
	stopped  bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	for i, x := range t.f.timers {
		if x == t {
			t.f.timers = append(t.f.timers[:i], t.f.timers[i+1:]...)
			break
		}
	}
	return true
}
