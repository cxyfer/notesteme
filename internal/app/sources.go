package app

import (
	"context"
	"strings"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
)

// configureSources 設定 YouTube 以外的字幕來源（Supadata、Groq 等，SRC），
// 在 pipeline 建立前補進 deps。
//
//   - Supadata：有 API key（環境變數）才建立；連線走 deps.HTTP（netguard），key 不寫進 log。
//   - Groq：client 一律建立，沒有 API key 時轉錄前就回報 ErrNoKey（記為 not_configured，SRC-R10）；
//     base_url 與 model 用 groq 套件的預設值，還不解析設定檔的 asr.providers。連線同樣走 deps.HTTP。
//   - 其他 ASR 來源還沒實作。
func (s *Server) configureSources(ctx context.Context, deps *pipeline.Deps, env Env, cfg *config.Config) error {
	// key 前後的空白（例如 .env 的結尾換行）要去掉；只有空白視為未設定，與 doctor 一致。
	if key := strings.TrimSpace(env.Vars[config.EnvSupadataAPIKey]); key != "" {
		deps.Supadata = &supadata.Client{HTTP: deps.HTTP, APIKey: key}
	}
	deps.Groq = &groq.Client{HTTP: deps.HTTP, APIKey: strings.TrimSpace(env.Vars[config.EnvGroqAPIKey])}
	return nil
}
