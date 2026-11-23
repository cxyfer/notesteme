package api

import (
	"net/http"
	"strings"

	"github.com/cxyfer/notesteme/internal/entry"
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

// entryPages 在 Web UI 之前掛上 ENTRY 的頁面路由（/add、/share、/login、manifest、service worker）。
// UI handler 沒有 IndexHTML（測試替身）或沒有驗證服務時，照舊只交給 UI。
func entryPages(d Deps) http.Handler {
	ix, ok := d.UI.(interface{ IndexHTML() []byte })
	if !ok || d.Auth == nil {
		return d.UI
	}
	if d.InsecureNoAuth {
		// 無驗證模式：/add、/share 一律視為已登入，不會導回登入頁（AUTH-R3）。
		return entry.NewPages(insecureSessions{}, ix.IndexHTML, d.UI, d.Log)
	}
	return entry.NewPages(d.Auth, ix.IndexHTML, d.UI, d.Log)
}
