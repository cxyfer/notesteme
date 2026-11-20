package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/config"
)

// configProviders 是 api.ProviderRegistry：列出設定檔 asr.providers 中的 provider。
// 它們都來自設定檔，所以一律唯讀（SEC-R3）；回應只有識別資訊，不含 command 的參數與環境變數。
type configProviders map[string]config.ASRProvider

func (p configProviders) Get(_ context.Context, id string) (api.ProviderInfo, bool) {
	prov, ok := p[id]
	if !ok {
		return api.ProviderInfo{}, false
	}
	return api.ProviderInfo{ID: prov.ID, Type: prov.Type, Editable: false, DefinedIn: api.ProviderDefinedInConfigFile}, true
}

// providerRegistry 回傳 provider 的登錄處；設定檔沒有定義任何 provider 時仍然有效（查詢一律 404）。
func (s *Server) providerRegistry(_ Env) api.ProviderRegistry {
	return configProviders(s.cfg.ASRProviders)
}
