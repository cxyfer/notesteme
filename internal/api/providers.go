package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ProviderRegistry 管理 provider 的建立與讀取（SEC-R3：command provider 只能在設定檔中定義）。
// 由 OPS-C 擴充：方法簽名與回應型別可以在實作時調整。
type ProviderRegistry interface {
	Create(ctx context.Context, body map[string]any) (map[string]any, error)
	Get(ctx context.Context, id string) (map[string]any, error)
}

func (rt *routes) registerProviders(api huma.API) {
	// 骨架：Deps.Providers 還沒有實作，一律回 501。
	huma.Register(api, huma.Operation{
		OperationID: "providers-create", Method: http.MethodPost, Path: "/api/providers",
		Summary: "新增 provider（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "providers-get", Method: http.MethodGet, Path: "/api/providers/{id}",
		Summary: "讀取 provider（尚未實作）",
	}, notImplemented)
}
