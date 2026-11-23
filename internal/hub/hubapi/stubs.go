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
// 這張表就是 Hub 的權限矩陣（HACC-R13）：S2 的 middleware 依 Access 在讀取 body 與查詢資源之前檢查，
// 所以實作某條路由時，從這裡移到自己的檔案，Access 一併帶走（stubs_test.go 的清單一併更新）。
type Stub struct {
	ID, Method, Path, Summary string
	Access                    Access
	// Spec 是定義這條路由的規則（說明用）。
	Spec string
}

// Stubs 是 HACC、HREP 規格「介面」章節的 /api/v1 路由（M5-S0）。
var Stubs = []Stub{
	// HACC：登入、帳號、PAT（S1、S2）
	{ID: "auth-providers", Method: http.MethodGet, Path: "/api/v1/auth/providers", Access: AccessPublic, Spec: "HACC-R1",
		Summary: "列出已啟用的登入方式（尚未實作）"},
	{ID: "auth-logout", Method: http.MethodPost, Path: "/api/v1/auth/logout", Access: AccessSession, Spec: "HACC-R8",
		Summary: "登出，session 立即失效（尚未實作）"},
	{ID: "me", Method: http.MethodGet, Path: "/api/v1/me", Access: AccessAuthenticated, Spec: "HACC-R6、ADR-0032",
		Summary: "目前的帳號、登入方式、群組與有效權限（尚未實作）"},
	{ID: "me-identities-link", Method: http.MethodPost, Path: "/api/v1/me/identities/{provider}/link", Access: AccessSession, Spec: "HACC-R6",
		Summary: "連結新的登入方式（尚未實作）"},
	{ID: "me-identities-unlink", Method: http.MethodDelete, Path: "/api/v1/me/identities/{provider}", Access: AccessSession, Spec: "HACC-R7",
		Summary: "移除登入方式（尚未實作）"},
	{ID: "me-tokens-list", Method: http.MethodGet, Path: "/api/v1/me/tokens", Access: AccessSession, Spec: "HACC-R9",
		Summary: "列出 Hub token（尚未實作）"},
	{ID: "me-tokens-create", Method: http.MethodPost, Path: "/api/v1/me/tokens", Access: AccessSession, Spec: "HACC-R9",
		Summary: "建立 Hub token，token 只顯示這一次（尚未實作）"},
	{ID: "me-tokens-revoke", Method: http.MethodDelete, Path: "/api/v1/me/tokens/{id}", Access: AccessSession, Spec: "HACC-R9",
		Summary: "撤銷 Hub token（尚未實作）"},
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
	{ID: "admin-users-group-add", Method: http.MethodPut, Path: "/api/v1/admin/users/{id}/groups/{group}", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R12",
		Summary: "把使用者加入群組（尚未實作）"},
	{ID: "admin-users-group-remove", Method: http.MethodDelete, Path: "/api/v1/admin/users/{id}/groups/{group}", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R12",
		Summary: "把使用者移出群組（尚未實作）"},
	{ID: "admin-users-suspend", Method: http.MethodPost, Path: "/api/v1/admin/users/{id}/suspend", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R17",
		Summary: "停用帳號（尚未實作）"},
	{ID: "admin-users-unsuspend", Method: http.MethodPost, Path: "/api/v1/admin/users/{id}/unsuspend", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R17",
		Summary: "恢復帳號（尚未實作）"},
	{ID: "admin-audit", Method: http.MethodGet, Path: "/api/v1/admin/audit", Access: perm(hubconfig.PermAdmin), Spec: "HACC-R17",
		Summary: "稽核紀錄（尚未實作）"},
	// HREP：分享、篩選、取用、社群（S3～S5）
	{ID: "shares-create", Method: http.MethodPost, Path: "/api/v1/shares", Access: perm(hubconfig.PermUpload), Spec: "HREP-R1",
		Summary: "分享 bundle（multipart：bundle、attest、visibility，尚未實作）"},
	{ID: "shares-withdraw", Method: http.MethodDelete, Path: "/api/v1/shares/{id}", Access: perm(hubconfig.PermUpload), Spec: "HREP-R10",
		Summary: "撤回自己的分享（尚未實作）"},
	{ID: "versions-list", Method: http.MethodGet, Path: "/api/v1/versions", Access: perm(hubconfig.PermRead), Spec: "HREP-R16",
		Summary: "篩選版本（尚未實作）"},
	{ID: "versions-get", Method: http.MethodGet, Path: "/api/v1/versions/{id}", Access: perm(hubconfig.PermRead), Spec: "HREP-R17",
		Summary: "版本詳情、來源紀錄與衍生關係（尚未實作）"},
	{ID: "videos-get", Method: http.MethodGet, Path: "/api/v1/videos/{platform}/{external_id}", Access: perm(hubconfig.PermRead), Spec: "HREP-R17",
		Summary: "影片的所有版本，依種類、變體與語言分組（尚未實作）"},
	{ID: "versions-download", Method: http.MethodGet, Path: "/api/v1/versions/{id}/download", Access: perm(hubconfig.PermDownload), Spec: "HREP-R18",
		Summary: "下載渲染後的字幕、筆記或章節（尚未實作）"},
	{ID: "versions-content", Method: http.MethodGet, Path: "/api/v1/versions/{id}/content", Access: perm(hubconfig.PermRegenerate), Spec: "HREP-R18",
		Summary: "原始的 cues JSON（Client 重新生成用，尚未實作）"},
	{ID: "lookup", Method: http.MethodGet, Path: "/api/v1/lookup", Access: perm(hubconfig.PermRegenerate), Spec: "HREP-R19",
		Summary: "ASR 前查詢：以影片 ID 雜湊的前綴查詢（尚未實作）"},
	{ID: "versions-vote-put", Method: http.MethodPut, Path: "/api/v1/versions/{id}/vote", Access: perm(hubconfig.PermRead), Spec: "HREP-R13",
		Summary: "投票（尚未實作）"},
	{ID: "versions-vote-delete", Method: http.MethodDelete, Path: "/api/v1/versions/{id}/vote", Access: perm(hubconfig.PermRead), Spec: "HREP-R13",
		Summary: "收回投票（尚未實作）"},
	{ID: "versions-reports-create", Method: http.MethodPost, Path: "/api/v1/versions/{id}/reports", Access: perm(hubconfig.PermRead), Spec: "HREP-R14",
		Summary: "檢舉版本（尚未實作）"},
}

// RawStubs 是不經過 Huma 的路由（瀏覽器的 OAuth 流程與 MCP 的 JSON-RPC），同樣先回 501。
var RawStubs = []Stub{
	{ID: "oauth-start", Method: http.MethodGet, Path: "/auth/{provider}/start", Access: AccessPublic, Spec: "HACC-R2"},
	{ID: "oauth-callback", Method: http.MethodGet, Path: "/auth/{provider}/callback", Access: AccessPublic, Spec: "HACC-R3"},
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
