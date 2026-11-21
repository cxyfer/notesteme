package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// m2Stub 是 M2 之後的切片才會實作的路由；實作前先明確回應 501 not_implemented。
// scope 在讀取請求本體之前檢查（registerScoped），所以沒有憑證回 401、缺 scope 回 403。
type m2Stub struct {
	ID, Method, Path, Summary string
	Scope                     string
}

// m2Stubs 是 M2 的 501 stub 清單（M2 S0 契約第 4 節）。
// scope 的原則：讀取設定物件用 read；寫入用 admin；會花錢或觸發重新處理的用 ingest。
// 切片實作某條路由時，從這份清單移除，改在自己的檔案註冊。
// PRF（處理方案、頻道覆寫）的 stub 放在 m2_stubs_prf.go；PRM（prompt 庫）已經實作，路由在 prompts.go。
var m2Stubs = []m2Stub{
	// OUT：字幕輸出與 ASS 樣式
	{"subtitles-import", http.MethodPost, "/api/items/{id}/subtitles/import", "匯入字幕（尚未實作）", auth.ScopeIngest},
	{"ass-styles-list", http.MethodGet, "/api/ass-styles", "列出 ASS 樣式（尚未實作）", auth.ScopeRead},
	{"ass-styles-create", http.MethodPost, "/api/ass-styles", "建立 ASS 樣式（尚未實作）", auth.ScopeAdmin},
	{"ass-styles-get", http.MethodGet, "/api/ass-styles/{id}", "讀取 ASS 樣式（尚未實作）", auth.ScopeRead},
	{"ass-styles-patch", http.MethodPatch, "/api/ass-styles/{id}", "修改 ASS 樣式（尚未實作）", auth.ScopeAdmin},
	{"ass-styles-preview", http.MethodPost, "/api/ass-styles/preview", "預覽 ASS 樣式（尚未實作）", auth.ScopeRead},
	{"ass-styles-import", http.MethodPost, "/api/ass-styles/import", "匯入 ASS 樣式（尚未實作）", auth.ScopeAdmin},
	// PRM 的 playground 與 compare 要花錢，屬於 LLM 執行切片（S3），留在這裡；PRM 的其他路由見 prompts.go。
	{"prompts-playground", http.MethodPost, "/api/prompts/playground", "提示詞試跑，會花錢（尚未實作）", auth.ScopeIngest},
	{"prompts-compare", http.MethodPost, "/api/prompts/{id}/compare", "提示詞比較，會花錢（尚未實作）", auth.ScopeIngest},
	// COR：詞彙表與詞彙候選
	{"glossaries-get", http.MethodGet, "/api/glossaries/{scope}/{id}", "讀取詞彙表（尚未實作）", auth.ScopeRead},
	{"glossaries-put", http.MethodPut, "/api/glossaries/{scope}/{id}", "取代詞彙表（尚未實作）", auth.ScopeAdmin},
	{"channels-term-candidates", http.MethodGet, "/api/channels/{id}/term-candidates", "詞彙候選（尚未實作）", auth.ScopeRead},
	// AGT：切換執行者
	{"items-switch-executor", http.MethodPost, "/api/items/{id}/switch-executor", "切換影音步驟的執行者（尚未實作）", auth.ScopeIngest},
	{"tasks-switch-executor", http.MethodPost, "/api/tasks/{id}/switch-executor", "切換任務的執行者（尚未實作）", auth.ScopeIngest},
}

// registerM2Stubs 註冊 M2 的 501 stub。
func (rt *routes) registerM2Stubs(api huma.API) {
	for _, s := range m2Stubs {
		registerScoped(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
		}, s.Scope, notImplemented)
	}
}
