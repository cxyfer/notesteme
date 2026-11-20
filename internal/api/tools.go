package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/toolexec"
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
	// CodeToolStateCorrupt 表示 yt-dlp 的 state.json 損毀；訊息指出檔案位置，刪除後可以重來。
	CodeToolStateCorrupt = "tool_state_corrupt"
	// CodeToolUnsupportedPlatform 表示這個平台不支援受管理的 yt-dlp。
	CodeToolUnsupportedPlatform = "tool_unsupported_platform"
)

// installFailedMessage 是安裝失敗時回給 client 的固定訊息；pip 的輸出只寫進 log。
const installFailedMessage = "yt-dlp 安裝失敗，詳見 server log。"

type ytdlpUpdateInput struct {
	// Body 可以省略：沒有 body 就是更新到最新版。
	Body *struct {
		Pin   string `json:"pin,omitempty" doc:"固定在這個版本（YYYY.MM.DD 或 YYYY.MM.DD.N）；固定後手動與排程的更新都略過"`
		Unpin bool   `json:"unpin,omitempty" doc:"解除固定"`
	}
}

type ytdlpOutput struct{ Body ytdlp.Result }

func (rt *routes) registerTools(api huma.API) {
	registerOwnerOnly(api, huma.Operation{
		OperationID: "tools-ytdlp-update", Method: http.MethodPost, Path: "/api/tools/yt-dlp/update",
		Summary: "更新 yt-dlp，自我檢查失敗就回退；可以固定或解除固定版本（只有 owner）",
		// body 很小，但更新要跑好幾分鐘：huma 預設在讀 body 時對連線設 5 秒的讀取期限，期限過了之後
		// net/http 會以為 client 斷線而取消請求的 context，更新就會被中斷。BodyReadTimeout 設為負值
		// 讓 huma 清除這個期限；body 的大小另外限制。
		BodyReadTimeout: -1, MaxBodyBytes: 4 << 10,
	}, "更新 yt-dlp", rt.updateYtDlp)
	registerOwnerOnly(api, huma.Operation{
		OperationID: "tools-ytdlp-rollback", Method: http.MethodPost, Path: "/api/tools/yt-dlp/rollback",
		Summary: "把 yt-dlp 回到上一個版本（只有 owner）",
	}, "回退 yt-dlp", rt.rollbackYtDlp)
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
	case errors.Is(err, ytdlp.ErrStateCorrupt):
		return newError(http.StatusConflict, CodeToolStateCorrupt, err.Error())
	case errors.Is(err, ytdlp.ErrUnsupportedPlatform):
		return newError(http.StatusConflict, CodeToolUnsupportedPlatform, err.Error())
	case errors.Is(err, ytdlp.ErrInstall):
		// 錯誤訊息帶有 pip 的 stderr 尾段：只寫進 log，而且先遮蔽 cookie 內容；回應不含這些內容。
		rt.d.Log.WarnContext(ctx, "安裝 yt-dlp 失敗", "error", string(toolexec.RedactCookieLines([]byte(err.Error()))))
		return newError(http.StatusBadGateway, CodeUpstream, installFailedMessage)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return newError(http.StatusServiceUnavailable, CodeInternal, "更新被中斷，沒有做任何變更。")
	}
	rt.d.Log.ErrorContext(ctx, "yt-dlp 更新失敗", "error", err)
	return asError(err)
}
