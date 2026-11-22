package api

import (
	"net/http"
	"strings"
)

// uiDispatch 把非後端路徑交給 Web UI（ADR-0008）；後端路徑照常進入 mux。
//
// 不在 mux 上註冊 "/" catch-all：那樣「路徑對、方法錯」的請求與未知的 /api/* 都會落到 UI，
// 405 與 404 的語意會改變。後端路徑的判斷用原始路徑；UI 端自己正規化並轉址。
func uiDispatch(ui, backend http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBackendPath(r.URL.Path) {
			backend.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

// isBackendPath 回報路徑是否屬於後端：/api、/mcp 與其下的路徑，以及 /healthz、/readyz。
func isBackendPath(p string) bool {
	for _, prefix := range []string{"/api", "/mcp"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return p == "/healthz" || p == "/readyz"
}
