package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// SystemInfo 提供 GET /api/system 的內容：生效的設定與其來源、外部工具（SEC-R1、OPS-R8）。
// 由 OPS-B 擴充：方法簽名與回應型別可以在實作時調整。
type SystemInfo interface {
	Info(ctx context.Context) (map[string]any, error)
}

func (rt *routes) registerSystem(api huma.API) {
	// 骨架：Deps.System 還沒有實作，一律回 501。
	huma.Register(api, huma.Operation{
		OperationID: "system-get", Method: http.MethodGet, Path: "/api/system",
		Summary: "生效的設定與其來源、外部工具（尚未實作）",
	}, notImplemented)
}
