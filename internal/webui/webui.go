// Package webui 提供 Web UI 的靜態檔案：嵌入的 Vite 建置產物、SPA fallback 與安全標頭（ADR-0008、NFR-SEC-1）。
//
// 路由規則（見 web/client/README.md）：
//   - /assets/* 是有 hash 的檔案：長期快取；找不到回 404，不回 index.html。
//   - 最後一段有副檔名的路徑（例如 /favicon.ico）找不到也回 404。
//   - 其餘路徑都是前端路由：回 index.html（no-cache），由 TanStack Router 處理。
//   - 只接受 GET 與 HEAD；路徑不是正規形式時 301 到正規形式。
//
// 是否交給後端（/api、/mcp、/healthz、/readyz）由 internal/api 判斷，這裡不處理。
package webui

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// CSP 是所有 UI 回應的 Content-Security-Policy（NFR-SEC-1）。
//
//   - script-src 只有 'self'：不允許 inline script 與 eval，前端不能有 inline <script>。
//   - style-src 允許 'unsafe-inline'（只限 style）：Radix 的 react-remove-scroll 等套件會插入 <style> 元素。
//     見 docs/ui/README.md「實作現況」的待決問題。
//   - frame-src 只開放 YouTube 與 Bilibili 的嵌入播放器；img-src 開放兩者的縮圖網域。
const CSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' https://*.ytimg.com https://*.hdslb.com; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"media-src 'self'; " +
	"frame-src https://www.youtube.com https://www.youtube-nocookie.com https://player.bilibili.com; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// PermissionsPolicy 關閉 UI 用不到的瀏覽器功能。
const PermissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNoCache   = "no-cache"
)

// SetSecurityHeaders 設定 UI 回應的安全標頭（NFR-SEC-1）。
func SetSecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", CSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Permissions-Policy", PermissionsPolicy)
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
}

// Placeholder 是還沒建置前端時回應的頁面：只有 Go、沒有 Node 的建置也能正常執行與測試。
// 字串 notesteme-ui-placeholder 讓 release 檢查可以確認發布的 binary 不是這個頁面。
const Placeholder = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="notesteme-ui-placeholder">
<title>Notesteme</title>
</head>
<body>
<main>
<h1>Web UI 尚未建置</h1>
<p>這個 binary 沒有包含前端。在 web/client 執行 <code>npm ci &amp;&amp; npm run build</code> 後重新編譯；API、CLI 與 MCP 不受影響。</p>
<p lang="en">The web UI was not built into this binary. Run <code>npm ci &amp;&amp; npm run build</code> in web/client and rebuild.</p>
</main>
</body>
</html>
`

// Handler 是 UI 的 http.Handler。
type Handler struct {
	fsys  fs.FS
	index []byte // nil 代表沒有建置產物，回應 Placeholder
}

// New 建立 handler；fsys 的根目錄就是 Vite 的 dist（含 index.html 與 assets/）。
func New(fsys fs.FS) *Handler {
	h := &Handler{fsys: fsys}
	if b, err := fs.ReadFile(fsys, "index.html"); err == nil {
		h.index = b
	}
	return h
}

// Built 回報是否有真正的建置產物（不是 Placeholder）。
func (h *Handler) Built() bool { return h.index != nil }

// IndexHTML 回傳 fallback 時回應的 HTML（建置產物或 Placeholder）。
func (h *Handler) IndexHTML() []byte {
	if h.index != nil {
		return h.index
	}
	return []byte(Placeholder)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	SetSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := r.URL.Path
	// 反斜線與 NUL 不會出現在合法的前端路徑或檔名中。
	if strings.ContainsAny(p, "\\\x00") {
		http.NotFound(w, r)
		return
	}
	clean := path.Clean("/" + p)
	if clean != p {
		// 不是正規形式（..、重複斜線、結尾斜線）：轉到正規形式，讀檔只用正規路徑。
		target := clean
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	name := strings.TrimPrefix(clean, "/")
	if name == "" || name == "index.html" {
		h.serveIndex(w, r)
		return
	}
	if h.serveFile(w, r, name) {
		return
	}
	// 有 hash 的資源與看起來像檔案的路徑找不到時回 404，不能回 index.html（否則瀏覽器會把 HTML 當 JS 解析）。
	if strings.HasPrefix(clean, "/assets/") || path.Ext(clean) != "" {
		w.Header().Set("Cache-Control", cacheNoCache)
		http.NotFound(w, r)
		return
	}
	h.serveIndex(w, r)
}

// serveFile 回應 fsys 中的一般檔案；不存在或是目錄時回傳 false。
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	f, err := h.fsys.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		rs = bytes.NewReader(b)
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", cacheImmutable)
	} else {
		w.Header().Set("Cache-Control", cacheNoCache)
	}
	http.ServeContent(w, r, name, time.Time{}, rs)
	return true
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", cacheNoCache)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.IndexHTML()))
}
