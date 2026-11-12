package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// configureSources 設定 YouTube 以外的字幕來源（Supadata、ASR 等，SRC），
// 在 pipeline 建立前補進 deps。目前沒有其他來源。
func (s *Server) configureSources(ctx context.Context, deps *pipeline.Deps, env Env, cfg *config.Config) error {
	return nil
}
