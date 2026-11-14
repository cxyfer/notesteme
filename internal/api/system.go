package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// SystemInfo 提供 GET /api/system 的內容：生效的設定與其來源、外部工具（SEC-R1、OPS-R8）。
type SystemInfo interface {
	Info(ctx context.Context) (*SystemView, error)
}

// SystemView 是 GET /api/system 的回應。
type SystemView struct {
	Listen       string `json:"listen" doc:"HTTP 監聽位址"`
	ListenSource string `json:"listen_source" doc:"default、config_file 或 env"`
	Data         string `json:"data" doc:"資料目錄"`
	DataSource   string `json:"data_source" doc:"default、config_file 或 env"`
	// Tools 的 key 是工具名稱（yt-dlp、ffmpeg、ffprobe、deno）。
	Tools map[string]ToolView `json:"tools"`
	YtDlp YtDlpView           `json:"yt_dlp"`
}

// ToolView 是一個外部工具生效的路徑與來源（OPS-R8）。
type ToolView struct {
	Path string `json:"path" doc:"生效的路徑；PATH 中找不到時為空字串"`
	// Source 是 config_file、env、managed 或 path。
	Source    string `json:"source" doc:"config_file、env、managed 或 path"`
	Available bool   `json:"available" doc:"路徑是否可以執行"`
	Error     string `json:"error,omitempty" doc:"不能執行的原因"`
}

// YtDlpView 是 yt-dlp 的固定版本與上一版（OPS-R6、R7）；沒有時為空字串。
// 狀態檔無法讀取時 Pinned 與 Previous 為 null，Error 說明原因（其他欄位照常回傳，不讓 /api/system 整個失敗）。
type YtDlpView struct {
	Pinned   *string `json:"pinned"`
	Previous *string `json:"previous"`
	Error    string  `json:"error,omitempty" doc:"狀態檔無法使用時的簡短說明"`
}

// Str 回傳 s 的指標，供組 YtDlpView 使用。
func Str(s string) *string { return &s }

type systemOutput struct{ Body SystemView }

func (rt *routes) registerSystem(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "system-get", Method: http.MethodGet, Path: "/api/system",
		Summary: "生效的設定與其來源、外部工具的路徑與來源（只有 owner）",
	}, rt.getSystem)
}

// getSystem 只有 owner（session 或本機）可以使用：路徑與設定的來源屬於管理資訊。
func (rt *routes) getSystem(ctx context.Context, _ *struct{}) (*systemOutput, error) {
	if err := requireOwner(ctx, "查看系統資訊"); err != nil {
		return nil, err
	}
	if rt.d.System == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	v, err := rt.d.System.Info(ctx)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "讀取系統資訊失敗", "error", err)
		return nil, asError(err)
	}
	return &systemOutput{Body: *v}, nil
}
