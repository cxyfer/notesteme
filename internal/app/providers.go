package app

import (
	"context"
	"errors"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/providers"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
)

// newProviderCatalog 組出設定頁的 provider 目錄（SEC-R3、SEC-R11）：LLM 與字幕來源取自 pipeline 的依賴，
// 測試連線經同一個 netguard client（ADR-0014），API key 的來源與正式執行相同（環境變數優先，其次是資料庫）。
func newProviderCatalog(cfg *config.Config, deps pipeline.Deps) *providers.Catalog {
	c := &providers.Catalog{ASR: cfg.ASRProviders, HTTP: deps.HTTP, LLMConfigured: map[string]bool{}}
	if deps.LLM != nil {
		c.LLM = deps.LLM
	}
	for id := range cfg.LLMProviders {
		c.LLMConfigured[id] = true
	}
	// 介面值裡放 nil 指標會變成非 nil 的介面：只有真的有 client 時才放。
	if g, ok := deps.Groq.(*groq.Client); ok && g != nil {
		c.Groq = g
	}
	if sd, ok := deps.Supadata.(*supadata.Client); ok && sd != nil {
		c.Supadata = sd
	}
	return c
}

// catalogRegistry 是 api.ProviderRegistry：把 providers.Catalog 轉成 API 的型別。
// 回應只有識別資訊與停用公告，不含 command 的參數、環境變數與 API key。
type catalogRegistry struct{ c *providers.Catalog }

func (r catalogRegistry) Get(_ context.Context, id, kind string) (api.ProviderInfo, bool) {
	in, ok := r.c.Get(id, kind)
	if !ok {
		return api.ProviderInfo{}, false
	}
	return apiProviderInfo(in), true
}

func (r catalogRegistry) List(context.Context) []api.ProviderInfo {
	list := r.c.List()
	out := make([]api.ProviderInfo, 0, len(list))
	for _, in := range list {
		out = append(out, apiProviderInfo(in))
	}
	return out
}

func (r catalogRegistry) Test(ctx context.Context, id, kind string) (api.ProviderTestResult, error) {
	res, err := r.c.Test(ctx, id, kind)
	switch {
	case errors.Is(err, providers.ErrNotFound):
		return api.ProviderTestResult{}, api.ErrProviderNotFound
	case errors.Is(err, providers.ErrNotTestable):
		return api.ProviderTestResult{}, api.ErrProviderNotTestable
	case err != nil:
		return api.ProviderTestResult{}, err
	}
	return api.ProviderTestResult{OK: true, LatencyMS: res.Latency.Milliseconds(), Message: res.Message}, nil
}

func apiProviderInfo(in providers.Info) api.ProviderInfo {
	deps := make([]api.ProviderDeprecation, 0, len(in.Deprecations))
	for _, d := range in.Deprecations {
		deps = append(deps, api.ProviderDeprecation{Model: d.Model, After: d.After, Replacement: d.Replacement, Message: d.Message})
	}
	return api.ProviderInfo{
		ID: in.ID, Type: in.Type, Kind: in.Kind, Editable: false, DefinedIn: in.DefinedIn,
		Testable: in.Testable, UntestableReason: in.UntestableReason, Deprecations: deps,
	}
}

// providerRegistry 回傳 provider 目錄；pipeline 還沒建立時（沒有 LLM 與字幕來源）只列出內建項目與設定檔的 asr.providers。
func (s *Server) providerRegistry(_ Env) api.ProviderRegistry {
	c := s.providerCatalog
	if c == nil {
		c = &providers.Catalog{ASR: s.cfg.ASRProviders}
	}
	return catalogRegistry{c}
}
