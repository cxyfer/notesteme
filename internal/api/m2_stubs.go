package api

import (
	"github.com/danielgtaylor/huma/v2"
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
// PRF（處理方案、頻道覆寫）的 stub 放在 m2_stubs_prf.go；PRM（prompt 庫）已經實作，路由在 prompts.go，試跑與比較在 prompts_playground.go；
// OUT 的 ASS 樣式與字幕匯入已經實作，路由在 ass_styles.go 與 subtitle_import.go；COR 的詞彙表與術語候選已經實作，路由在 glossaries.go。
// AGT 的 switch-executor 已經實作，路由在 agent.go；目前沒有剩下的 stub。
var m2Stubs = []m2Stub{}

// registerM2Stubs 註冊 M2 的 501 stub。
func (rt *routes) registerM2Stubs(api huma.API) {
	for _, s := range m2Stubs {
		registerScoped(api, huma.Operation{
			OperationID: s.ID, Method: s.Method, Path: s.Path, Summary: s.Summary,
		}, s.Scope, notImplemented)
	}
}
