package hubapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
)

// Access 是路由需要的存取層級（HACC 規格「介面」的權限欄）：不需要登入、只能用 session、任何有效身分，或一個權限。
type Access string

const (
	// AccessPublic 不需要登入（HACC-R13 列出的例外）。
	AccessPublic Access = "public"
	// AccessSession 只接受瀏覽器的 session（PAT 不能用）。
	AccessSession Access = "session"
	// AccessAuthenticated 接受 session 或 PAT，不要求特定權限。
	AccessAuthenticated Access = "authenticated"
)

// perm 把權限名稱轉成 Access。
func perm(p string) Access { return Access(p) }

// Stub 是還沒有實作的路由：回 501 not_implemented。
// 這張表與已實作路由的 Access 就是 Hub 的權限矩陣（HACC-R13）：middleware 依 Access 在讀取 body 與查詢資源之前檢查，
// 所以實作某條路由時，從這裡移到自己的檔案，Access 一併帶走（stubs_test.go 的清單一併更新）。
type Stub struct {
	ID, Method, Path, Summary string
	Access                    Access
	// Spec 是定義這條路由的規則（說明用）。
	Spec string
}

// Stubs 是 HACC、HREP 規格「介面」章節的 /api/v1 路由（M5-S0）。
var Stubs = []Stub{
	// HACC：登入、登出、/me 與登入方式的連結、移除由 M5-S1 實作（auth.go）；Hub token、群組指派與稽核紀錄由 M5-S2 實作（tokens.go）。
	{ID: "me-shares", Method: http.MethodGet, Path: "/api/v1/me/shares", Access: perm(hubconfig.PermUpload), Spec: "HACC-R16",
		Summary: "我的分享與版本狀態（尚未實作）"},
	// HACC：下架申請（S5）
	{ID: "takedowns-create", Method: http.MethodPost, Path: "/api/v1/takedowns", Access: AccessPublic, Spec: "HACC-R15",
		Summary: "提出下架申請，不需要登入（尚未實作）"},
	{ID: "takedowns-get", Method: http.MethodGet, Path: "/api/v1/takedowns/{id}", Access: AccessPublic, Spec: "HACC-R15",
		Summary: "查詢下架申請的狀態（尚未實作）"},
	// HACC：管理（S2、S5）
	{ID: "admin-takedowns-list", Method: http.MethodGet, Path: "/api/v1/admin/takedowns", Access: perm(hubconfig.PermModerate), Spec: "HACC-R16",
		Summary: "列出下架申請（尚未實作）"},
	{ID: "admin-takedowns-decide", Method: http.MethodPost, Path: "/api/v1/admin/takedowns/{id}/decision", Access: perm(hubconfig.PermModerate), Spec: "HACC-R16",
		Summary: "決定下架申請（尚未實作）"},
	{ID: "admin-reports-list", Method: http.MethodGet, Path: "/api/v1/admin/reports", Access: perm(hubconfig.PermModerate), Spec: "HREP-R14",
		Summary: "列出檢舉（尚未實作）"},
	{ID: "admin-reports-decide", Method: http.MethodPost, Path: "/api/v1/admin/reports/{id}/decision", Access: perm(hubconfig.PermModerate), Spec: "HREP-R15",
		Summary: "處理檢舉（尚未實作）"},
	{ID: "admin-versions-hide", Method: http.MethodPost, Path: "/api/v1/admin/versions/{id}/hide", Access: perm(hubconfig.PermModerate), Spec: "HREP-R15",
		Summary: "隱藏版本（尚未實作）"},
	{ID: "admin-users-list", Method: http.MethodGet, Path: "/api/v1/admin/users", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R12",
		Summary: "列出使用者（尚未實作）"},
	{ID: "admin-users-suspend", Method: http.MethodPost, Path: "/api/v1/admin/users/{id}/suspend", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R17",
		Summary: "停用帳號（尚未實作）"},
	{ID: "admin-users-unsuspend", Method: http.MethodPost, Path: "/api/v1/admin/users/{id}/unsuspend", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R17",
		Summary: "恢復帳號（尚未實作）"},
	// HREP：分享、撤回與版本詳情由 M5-S3 實作（repository.go）；篩選、影片頁、取用與 lookup 由 M5-S4 實作（listing.go、download.go）；社群（S5）
	{ID: "versions-vote-put", Method: http.MethodPut, Path: "/api/v1/versions/{id}/vote", Access: perm(hubconfig.PermRead), Spec: "HREP-R13",
		Summary: "投票（尚未實作）"},
	{ID: "versions-vote-delete", Method: http.MethodDelete, Path: "/api/v1/versions/{id}/vote", Access: perm(hubconfig.PermRead), Spec: "HREP-R13",
		Summary: "收回投票（尚未實作）"},
	{ID: "versions-reports-create", Method: http.MethodPost, Path: "/api/v1/versions/{id}/reports", Access: perm(hubconfig.PermRead), Spec: "HREP-R14",
		Summary: "檢舉版本（尚未實作）"},
}

// RawStubs 是不經過 Huma 的路由（MCP 的 JSON-RPC），同樣先回 501。瀏覽器的 OAuth 流程（/auth/<provider>/start|callback）由 M5-S1 實作（auth.go）。
var RawStubs = []Stub{
	// HMCP-R1：只接受 PAT，所有方法都交給 MCP SDK（S6）。
	{ID: "mcp", Method: "", Path: "/mcp", Access: AccessAuthenticated, Spec: "HMCP-R1"},
}

// AccessExtension 是 OpenAPI operation 上記錄 Access 的擴充欄位，讓 Hub 的用戶端與 contract test 看得到權限。
const AccessExtension = "x-notesteme-access"

func notImplemented(context.Context, *struct{}) (*struct{}, error) {
	return nil, apierr.NotImplemented()
}

func registerStubs(api huma.API) {
	for _, s := range Stubs {
		huma.Register(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
			Description: "規則：" + s.Spec + "。",
			Extensions:  map[string]any{AccessExtension: string(s.Access)},
			Errors:      []int{http.StatusNotImplemented},
		}, notImplemented)
	}
}

func registerRawStubs(mux *http.ServeMux) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { apierr.Write(w, apierr.NotImplemented()) })
	for _, s := range RawStubs {
		pattern := s.Path
		if s.Method != "" {
			pattern = s.Method + " " + s.Path
		}
		mux.Handle(pattern, h)
	}
}
