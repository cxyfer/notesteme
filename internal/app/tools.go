package app

import "github.com/cxyfer/notesteme/internal/api"

// toolManager 回傳外部工具（yt-dlp 更新與回退）的管理者。骨架：回傳 nil（路由回 501），由 OPS-B 實作。
func (s *Server) toolManager(_ Env) api.ToolManager { return nil }
