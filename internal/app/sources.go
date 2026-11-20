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
//   - 金鑰優先順序（SEC-R1）：環境變數優先；環境變數未設定且有 services（DB + Keyring）時，
//     透過 KeyFunc 於每次執行時動態解密讀取，讓 PUT /api/settings/services 不需重啟即生效。
//   - Supadata：有環境變數或有 services 時建立；連線走 deps.HTTP（netguard），key 不寫進 log。
//   - Groq：client 一律建立，沒有 API key 時轉錄前就回報 ErrNoKey（記為 not_configured，SRC-R10）；
//     base_url 與 model 用 groq 套件的預設值，還不解析設定檔的 asr.providers。連線同樣走 deps.HTTP。
func (s *Server) configureSources(_ context.Context, deps *pipeline.Deps, env Env, _ *config.Config) error {
	var supadataKeyFunc, groqKeyFunc func(context.Context) (string, error)
	if s.services != nil {
		svc := s.services
		supadataKeyFunc = providerKeyFunc(svc, supadata.ID)
		groqKeyFunc = providerKeyFunc(svc, groq.ID)
	}

	// key 前後的空白（例如 .env 的結尾換行）要去掉；只有空白視為未設定，與 doctor 一致。
	if key := strings.TrimSpace(env.Vars[config.EnvSupadataAPIKey]); key != "" || supadataKeyFunc != nil {
		deps.Supadata = &supadata.Client{HTTP: deps.HTTP, APIKey: key, KeyFunc: supadataKeyFunc}
	}
	deps.Groq = &groq.Client{
		HTTP:    deps.HTTP,
		APIKey:  strings.TrimSpace(env.Vars[config.EnvGroqAPIKey]),
		KeyFunc: groqKeyFunc,
	}
	return nil
}

func providerKeyFunc(svc interface {
	ProviderKey(context.Context, string) (string, bool, error)
}, id string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		k, ok, err := svc.ProviderKey(ctx, id)
		if err != nil || !ok {
			return "", err
		}
		return k, nil
	}
}
