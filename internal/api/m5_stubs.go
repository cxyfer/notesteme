package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m5Stubs 是 HSHR 規格「介面」章節列出、還沒有實作的 Client 路由（M5-S0，比照 m3Stubs）：
// 先回 501 not_implemented，scope 在讀取請求本體之前檢查（registerScoped）。
// 切片實作某條路由時，從這份清單移除，改在自己的檔案註冊（m5_stubs_test.go 的清單一併更新）。
// GET /api/items/{id} 的 hub_shares 欄位由 S7 加入。
var m5Stubs = []m2Stub{
	// HSHR-R1、R12（S7、S8）
	{ID: "settings-hub-get", Method: http.MethodGet, Path: "/api/settings/hub",
		Summary: "Hub 的連線設定：url、token_set 與 ASR 前查詢（不含 token，尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "settings-hub-put", Method: http.MethodPut, Path: "/api/settings/hub",
		Summary: "設定 Hub token 與 ASR 前查詢（部分更新，尚未實作）", Scope: auth.ScopeAdmin},
	// HSHR-R2～R6（S7）
	{ID: "items-hub-share", Method: http.MethodPost, Path: "/api/items/{id}/hub/share",
		Summary: "把影音的產出分享到 Hub（尚未實作）", Scope: auth.ScopeAdmin},
	// HSHR-R9～R11（S8）
	{ID: "hub-import", Method: http.MethodPost, Path: "/api/hub/import",
		Summary: "以 Hub 上的原始字幕重新生成影音（尚未實作）", Scope: auth.ScopeIngest},
}

// registerM5Stubs 註冊 M5 的 501 stub。
func (rt *routes) registerM5Stubs(api huma.API) {
	for _, s := range m5Stubs {
		registerScoped(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
		}, s.Scope, notImplemented)
	}
}
