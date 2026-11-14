package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// ToolResolver 是所有地方共用的外部工具尋找方式（OPS-R8）：設定檔 → 環境變數 → 受管理的 venv（<data>/tools）→ PATH。
// 實際執行工具、/readyz、/api/system 與 doctor 都用它，結果才會一致。
func ToolResolver(vars map[string]string, cfg *config.Config) toolexec.Resolver {
	return toolexec.Resolver{Vars: vars, Config: cfg.Tools, ManagedDir: ytdlp.ToolsDir(cfg.Data)}
}

// ytdlpManager 回傳 yt-dlp 的管理者（每個 Server 一個：它的互斥鎖要涵蓋所有 API 入口）。
// 子程序一律經過和其他工具相同的 runner（startPipeline 設定）。
func (s *Server) ytdlpManager(env Env) *ytdlp.Manager {
	if s.ytdlp == nil {
		s.ytdlp = ytdlp.New(ytdlp.ToolsDir(s.cfg.Data), s.runner, ToolResolver(env.Vars, s.cfg), env.Clock, s.log)
	}
	return s.ytdlp
}

// toolManager 回傳外部工具（yt-dlp 更新與回退）的管理者。
func (s *Server) toolManager(env Env) api.ToolManager { return toolManager{m: s.ytdlpManager(env)} }

// toolManager 把 *ytdlp.Manager 接到 API 的 ToolManager。
type toolManager struct{ m *ytdlp.Manager }

func (t toolManager) UpdateYtDlp(ctx context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error) {
	return t.m.Update(ctx, req)
}

func (t toolManager) RollbackYtDlp(ctx context.Context) (*ytdlp.Result, error) {
	return t.m.Rollback(ctx)
}
