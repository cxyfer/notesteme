package app

import (
	"context"
	"net/http"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/secrets"
)

// llmRegistry 組出 LLM provider 的 Registry（LLM 規格、ADR-0019）：
//   - provider：內建的 anthropic、openai，加上設定檔 llm.providers 的（同名時取代內建的 base_url 與限流）。
//   - API key：環境變數 NOTESTEME_<ID>_API_KEY 優先（L6、SEC-R1），其次是 PUT /api/settings/services 加密存放的 key，
//     每次呼叫時才讀，存完即生效、免重啟（SEC-R4）。
//   - 連線走 netguard 的 client（ADR-0014）；限流用注入的時鐘（LLM-R7）。
func (s *Server) llmRegistry(env Env, cfg *config.Config, hc *http.Client) *llm.Registry {
	configured := make(map[string]llm.ProviderConfig, len(cfg.LLMProviders))
	for id, p := range cfg.LLMProviders {
		configured[id] = llm.ProviderConfig{ID: id, Type: p.Type, BaseURL: p.BaseURL, Limits: llm.Limits{RPM: p.RPM, TPM: p.TPM}}
	}
	reg := llm.NewRegistry(configured, env.Clock)
	reg.HTTP = hc
	reg.Env = env.Vars
	if svc := s.services; svc != nil {
		reg.StoredKey = func(ctx context.Context, id string) (string, bool, error) {
			// 設定頁只能存白名單中的服務商（secrets.ValidServiceProvider）；其他 provider 只看環境變數。
			if !secrets.ValidServiceProvider(id) {
				return "", false, nil
			}
			return svc.ProviderKey(ctx, id)
		}
	}
	return reg
}
