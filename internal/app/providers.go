package app

import "github.com/cxyfer/notesteme/internal/api"

// providerRegistry 回傳 provider 的登錄處。骨架：回傳 nil（路由回 501），由 OPS-C 實作。
func (s *Server) providerRegistry(_ Env) api.ProviderRegistry { return nil }
