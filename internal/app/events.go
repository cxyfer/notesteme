package app

import "net/http"

// eventsState 是 SSE 事件匯流排的狀態（PIPE-R14）。目前是空殼，GET /api/events 回應 501。
type eventsState struct{}

// startEvents 在 pipeline 啟動前建立事件匯流排，讓 store 的狀態變更可以發布出去。
func (s *Server) startEvents() {}

// eventsHandler 回傳 GET /api/events 的 handler；nil 代表尚未實作。
func (s *Server) eventsHandler() http.Handler { return nil }

// stopEvents 在 http.Server 關閉時結束所有 SSE 串流，避免 Shutdown 一直等長連線。
func (s *Server) stopEvents() {}
