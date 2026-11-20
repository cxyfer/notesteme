package pipeline

import (
	"context"
	"sync"
)

// itemLocks 是以影音 ID 為鍵的互斥鎖；零值可直接使用。沒有人持有或等待的鍵會被移除，不會累積。
type itemLocks struct {
	mu sync.Mutex
	m  map[string]*itemLock
}

type itemLock struct {
	ch   chan struct{} // 容量 1：放得進去代表取得鎖
	refs int           // 持有與等待中的數量，由 itemLocks.mu 保護
}

// lock 取得 id 的鎖；ctx 結束時放棄等待並回傳 ctx 的錯誤。成功時回傳釋放鎖的函式。
func (l *itemLocks) lock(ctx context.Context, id string) (unlock func(), err error) {
	l.mu.Lock()
	if l.m == nil {
		l.m = map[string]*itemLock{}
	}
	e := l.m[id]
	if e == nil {
		e = &itemLock{ch: make(chan struct{}, 1)}
		l.m[id] = e
	}
	e.refs++
	l.mu.Unlock()

	release := func() {
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, id)
		}
		l.mu.Unlock()
	}
	select {
	case e.ch <- struct{}{}:
		return func() { <-e.ch; release() }, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}
