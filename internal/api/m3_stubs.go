package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m3Stubs 是 M3 規格「介面」章節列出、還沒有實作的路由（M3-S0，比照 M2 S0 的 m2Stubs）：
// 先回 501 not_implemented，scope 在讀取請求本體之前檢查（registerScoped）。
// 切片實作某條路由時，從這份清單移除，改在自己的檔案註冊（m3_stubs_test.go 的清單一併更新）。
var m3Stubs = []m2Stub{
	// NOTE-R13（S4 NOTE）
	{ID: "items-note", Method: http.MethodGet, Path: "/api/items/{id}/note",
		Summary: "取得影音的筆記（Markdown、frontmatter、自動檢查與寫入狀態，尚未實作）", Scope: auth.ScopeRead},
	// NOTE-R11、R12（S4 NOTE）
	{ID: "note-templates-list", Method: http.MethodGet, Path: "/api/note-templates",
		Summary: "列出筆記模板（尚未實作）", Scope: auth.ScopeRead},
	{ID: "note-templates-create", Method: http.MethodPost, Path: "/api/note-templates",
		Summary: "建立筆記模板，或複製內建模板（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "note-templates-update", Method: http.MethodPatch, Path: "/api/note-templates/{id}",
		Summary: "修改筆記模板，建立新版本（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "note-templates-preview", Method: http.MethodPost, Path: "/api/note-templates/{id}/preview",
		Summary: "用影音現行筆記的段落預覽模板，不呼叫 LLM、不寫入檔案（尚未實作）", Scope: auth.ScopeAdmin},
	// VAULT-R1（S5 VAULT）
	{ID: "vaults-list", Method: http.MethodGet, Path: "/api/vaults",
		Summary: "列出設定檔定義的 vault（id、adapter、base，尚未實作）", Scope: auth.ScopeAdmin},
}

// registerM3Stubs 註冊 M3 的 501 stub。
func (rt *routes) registerM3Stubs(api huma.API) {
	for _, s := range m3Stubs {
		registerScoped(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
		}, s.Scope, notImplemented)
	}
}
