package api

import (
	"net/http"

	"github.com/cxyfer/notesteme/internal/auth"
)

// prmStubs 是 PRM（prompt 庫）的 501 stub，共 6 條，不含 playground 與 compare（M2 S2 實作時整個檔案刪除，改在 prompts.go 註冊）。
// 註冊與 m2Stubs 相同，見 registerM2Stubs。
var prmStubs = []m2Stub{
	{"prompts-list", http.MethodGet, "/api/prompts", "列出提示詞（尚未實作）", auth.ScopeRead},
	{"prompts-create", http.MethodPost, "/api/prompts", "建立提示詞（尚未實作）", auth.ScopeAdmin},
	{"prompts-delete", http.MethodDelete, "/api/prompts/{id}", "刪除提示詞（尚未實作）", auth.ScopeAdmin},
	{"prompts-versions-list", http.MethodGet, "/api/prompts/{id}/versions", "提示詞的版本（尚未實作）", auth.ScopeRead},
	{"prompts-versions-create", http.MethodPost, "/api/prompts/{id}/versions", "新增提示詞版本（尚未實作）", auth.ScopeAdmin},
	{"prompts-default", http.MethodPost, "/api/prompts/{id}/default", "設為預設提示詞（尚未實作）", auth.ScopeAdmin},
}
