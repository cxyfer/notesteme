package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m4Stubs 是 M4 規格「介面」章節列出、還沒有實作的路由（M4-S0，比照 M2 S0、M3-S0）：
// 先回 501 not_implemented，scope 在讀取請求本體之前檢查（registerScoped）。
// 切片實作某條路由時，從這份清單移除，改在自己的檔案註冊（m4_stubs_test.go 的清單一併更新）。
//
// 不在這份清單的 M4 介面：
//   - GET/PUT /api/settings/budget：沿用既有的 settings-get／settings-put（M4-S4 COST 已實作 budget 區段）；
//     不另外註冊字面路徑，以免蓋過 {section}。COST 的其他路由在 costs.go。
//   - AUTH 的路由（sessions、tokens rotate、OIDC 的 start／callback、auth/methods）在 AUTH 切片實作（auth_sessions.go、oidc.go）。
//   - ENTRY 的頁面路由（/add、/share、/login、/manifest.webmanifest、/sw.js）不在 /api 底下，已由 internal/entry 實作（掛在 ui.go 的 entryPages）；
//     POST /api/auth/login 的 next（ENTRY-R6）也已實作。
//   - DIG 的路由（continue、digests）在 M4-S6 實作（digests.go）。
//   - GET /api/search 在 S8 SRCH 實作（search.go）。
//   - GET /api/items/:id 的 reprobe、better_source 在 SRC-10 實作（items.go）。
//   - BND 的路由（GET /api/items/{id}/bundle、POST /api/bundles/import）已實作（bundles.go）。
//   - GLOS 的路由（GET /api/term-candidates、POST /api/term-candidates/decide）已實作（term_review.go）；
//     PATCH /api/channels/:id/term-candidates/:candidate 的 term、scope 也已加上（glossaries.go）。
//   - 既有路由的欄位擴充（GET /api/items/:id 的 read_state，
//     GET /api/system 的 alerts、backup）由各切片加上。
var m4Stubs = []m2Stub{
	// LIB-R16～R21（S13 LIB）。批次的 scope 依動作（LIB-R16），stub 不讀 body，先要求多數動作需要的 ingest；
	// 實作時改成在讀取影音之前依動作判斷（delete 要 admin、export_bundle 同 BND 的匯出）。
	{ID: "items-batch", Method: http.MethodPost, Path: "/api/items/batch",
		Summary: "對多部影音執行同一個動作（重跑、換處理方案、標記已讀、刪除、匯出 bundle，尚未實作）", Scope: auth.ScopeIngest},
	{ID: "overview-get", Method: http.MethodGet, Path: "/api/overview",
		Summary: "總覽：處理中、等你決定、失敗、本月花費、磁碟用量與今天的動態（尚未實作）", Scope: auth.ScopeRead},
}

// registerM4Stubs 註冊 M4 的 501 stub。
func (rt *routes) registerM4Stubs(api huma.API) {
	for _, s := range m4Stubs {
		registerScoped(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
		}, s.Scope, notImplemented)
	}
}
