package app

import "github.com/cxyfer/notesteme/internal/api"

// backupSource 回傳 POST /api/backup 的備份來源。骨架：回傳 nil（路由回 501），由 OPS-A 實作。
func (s *Server) backupSource(_ Env) api.BackupSource { return nil }
