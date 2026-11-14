package app

import "github.com/cxyfer/notesteme/internal/api"

// systemInfo 回傳 GET /api/system 的提供者。骨架：回傳 nil（路由回 501），由 OPS-B 實作。
// env 在這裡傳入，讓實作不必再改 core.go 的 apiDeps。
func (s *Server) systemInfo(_ Env) api.SystemInfo { return nil }
