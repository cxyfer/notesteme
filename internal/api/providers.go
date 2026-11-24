package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/errclass"
)

// CodeConfigFileOnly 表示這個項目只能在設定檔中定義（SEC-R3）。
const CodeConfigFileOnly = "config_file_only"

// ProviderTypeCommand 是會在主機上執行命令的 provider 類型（SEC-R3、ADR-0013）。
const ProviderTypeCommand = "command"

// Provider 的定義來源。
const (
	ProviderDefinedInConfigFile = "config_file"
	ProviderDefinedInBuiltin    = "builtin"
)

// ProviderInfo 是 GET /api/providers 與 GET /api/providers/{id} 的一項。
// 只含識別資訊與停用公告：command 的參數、環境變數與 API key 都不回傳，避免洩漏 secret。
type ProviderInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Kind 是 llm、asr（語音轉錄）或 transcript（字幕服務）。
	Kind string `json:"kind" enum:"llm,asr,transcript"`
	// Editable 一律是 false：內建與設定檔中的 provider 都不能透過 API 修改（SEC-R3）；API key 在 PUT /api/settings/services 設定。
	Editable  bool   `json:"editable"`
	DefinedIn string `json:"defined_in" enum:"builtin,config_file"`
	// Testable 為 false 時不提供測試連線，原因在 untestable_reason（SEC-R11）。
	Testable         bool   `json:"testable"`
	UntestableReason string `json:"untestable_reason,omitempty"`
	// Deprecations 是模型的停用公告；沒有時是空陣列（SEC-R11）。
	Deprecations []ProviderDeprecation `json:"deprecations"`
}

// ProviderDeprecation 是一個模型的停用公告。
type ProviderDeprecation struct {
	Model       string `json:"model"`
	After       string `json:"after" doc:"停止服務的日期（YYYY-MM-DD）"`
	Replacement string `json:"replacement,omitempty"`
	Message     string `json:"message,omitempty"`
}

// ProviderTestResult 是 POST /api/providers/{id}/test 成功的回應。
type ProviderTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Message   string `json:"message"`
}

// ProviderRegistry 是 provider 目錄（SEC-R3、SEC-R11）：內建與設定檔定義的 provider。
type ProviderRegistry interface {
	// Get 回傳指定 id 的 provider；kind 為空時依固定順序（asr、llm、transcript）取第一個。不存在時 ok 為 false。
	Get(ctx context.Context, id, kind string) (info ProviderInfo, ok bool)
	// List 依 id 排序列出所有 provider。
	List(ctx context.Context) []ProviderInfo
	// Test 測試連線。錯誤是 ErrProviderNotFound、ErrProviderNotTestable，或帶訊息的 *errclass.Error
	// （needs_action → 409，其他 → 502）。
	Test(ctx context.Context, id, kind string) (ProviderTestResult, error)
}

var (
	// ErrProviderNotFound 表示目錄中沒有這個 provider。
	ErrProviderNotFound = errors.New("找不到指定的 provider。")
	// ErrProviderNotTestable 表示這個 provider 不提供測試連線。
	ErrProviderNotTestable = errors.New("這個 provider 不提供測試連線。")
)

type providerCreateInput struct {
	// 指標型別讓本體成為選填：Deps.Providers 為 nil 時要回 501，不能先被 Huma 擋成「缺少本體」的 400。
	// 內容只取 type 判斷，其餘欄位（可能含命令）不解析、不回顯。
	Body *map[string]any
}

type providerGetInput struct {
	ID   string `path:"id" doc:"provider 的 id"`
	Kind string `query:"kind" enum:"llm,asr,transcript" doc:"同一個 id 有多種 provider 時指定種類；省略時依 asr、llm、transcript 的順序"`
}

type providerOutput struct{ Body ProviderInfo }

type providerListOutput struct {
	Body struct {
		Providers []ProviderInfo `json:"providers"`
	}
}

type providerTestOutput struct{ Body ProviderTestResult }

