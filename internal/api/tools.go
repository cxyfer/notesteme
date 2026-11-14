package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// ToolManager 管理外部工具；目前只有 yt-dlp 的更新與回退（OPS-R6、R7）。
// *ytdlp.Manager 實作它。
type ToolManager interface {
	// UpdateYtDlp 更新 yt-dlp：自我檢查失敗就回退（結果的 RolledBack 為 true，不是錯誤）。
	UpdateYtDlp(ctx context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error)
	// RollbackYtDlp 回到上一個版本。
	RollbackYtDlp(ctx context.Context) (*ytdlp.Result, error)
}

// 外部工具管理的錯誤代碼。
const (
	CodeToolExternallyManaged = "tool_externally_managed"
	CodeUpdateInProgress      = "update_in_progress"
	CodeNoPreviousVersion     = "no_previous_version"
)

// requireOwner 只讓 owner（session 或本機）通過：token 即使帶了 scope 也不行，因為這些操作會執行程式或洩漏管理資訊。
func requireOwner(ctx context.Context, what string) error {
	if p, ok := principalFrom(ctx); ok && p.Kind == "token" {
		return newError(http.StatusForbidden, CodeForbidden, "只有 owner 可以"+what+"。")
	}
	return nil
}

type ytdlpUpdateInput struct {
	// Body 可以省略：沒有 body 就是更新到最新版。
	Body *struct {
		Pin   string `json:"pin,omitempty" doc:"固定在這個版本（YYYY.MM.DD 或 YYYY.MM.DD.N）；固定後手動與排程的更新都略過"`
		Unpin bool   `json:"unpin,omitempty" doc:"解除固定"`
	}
}

type ytdlpOutput struct{ Body ytdlp.Result }

func (rt *routes) registerTools(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "tools-ytdlp-update", Method: http.MethodPost, Path: "/api/tools/yt-dlp/update",
		Summary: "更新 yt-dlp，自我檢查失敗就回退；可以固定或解除固定版本（只有 owner）",
		// body 很小，但更新要跑好幾分鐘：huma 預設在讀 body 時對連線設 5 秒的讀取期限，期限過了之後
		// net/http 會以為 client 斷線而取消請求的 context，更新就會被中斷。BodyReadTimeout 設為負值
		// 讓 huma 清除這個期限；body 的大小另外限制。
		BodyReadTimeout: -1, MaxBodyBytes: 4 << 10,
	}, rt.updateYtDlp)
	huma.Register(api, huma.Operation{
		OperationID: "tools-ytdlp-rollback", Method: http.MethodPost, Path: "/api/tools/yt-dlp/rollback",
		Summary: "把 yt-dlp 回到上一個版本（只有 owner）",
	}, rt.rollbackYtDlp)
}

func (rt *routes) updateYtDlp(ctx context.Context, in *ytdlpUpdateInput) (*ytdlpOutput, error) {
	if err := requireOwner(ctx, "更新 yt-dlp"); err != nil {
		return nil, err
	}
	if rt.d.Tools == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	var req ytdlp.UpdateRequest
	if in.Body != nil {
		req = ytdlp.UpdateRequest{Pin: in.Body.Pin, Unpin: in.Body.Unpin}
	}
	res, err := rt.d.Tools.UpdateYtDlp(ctx, req)
	if err != nil {
		return nil, rt.mapToolErr(ctx, err)
	}
	return &ytdlpOutput{Body: *res}, nil
}

func (rt *routes) rollbackYtDlp(ctx context.Context, _ *struct{}) (*ytdlpOutput, error) {
	if err := requireOwner(ctx, "回退 yt-dlp"); err != nil {
		return nil, err
	}
	if rt.d.Tools == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	res, err := rt.d.Tools.RollbackYtDlp(ctx)
	if err != nil {
		return nil, rt.mapToolErr(ctx, err)
	}
	return &ytdlpOutput{Body: *res}, nil
}

// mapToolErr 把 yt-dlp 管理的錯誤對應到 HTTP 狀態碼；訊息是我們自己寫的繁體中文，可以直接給使用者。
func (rt *routes) mapToolErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ytdlp.ErrInvalidVersion), errors.Is(err, ytdlp.ErrInvalidRequest):
		return newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error())
	case errors.Is(err, ytdlp.ErrExternallyManaged):
		return newError(http.StatusConflict, CodeToolExternallyManaged, err.Error())
	case errors.Is(err, ytdlp.ErrBusy):
		return newError(http.StatusConflict, CodeUpdateInProgress, err.Error())
	case errors.Is(err, ytdlp.ErrNoPrevious):
		return newError(http.StatusConflict, CodeNoPreviousVersion, err.Error())
	case errors.Is(err, ytdlp.ErrInstall):
		rt.d.Log.WarnContext(ctx, "安裝 yt-dlp 失敗", "error", err)
		return newError(http.StatusBadGateway, CodeUpstream, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return newError(http.StatusServiceUnavailable, CodeInternal, "更新被中斷，沒有做任何變更。")
	}
	rt.d.Log.ErrorContext(ctx, "yt-dlp 更新失敗", "error", err)
	return asError(err)
}
