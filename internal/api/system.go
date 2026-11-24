package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// SystemInfo 提供 GET /api/system 的內容：生效的設定與其來源、版本、外部工具（SEC-R1、OPS-R8、OPS-R22）。
type SystemInfo interface {
	Info(ctx context.Context) (*SystemView, error)
}

// SystemView 是 GET /api/system 的回應。
type SystemView struct {
	// Version 與 SchemaVersion 是程式與資料庫的版本（OPS-R22、NFR-OBS-3）。
	Version       string `json:"version" doc:"程式版本（建置時寫入；開發版是 dev）"`
	SchemaVersion int64  `json:"schema_version,omitempty" doc:"資料庫的 schema 版本（最新已套用的 migration）；讀不到時省略"`
	Listen        string `json:"listen" doc:"HTTP 監聽位址"`
	ListenSource  string `json:"listen_source" doc:"default、config_file 或 env"`
	Data          string `json:"data" doc:"資料目錄"`
	DataSource    string `json:"data_source" doc:"default、config_file 或 env"`
	// Tools 的 key 是工具名稱（yt-dlp、ffmpeg、ffprobe、deno）。
	Tools map[string]ToolView `json:"tools"`
	YtDlp YtDlpView           `json:"yt_dlp"`
	// Alerts 是目前成立的告警（OPS-R13）；Backup 是每日備份的狀態（OPS-R11）。總覽（LIB-09）讀這兩個欄位。
	Alerts []AlertView `json:"alerts" doc:"目前成立的告警，依開始的時間排序"`
	Backup BackupView  `json:"backup"`
}

// AlertView 是一則成立中的告警（OPS-R13～R18）。
type AlertView struct {
	Kind    string    `json:"kind" doc:"backup_failed、stuck_task、job_stale、disk_usage、cookie_stale、ytdlp_failure_rate 或 ytdlp_update_failed"`
	Key     string    `json:"key" doc:"告警的對象，例如影音 id、<kind>:<key>、/data、平台"`
	Message string    `json:"message"`
	Since   time.Time `json:"since" doc:"條件開始成立的時間"`
}

// BackupView 是每日備份（backup.daily）的狀態；沒有時為 null（例如停用時 next_run_at 為 null）。
type BackupView struct {
	LastOKAt  *time.Time `json:"last_ok_at" doc:"最近一次成功的時間"`
	LastError *string    `json:"last_error" doc:"最近一次失敗的原因；成功後為 null"`
	NextRunAt *time.Time `json:"next_run_at" doc:"下一次執行的時間；停用時為 null"`
}

// ToolView 是一個外部工具生效的路徑與來源（OPS-R8）。
type ToolView struct {
	Path string `json:"path" doc:"生效的路徑；PATH 中找不到時為空字串"`
	// Source 是 config_file、env、managed 或 path。
	Source    string `json:"source" doc:"config_file、env、managed 或 path"`
	Available bool   `json:"available" doc:"路徑是否可以執行"`
	Error     string `json:"error,omitempty" doc:"不能執行的原因"`
	// Version 是工具回報的版本（OPS-R22）：結果會快取，第一次查詢最多等 2 秒；不能執行、逾時或認不出版本時省略。
	Version string `json:"version,omitempty" doc:"工具回報的版本；不知道時省略"`
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
	registerOwnerOnly(api, huma.Operation{
		OperationID: "system-get", Method: http.MethodGet, Path: "/api/system",
		Summary: "生效的設定與其來源、程式與 schema 的版本、外部工具的路徑、來源與版本（只有 owner）",
	}, "查看系統資訊", rt.getSystem)
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
