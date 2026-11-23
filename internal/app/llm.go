package app

import (
	"context"
	"net/http"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/vault"
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

// outputCatalog 回答設定檔定義的 vault 與 hook 存不存在（pipeline.OutputCatalog，VAULT-R1、R10）。
type outputCatalog struct {
	vaults map[string]config.Vault
	hooks  map[string]config.Hook
}

// vaultDefs 把設定檔的 vault 與 hook 轉成 pipeline 使用的定義（VAULT-R1、R10）。
func vaultDefs(cfg *config.Config) (map[string]vault.Def, map[string]vault.Hook) {
	vs := make(map[string]vault.Def, len(cfg.Vaults))
	for id, v := range cfg.Vaults {
		vs[id] = vault.Def{ID: id, Adapter: v.Adapter, Path: v.Path, Remote: v.Remote, Branch: v.Branch, Author: v.Author, Base: v.Base}
	}
	hs := make(map[string]vault.Hook, len(cfg.Hooks))
	for id, h := range cfg.Hooks {
		hs[id] = vault.Hook{ID: id, Argv: h.Argv, Timeout: h.Timeout, MaxOutput: h.MaxOutput, EnvAllowlist: h.EnvAllowlist}
	}
	return vs, hs
}

func (c outputCatalog) HasVault(id string) bool { _, ok := c.vaults[id]; return ok }
func (c outputCatalog) HasHook(id string) bool  { _, ok := c.hooks[id]; return ok }

// 正式環境的目錄必須實作 pipeline 的介面：GEM-R1 的 type 檢查與 VAULT-R1、R10 的 id 檢查都靠型別斷言，
// 方法簽名改了時要在編譯時失敗，而不是靜默略過檢查（閘門讓 @M3 的 422 場景驗證不到）。
var (
	_ pipeline.ProviderCatalog = (*llm.Registry)(nil)
	_ pipeline.ProviderTypes   = (*llm.Registry)(nil)
	_ pipeline.OutputCatalog   = outputCatalog{}
)
