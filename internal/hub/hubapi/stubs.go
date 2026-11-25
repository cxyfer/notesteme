package hubapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
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
	// HACC、HREP 的路由都已實作：登入、登出、/me 與登入方式（M5-S1，auth.go）；Hub token、群組指派與稽核紀錄（M5-S2，tokens.go）；
	// 分享、撤回與版本詳情（M5-S3，repository.go）；篩選、影片頁、取用與 lookup（M5-S4，listing.go、download.go）；
	// 投票與檢舉（M5-S5，community.go）、下架申請（takedowns.go）、管理與我的分享（moderation.go）。
}

// RawStubs 是不經過 Huma、還沒有實作的路由（目前沒有）。瀏覽器的 OAuth 流程（/auth/<provider>/start|callback）在 auth.go，
// Hub MCP 的 /mcp（HMCP，只接受 Hub token）在 mcp.go。
var RawStubs = []Stub{}

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
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { apierr.WriteFor(w, r, apierr.NotImplemented()) })
	for _, s := range RawStubs {
		pattern := s.Path
		if s.Method != "" {
			pattern = s.Method + " " + s.Path
		}
		mux.Handle(pattern, h)
	}
}
