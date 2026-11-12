package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/supadata"
)

// configureSources 設定 YouTube 以外的字幕來源（Supadata、ASR 等，SRC），
// 在 pipeline 建立前補進 deps。
//
//   - Supadata：有 API key（環境變數）才建立；連線走 deps.HTTP（netguard），key 不寫進 log。
//   - ASR 等其他來源還沒實作。
func (s *Server) configureSources(ctx context.Context, deps *pipeline.Deps, env Env, cfg *config.Config) error {
	if key := env.Vars[config.EnvSupadataAPIKey]; key != "" {
		deps.Supadata = &supadata.Client{HTTP: deps.HTTP, APIKey: key}
	}
	return nil
}
