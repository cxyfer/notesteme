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
//   - GET/PUT /api/settings/budget：沿用既有的 settings-get／settings-put（budget 區段目前回 501，由 COST 切片實作）；
//     不另外註冊字面路徑，以免蓋過 {section}。
//   - GET /api/auth/oidc/start、callback：AUTH-R16 規定 OIDC 沒有啟用時回 404，S0 不啟用 OIDC，所以不註冊
//     （沒有身分的請求在 S0 先被驗證擋下，回 401）。
//   - ENTRY 的頁面路由（/add、/share、/manifest.webmanifest、/sw.js）不在 /api 底下，由 ENTRY 切片在 UI 的 handler 加上。
//   - 既有路由的欄位擴充（GET /api/items/:id 的 summary、reprobe、better_source、read_state，POST /api/auth/login 的 next，
//     PATCH /api/channels/:id/term-candidates/:candidate 的 term、scope，GET /api/system 的 alerts、backup）由各切片加上。
var m4Stubs = []m2Stub{
	// SUB-R13（S2、S3 SUB）
	{ID: "subscriptions-list", Method: http.MethodGet, Path: "/api/subscriptions",
		Summary: "列出訂閱（尚未實作）", Scope: auth.ScopeRead},
	{ID: "subscriptions-create", Method: http.MethodPost, Path: "/api/subscriptions",
		Summary: "建立訂閱（頻道、播放清單、Bilibili 合集或 Podcast feed，尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "subscriptions-get", Method: http.MethodGet, Path: "/api/subscriptions/{id}",
		Summary: "取得一個訂閱（尚未實作）", Scope: auth.ScopeRead},
	{ID: "subscriptions-patch", Method: http.MethodPatch, Path: "/api/subscriptions/{id}",
		Summary: "修改訂閱（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "subscriptions-delete", Method: http.MethodDelete, Path: "/api/subscriptions/{id}",
		Summary: "刪除訂閱（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "subscriptions-entries", Method: http.MethodGet, Path: "/api/subscriptions/{id}/entries",
		Summary: "列出訂閱探索到的條目與略過原因（尚未實作）", Scope: auth.ScopeRead},
	{ID: "subscriptions-preview", Method: http.MethodPost, Path: "/api/subscriptions/{id}/preview",
		Summary: "試跑既有訂閱的篩選與回補，不建立影音（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "subscriptions-preview-new", Method: http.MethodPost, Path: "/api/subscriptions/preview",
		Summary: "建立訂閱之前試跑篩選與回補，不建立影音（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "subscriptions-check", Method: http.MethodPost, Path: "/api/subscriptions/{id}/check",
		Summary: "立即檢查訂閱的新內容（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "subscriptions-import", Method: http.MethodPost, Path: "/api/subscriptions/import",
		Summary: "從 OPML 匯入訂閱（尚未實作）", Scope: auth.ScopeAdmin},
	// DIG-R7、R8、R11（S6 DIG）
	{ID: "items-continue", Method: http.MethodPost, Path: "/api/items/{id}/continue",
		Summary: "繼續處理摘要後暫停的影音（尚未實作）", Scope: auth.ScopeIngest},
	{ID: "items-continue-batch", Method: http.MethodPost, Path: "/api/items/continue",
		Summary: "一次繼續處理多部摘要後暫停的影音（尚未實作）", Scope: auth.ScopeIngest},
	{ID: "digests-list", Method: http.MethodGet, Path: "/api/digests",
		Summary: "列出彙整設定（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "digests-create", Method: http.MethodPost, Path: "/api/digests",
		Summary: "建立彙整設定（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "digests-patch", Method: http.MethodPatch, Path: "/api/digests/{id}",
		Summary: "修改彙整設定（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "digests-delete", Method: http.MethodDelete, Path: "/api/digests/{id}",
		Summary: "刪除彙整設定（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "digests-preview", Method: http.MethodPost, Path: "/api/digests/{id}/preview",
		Summary: "預覽彙整的內容，不送出、不改變下次執行時間（尚未實作）", Scope: auth.ScopeAdmin},
	// COST 的介面（S4 COST）
	{ID: "prices-list", Method: http.MethodGet, Path: "/api/prices",
		Summary: "列出計價表（內建與覆寫，尚未實作）", Scope: auth.ScopeRead},
	{ID: "prices-put", Method: http.MethodPut, Path: "/api/prices",
		Summary: "設定單價覆寫（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "prices-delete", Method: http.MethodDelete, Path: "/api/prices",
		Summary: "刪除單價覆寫（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "prices-confirm", Method: http.MethodPost, Path: "/api/prices/confirm",
		Summary: "確認內建計價表仍然正確（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "costs-summary", Method: http.MethodGet, Path: "/api/costs",
		Summary: "依日期、provider、模型、步驟、頻道或影音彙總花費（尚未實作）", Scope: auth.ScopeRead},
	{ID: "costs-entries", Method: http.MethodGet, Path: "/api/costs/entries",
		Summary: "列出成本帳本的紀錄（尚未實作）", Scope: auth.ScopeRead},
	{ID: "costs-export", Method: http.MethodGet, Path: "/api/costs/export.csv",
		Summary: "匯出成本帳本為 CSV（尚未實作）", Scope: auth.ScopeRead},
	// LIB-R16～R21（S13 LIB）。批次的 scope 依動作（LIB-R16），stub 不讀 body，先要求多數動作需要的 ingest；
	// 實作時改成在讀取影音之前依動作判斷（delete 要 admin、export_bundle 同 BND 的匯出）。
	{ID: "items-batch", Method: http.MethodPost, Path: "/api/items/batch",
		Summary: "對多部影音執行同一個動作（重跑、換處理方案、標記已讀、刪除、匯出 bundle，尚未實作）", Scope: auth.ScopeIngest},
	{ID: "overview-get", Method: http.MethodGet, Path: "/api/overview",
		Summary: "總覽：處理中、等你決定、失敗、本月花費、磁碟用量與今天的動態（尚未實作）", Scope: auth.ScopeRead},
	// BND 的介面（S10 BND）
	{ID: "items-bundle", Method: http.MethodGet, Path: "/api/items/{id}/bundle",
		Summary: "匯出影音的 bundle（.notesteme.zip，尚未實作）", Scope: auth.ScopeRead},
	{ID: "bundles-import", Method: http.MethodPost, Path: "/api/bundles/import",
		Summary: "匯入 bundle（multipart 的 file，尚未實作）", Scope: auth.ScopeAdmin},
	// GLOS-R4、R5（S9 GLOS）
	{ID: "term-candidates-decide", Method: http.MethodPost, Path: "/api/term-candidates/decide",
		Summary: "一次決定多個術語候選（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "term-candidates-list", Method: http.MethodGet, Path: "/api/term-candidates",
		Summary: "列出所有頻道待確認的術語候選（尚未實作）", Scope: auth.ScopeRead},
	// SRCH-R5、R6、R10（S8 SRCH）
	{ID: "search", Method: http.MethodGet, Path: "/api/search",
		Summary: "字幕全文搜尋（尚未實作）", Scope: auth.ScopeRead},
	// AUTH-R20、R21（S11 AUTH）
	{ID: "auth-sessions-list", Method: http.MethodGet, Path: "/api/auth/sessions",
		Summary: "列出登入的工作階段（尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "auth-sessions-revoke", Method: http.MethodDelete, Path: "/api/auth/sessions/{id}",
		Summary: "撤銷一個工作階段（id 為 current 時等同登出，尚未實作）", Scope: auth.ScopeAdmin},
	{ID: "tokens-rotate", Method: http.MethodPost, Path: "/api/tokens/{id}/rotate",
		Summary: "輪替 token 的 secret，名稱、scope 與到期時間不變（尚未實作）", Scope: auth.ScopeAdmin},
}

// registerM4Stubs 註冊 M4 的 501 stub。
func (rt *routes) registerM4Stubs(api huma.API) {
	for _, s := range m4Stubs {
		registerScoped(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
		}, s.Scope, notImplemented)
	}
}
