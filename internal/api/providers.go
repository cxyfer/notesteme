package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// CodeConfigFileOnly 表示這個項目只能在設定檔中定義（SEC-R3）。
const CodeConfigFileOnly = "config_file_only"

// ProviderTypeCommand 是會在主機上執行命令的 provider 類型（SEC-R3、ADR-0013）。
const ProviderTypeCommand = "command"

// Provider 的定義來源。
const ProviderDefinedInConfigFile = "config_file"

// ProviderInfo 是 GET /api/providers/{id} 的回應。
// 只含識別資訊：command 的參數、環境變數與 API key 都不回傳，避免洩漏 secret。
type ProviderInfo struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Editable 一律是 false：設定檔中的 provider 不能透過 API 修改（SEC-R3）。
	Editable  bool   `json:"editable"`
	DefinedIn string `json:"defined_in"`
}

// ProviderRegistry 提供設定檔中定義的 provider（SEC-R3：command provider 只能在設定檔中定義）。
type ProviderRegistry interface {
	// Get 回傳指定 id 的 provider；不存在時 ok 為 false。
	Get(ctx context.Context, id string) (info ProviderInfo, ok bool)
}

type providerCreateInput struct {
	// 指標型別讓本體成為選填：Deps.Providers 為 nil 時要回 501，不能先被 Huma 擋成「缺少本體」的 400。
	// 內容只取 type 判斷，其餘欄位（可能含命令）不解析、不回顯。
	Body *map[string]any
}

type providerGetInput struct {
	ID string `path:"id" doc:"provider 的 id"`
}

type providerOutput struct{ Body ProviderInfo }

func (rt *routes) registerProviders(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "providers-create", Method: http.MethodPost, Path: "/api/providers",
		Summary: "新增 provider：command 類型只能在設定檔中定義，回 403（SEC-R3）",
	}, rt.createProvider)
	huma.Register(api, huma.Operation{
		OperationID: "providers-get", Method: http.MethodGet, Path: "/api/providers/{id}",
		Summary: "讀取設定檔中定義的 provider（唯讀）",
	}, rt.getProvider)
}

// createProvider 處理 POST /api/providers。
// command 類型一律拒絕（SEC-R3）：它等於能在主機上執行任意命令，所以不能由 API 建立，
// 即使 token 外洩也一樣。其他類型的 provider 由 DB 管理，M1 的規格沒有定義建立方式，回 501。
// 回應與日誌都不含請求內容。
func (rt *routes) createProvider(_ context.Context, in *providerCreateInput) (*providerOutput, error) {
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

// getProvider 處理 GET /api/providers/{id}：列出設定檔中的 provider，找不到回 404。
func (rt *routes) getProvider(ctx context.Context, in *providerGetInput) (*providerOutput, error) {
	if rt.d.Providers == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	info, ok := rt.d.Providers.Get(ctx, in.ID)
	if !ok {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到指定的 provider。")
	}
	return &providerOutput{Body: info}, nil
}
