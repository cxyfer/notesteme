package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// ToolManager 管理外部工具；目前只有 yt-dlp 的更新與回退（OPS-R6、R7）。
// 由 OPS-B 擴充：方法簽名與回應型別可以在實作時調整。
type ToolManager interface {
	// UpdateYtDlp 更新 yt-dlp；pin 非空時固定在該版本。
	UpdateYtDlp(ctx context.Context, pin string) (map[string]any, error)
	// RollbackYtDlp 回到上一個版本。
	RollbackYtDlp(ctx context.Context) (map[string]any, error)
}

func (rt *routes) registerTools(api huma.API) {
	// 骨架：Deps.Tools 還沒有實作，一律回 501。
	huma.Register(api, huma.Operation{
		OperationID: "tools-ytdlp-update", Method: http.MethodPost, Path: "/api/tools/yt-dlp/update",
		Summary: "更新 yt-dlp，自我檢查失敗就回退（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "tools-ytdlp-rollback", Method: http.MethodPost, Path: "/api/tools/yt-dlp/rollback",
		Summary: "把 yt-dlp 回到上一個版本（尚未實作）",
	}, notImplemented)
}
