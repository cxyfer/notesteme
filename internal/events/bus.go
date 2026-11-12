// Package events 是記憶體中的事件匯流排，供 SSE（PIPE-R14）把狀態變更推給訂閱者。
//
// 設計重點：發布端永遠不被訂閱者拖慢。每個訂閱者有固定大小的 buffer，
// 發布時用不阻塞的方式送出；buffer 滿了就斷開該訂閱者（它必須重新連線並自行補查現況）。
package events

import "sync"

// TypeStageStatus 是步驟狀態改變的事件類型。
const TypeStageStatus = "stage.status"

// Event 是推送給訂閱者的事件；JSON 欄位就是 SSE 的 data。
type Event struct {
	Type string `json:"type"`
	// Item 是影音的短代號（例如 yt:abc123），ItemID 是內部 ID。
	Item   string `json:"item"`
	ItemID string `json:"item_id"`
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

// DefaultBuffer 是每個訂閱者的 buffer 大小。
const DefaultBuffer = 256

// Bus 把事件廣播給所有訂閱者。零值不可用，請用 New。並行安全。
type Bus struct {
	buffer int

	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	closed bool
}

// New 建立 Bus；buffer 是每個訂閱者可以積壓的事件數，小於 1 時用 DefaultBuffer。
func New(buffer int) *Bus {
	if buffer < 1 {
		buffer = DefaultBuffer
	}
	return &Bus{buffer: buffer, subs: map[*Subscription]struct{}{}}
}

// Subscription 是一個訂閱者。事件從 C 讀取；Done 關閉代表訂閱已結束
// （自己 Close、跟不上而被斷開，或 Bus 已關閉），之後不會再收到事件。
type Subscription struct {
	// C 不會被關閉，請搭配 Done 一起 select。
	C    <-chan Event
	ch   chan Event
	done chan struct{}
	bus  *Bus
}

// Done 在訂閱結束時關閉。
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Close 結束訂閱；可重複呼叫。
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.drop(s)
}

// Subscribe 加入一個訂閱者；Bus 已關閉時回傳 false。
func (b *Bus) Subscribe() (*Subscription, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, false
	}
	ch := make(chan Event, b.buffer)
	s := &Subscription{C: ch, ch: ch, done: make(chan struct{}), bus: b}
	b.subs[s] = struct{}{}
	return s, true
}

// Publish 把事件送給所有訂閱者，不會阻塞；buffer 滿的訂閱者會被斷開。
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		select {
		case s.ch <- ev:
		default:
			b.drop(s)
		}
	}
}

// Close 斷開所有訂閱者，之後的 Subscribe 都會失敗、Publish 沒有作用；可重複呼叫。
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for s := range b.subs {
		b.drop(s)
	}
}

// drop 移除訂閱者並關閉 Done；呼叫端必須持有 b.mu。
func (b *Bus) drop(s *Subscription) {
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	close(s.done)
}
