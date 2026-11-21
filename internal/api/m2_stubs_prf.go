package api

import (
	"net/http"

	"github.com/cxyfer/notesteme/internal/auth"
)

// prfStubs 是 PRF（處理方案與頻道覆寫）的 501 stub，共 9 條（M2 S1 實作時整個檔案刪除，改在 profiles.go、channels.go 註冊）。
// 註冊與 m2Stubs 相同，見 registerM2Stubs。
var prfStubs = []m2Stub{
	{"profiles-list", http.MethodGet, "/api/profiles", "列出處理方案（尚未實作）", auth.ScopeRead},
	{"profiles-create", http.MethodPost, "/api/profiles", "建立處理方案（尚未實作）", auth.ScopeAdmin},
	{"profiles-get", http.MethodGet, "/api/profiles/{id}", "讀取處理方案（尚未實作）", auth.ScopeRead},
	{"profiles-patch", http.MethodPatch, "/api/profiles/{id}", "修改處理方案（尚未實作）", auth.ScopeAdmin},
	{"profiles-delete", http.MethodDelete, "/api/profiles/{id}", "刪除處理方案（尚未實作）", auth.ScopeAdmin},
	{"profiles-versions", http.MethodGet, "/api/profiles/{id}/versions", "處理方案的版本（尚未實作）", auth.ScopeRead},
	{"profiles-default", http.MethodPost, "/api/profiles/{id}/default", "設為預設處理方案（尚未實作）", auth.ScopeAdmin},
	{"channels-overrides-get", http.MethodGet, "/api/channels/{id}/overrides", "讀取頻道覆寫（尚未實作）", auth.ScopeRead},
	{"channels-overrides-patch", http.MethodPatch, "/api/channels/{id}/overrides", "修改頻道覆寫（尚未實作）", auth.ScopeAdmin},
}
