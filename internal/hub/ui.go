package hub

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/cxyfer/notesteme/internal/webui"
)

// UICSP 是 Hub UI 的 Content-Security-Policy（M5-S9）：比 Client 更嚴，Hub 不嵌入播放器、不載入外部縮圖，
// 使用者上傳的內容（標題、字幕、筆記中的連結與圖片）只當作文字顯示（HREP-R12、R18）。
const UICSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self'; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"media-src 'none'; " +
	"frame-src 'none'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// UIPlaceholder 是沒有建置 web/hub 時的頁面；notesteme-hub-ui-placeholder 讓 release 檢查確認發布的 binary 不是它。
const UIPlaceholder = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="notesteme-hub-ui-placeholder">
<title>Notesteme Hub</title>
</head>
<body>
<main>
<h1>Hub 網頁尚未建置</h1>
<p>這個 binary 沒有包含 Hub 的網頁。在 web/hub 執行 <code>npm ci &amp;&amp; npm run build</code> 後重新編譯；API 與 MCP 不受影響。</p>
<p lang="en">The Hub web UI was not built into this binary. Run <code>npm ci &amp;&amp; npm run build</code> in web/hub and rebuild.</p>
</main>
</body>
</html>
`

// NewUI 建立 Hub UI 的 handler（fsys 是 web/hub 的 dist）。
func NewUI(fsys fs.FS) *webui.Handler {
	return webui.NewWithOptions(fsys, webui.Options{CSP: UICSP, Placeholder: UIPlaceholder})
}

// withUI 把非後端路徑交給 Hub UI；後端路徑（/api、/auth、/mcp 與其下，/healthz、/readyz）照常進入 API。
// 與 Client 相同，不在 mux 上註冊 "/" catch-all，405 與未知的 /api/* 維持後端的語意。
func withUI(ui, backend http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isBackendPath(r.URL.Path) {
			backend.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

func isBackendPath(p string) bool {
	for _, prefix := range []string{"/api", "/auth", "/mcp"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return p == "/healthz" || p == "/readyz"
}
