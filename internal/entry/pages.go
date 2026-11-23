package entry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/webui"
)

// Sessions 驗證 session cookie（auth.Service 實作）。
type Sessions interface {
	AuthenticateSession(ctx context.Context, sessionToken string) (auth.Principal, error)
}

// Pages 是 ENTRY 的頁面路由；其他路徑交給 next（Web UI 的靜態檔案）。
type Pages struct {
	sessions Sessions
	index    func() []byte
	next     http.Handler
	log      *slog.Logger
}

// NewPages 建立頁面路由。index 回傳 SPA 的 index.html（webui.Handler.IndexHTML）。
func NewPages(sessions Sessions, index func() []byte, next http.Handler, log *slog.Logger) *Pages {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Pages{sessions: sessions, index: index, next: next, log: log}
}

// 頁面路由的路徑（ENTRY 規格的「介面」）。
const (
	PathAdd      = "/add"
	PathShare    = "/share"
	PathLogin    = "/login"
	PathManifest = "/manifest.webmanifest"
	PathSW       = "/sw.js"
)

// IsPagePath 回報路徑是否由 Pages 處理。
func IsPagePath(p string) bool {
	switch p {
	case PathAdd, PathShare, PathLogin, PathManifest, PathSW:
		return true
	}
	return false
}

func (h *Pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !IsPagePath(r.URL.Path) {
		h.next.ServeHTTP(w, r)
		return
	}
	webui.SetSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case PathManifest:
		serveStatic(w, r, "manifest.webmanifest", "application/manifest+json", []byte(Manifest))
		return
	case PathSW:
		serveStatic(w, r, "sw.js", "text/javascript; charset=utf-8", []byte(ServiceWorker))
		return
	}

	// /add、/share、/login：不能被嵌入（CSP 的 frame-ancestors 'none' 已在 SetSecurityHeaders）、不快取（ENTRY-R7）。
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case PathLogin:
		h.serveIndex(w, r)
	case PathAdd:
		target := PathAdd
		if r.URL.RawQuery != "" {
			// 照原始的 query 轉交（不重新編碼、不重排），登入後回到同一個網址。
			target += "?" + r.URL.RawQuery
		}
		signedIn, err := h.signedIn(r)
		switch {
		case err != nil:
			h.fail(w, r, err)
		case !signedIn:
			seeOther(w, loginURL(target))
		default:
			h.serveIndex(w, r)
		}
	case PathShare:
		// 只讀這三個參數；text、title 的其他內容丟棄，不寫進 log（ENTRY-R4）。
		q := r.URL.Query()
		target := PathAdd + "?share_error=no_url"
		if u, ok := ShareURL(q.Get("url"), q.Get("text"), q.Get("title")); ok {
			target = PathAdd + "?url=" + url.QueryEscape(u)
		}
		signedIn, err := h.signedIn(r)
		switch {
		case err != nil:
			h.fail(w, r, err)
		case !signedIn:
			seeOther(w, loginURL(target))
		default:
			seeOther(w, target)
		}
	}
}

// signedIn 只認 session cookie（ENTRY-R2）：bearer token 與 query 中的 token 一律不當成登入。
func (h *Pages) signedIn(r *http.Request) (bool, error) {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil || c.Value == "" {
		return false, nil
	}
	if _, err := h.sessions.AuthenticateSession(r.Context(), c.Value); err != nil {
		if errors.Is(err, auth.ErrUnauthenticated) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (h *Pages) fail(w http.ResponseWriter, r *http.Request, err error) {
	// 只記路徑，不記 query（分享的文字可能含個人內容）。
	h.log.ErrorContext(r.Context(), "驗證頁面路由的 session 時發生錯誤", "path", r.URL.Path, "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (h *Pages) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.index()))
}

func serveStatic(w http.ResponseWriter, r *http.Request, name, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

// loginURL 是登入頁的網址，next 是登入後要回到的站內路徑（ENTRY-R2、R5）。
func loginURL(next string) string {
	return PathLogin + "?next=" + url.QueryEscape(next)
}

// seeOther 回 303 與相對路徑的 Location；不用 http.Redirect（它會改寫路徑並加上 HTML 內容）。
func seeOther(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusSeeOther)
}