func (rt *routes) registerProviders(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "providers-create", Method: http.MethodPost, Path: "/api/providers",
		Summary: "新增 provider：command 類型只能在設定檔中定義，回 403（SEC-R3）",
	}, auth.ScopeAdmin, rt.createProvider)
	registerScoped(api, huma.Operation{
		OperationID: "providers-get", Method: http.MethodGet, Path: "/api/providers/{id}",
		Summary: "讀取內建或設定檔中定義的 provider（唯讀）",
	}, auth.ScopeRead, rt.getProvider)
	registerScoped(api, huma.Operation{
		OperationID: "providers-list", Method: http.MethodGet, Path: "/api/providers",
		Summary: "列出內建與設定檔中定義的 provider（唯讀，含模型的停用公告，SEC-R11）",
	}, auth.ScopeRead, rt.listProviders)
	registerScoped(api, huma.Operation{
		OperationID: "providers-test", Method: http.MethodPost, Path: "/api/providers/{id}/test",
		Summary: "測試 provider 的連線：送一個帶認證、不產生費用的請求；command 只檢查指令，不執行（SEC-R11）",
	}, auth.ScopeAdmin, rt.testProvider)
}

// createProvider 處理 POST /api/providers。
// command 類型一律拒絕（SEC-R3）：它等於能在主機上執行任意命令，所以不能由 API 建立，
// 即使 token 外洩也一樣。其他類型的 provider 由 DB 管理，M1 的規格沒有定義建立方式，回 501。
// 回應與日誌都不含請求內容。
func (rt *routes) createProvider(ctx context.Context, in *providerCreateInput) (*providerOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Providers == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	var typ string
	if in.Body != nil {
		typ, _ = (*in.Body)["type"].(string)
	}
	typ = strings.TrimSpace(typ)
	if typ == "" {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請指定 provider 的 type。")
	}
	if strings.EqualFold(typ, ProviderTypeCommand) {
		return nil, newError(http.StatusForbidden, CodeConfigFileOnly, "這個項目只能在設定檔中定義。")
	}
	return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
}

// getProvider 處理 GET /api/providers/{id}：內建或設定檔中的 provider，找不到回 404。
func (rt *routes) getProvider(ctx context.Context, in *providerGetInput) (*providerOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Providers == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	info, ok := rt.d.Providers.Get(ctx, in.ID, in.Kind)
	if !ok {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到指定的 provider。")
	}
	return &providerOutput{Body: info}, nil
}

// listProviders 處理 GET /api/providers（SEC-R11）。
func (rt *routes) listProviders(ctx context.Context, _ *struct{}) (*providerListOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Providers == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	out := &providerListOutput{}
	out.Body.Providers = rt.d.Providers.List(ctx)
	if out.Body.Providers == nil {
		out.Body.Providers = []ProviderInfo{}
	}
	return out, nil
}

// testProvider 處理 POST /api/providers/{id}/test（SEC-R11）：
// 成功 200 {ok, latency_ms, message}；缺 API key、key 被拒絕、找不到 command 的指令是 409 needs_action（帶 actions）；
// provider 失敗、逾時、連線失敗是 502 upstream_failed；不提供測試的 provider 回 501。
// 訊息不含 API key 與 provider 回應的內容；log 只記錯誤類別。
func (rt *routes) testProvider(ctx context.Context, in *providerGetInput) (*providerTestOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Providers == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	res, err := rt.d.Providers.Test(ctx, in.ID, in.Kind)
	if err == nil {
		res.OK = true
		return &providerTestOutput{Body: res}, nil
	}
	switch {
	case errors.Is(err, ErrProviderNotFound):
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到指定的 provider。")
	case errors.Is(err, ErrProviderNotTestable):
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個 provider 不提供測試連線。")
	}
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Message == "" {
		rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "測試 provider 連線失敗", slog.String("provider", in.ID))
		return nil, newError(http.StatusBadGateway, CodeUpstream, "測試連線失敗。")
	}
	if ce.Class == errclass.ClassNeedsAction {
		e := newError(http.StatusConflict, CodeNeedsAction, ce.Message)
		e.Actions = ce.Actions
		return nil, e
	}
	rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "測試 provider 連線失敗", slog.String("provider", in.ID), slog.String("class", string(ce.Class)), slog.String("detail", ce.Detail))
	return nil, newError(http.StatusBadGateway, CodeUpstream, ce.Message)
}
