package app

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/events"
)

// heartbeatInterval 是 SSE 沒有事件時送出註解行的間隔，避免 proxy 與 client 把閒置的連線當成斷線。
const heartbeatInterval = 15 * time.Second

// eventsState 是 SSE 事件匯流排的狀態（PIPE-R14）。
type eventsState struct {
	bus   *events.Bus
	clock clock.Clock
}

// startEvents 在 pipeline 啟動前建立事件匯流排，並交給 store 發布步驟狀態的變更。
func (s *Server) startEvents() {
	s.events.bus = events.New(events.DefaultBuffer)
	s.events.clock = s.store.Clock()
	s.store.SetEventSink(s.events.bus.Publish)
}

// eventsHandler 回傳 GET /api/events 的 handler（驗證由外層 middleware 負責）。
//
// 先註冊訂閱、再送出標頭與 `: ok`：client 讀到 `: ok` 之後發生的變更一定收得到。
// 串流在 client 斷線、被匯流排斷開（跟不上或 stopEvents）或 server 關閉時結束。
func (s *Server) eventsHandler() http.Handler {
	bus, clk := s.events.bus, s.events.clock
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, ok := bus.Subscribe()
		if !ok {
			http.Error(w, "server 正在關閉", http.StatusServiceUnavailable)
			return
		}
		defer sub.Close()

		// 串流會持續很久：取消這條連線的讀取期限，否則 ReadTimeout 到期會讓 request context 被取消。
		// 寫入端沒有設期限（見 server.go 的說明）。
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Time{})

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no") // 要求 nginx 之類的 proxy 不要緩衝
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(": ok\n\n")); err != nil || rc.Flush() != nil {
			return
		}

		hb := clk.NewTimer(heartbeatInterval)
		defer func() { hb.Stop() }()
		for {
			select {
			case ev := <-sub.C:
				data, err := json.Marshal(ev)
				if err != nil {
					continue
				}
				if _, err := w.Write([]byte("event: " + ev.Type + "\ndata: " + string(data) + "\n\n")); err != nil || rc.Flush() != nil {
					return
				}
			case <-hb.C():
				hb = clk.NewTimer(heartbeatInterval)
				if _, err := w.Write([]byte(": ping\n\n")); err != nil || rc.Flush() != nil {
					return
				}
			case <-sub.Done():
				return
			case <-r.Context().Done():
				return
			case <-s.root.Done():
				return
			}
		}
	})
}

// stopEvents 在 http.Server 關閉時結束所有 SSE 串流，避免 Shutdown 一直等長連線。
func (s *Server) stopEvents() {
	if s.events.bus != nil {
		s.events.bus.Close()
	}
}
