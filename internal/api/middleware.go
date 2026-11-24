package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/compat"
)

type ctxKey int

const (
	keyRequestInfo ctxKey = iota
	keyPrincipal
)

// requestInfo 是 middleware 放進 context、handler 需要的請求資訊。
type requestInfo struct {
	IP        string
	UserAgent string
	// Secure 表示請求經由 HTTPS 到達（含反向代理的 X-Forwarded-Proto）。
	Secure bool
}

func infoFrom(ctx context.Context) requestInfo {
	v, _ := ctx.Value(keyRequestInfo).(requestInfo)
	return v
}

// principalFrom 取出通過驗證的身分；公開路由沒有身分。
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(keyPrincipal).(auth.Principal)
	return p, ok
}

// requestInfoMiddleware 把來源位址、User-Agent 與是否為 HTTPS 放進 context。
// 來源位址取自連線；連線來自 trusted_proxies 時改用 X-Forwarded-For（AUTH-R6，見 auth.SourceAddr）。
// svc 為 nil 時（沒有設定 trusted proxies）只用連線位址。
// forceSecure 為 true 時（vps 部署）一律視為 HTTPS：TLS 由前面的 Caddy 終止，cookie 一律帶 Secure（OPS-R21）。
func requestInfoMiddleware(svc *auth.Service, forceSecure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ip string
			if svc != nil {
				ip = svc.SourceAddr(r.RemoteAddr, r.Header.Values("X-Forwarded-For"))
			} else {
				ip = auth.SourceAddr(r.RemoteAddr, nil, nil)
			}
			info := requestInfo{
				IP:        ip,
				UserAgent: r.UserAgent(),
				Secure:    forceSecure || r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyRequestInfo, info)))
		})
	}
}

// 免驗證的 OIDC 與登入方式路由（AUTH-R16；登入頁的 OIDC 按鈕）。只開放 GET 與這幾條精確的路徑：
// OIDC 沒有啟用時路由不存在，回 404。
const (
	pathOIDCStart    = "/api/auth/oidc/start"
	pathOIDCCallback = "/api/auth/oidc/callback"
	pathAuthMethods  = "/api/auth/methods"
)

// needsAuth 回報路徑是否受 AUTH-R1 保護：/api/* 與 /mcp，例外只有 POST /api/auth/login、
// GET /api/auth/oidc/start、GET /api/auth/oidc/callback（AUTH-R16）與 GET /api/auth/methods。
// /healthz、/readyz 不在 /api 底下，不受保護。
func needsAuth(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/auth/login" {
		return false
	}
	if r.Method == http.MethodGet && (p == pathOIDCStart || p == pathOIDCCallback || p == pathAuthMethods) {
		return false
	}
	return p == "/api" || strings.HasPrefix(p, "/api/") || isMCPPath(p)
}

func isMCPPath(p string) bool { return p == "/mcp" || strings.HasPrefix(p, "/mcp/") }

// safeMethod 回報方法是否不會改變狀態（不需要 CSRF token）。
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// proxyMiddleware 判斷反向代理驗證的標頭（AUTH-R13），把結果放進 context（auth.WithProxyUser），
// 並從請求中移除這個標頭，之後的 handler 都看不到原始值。
//   - 只信任 TCP 對端位址（r.RemoteAddr）在 trusted_proxies 中的請求；其他來源的標頭忽略並記 log（在 auth.ProxyUser）。
//   - 帶 bearer token 的請求完全不看這個標頭（AUTH-R15），也不撤銷任何 session。
//   - /mcp 不接受 proxy 身分（AUTH-R15、待決問題 A6）：不判斷標頭，所以 proxy session 在 /mcp 只會被拒絕、不會被撤銷。
func proxyMiddleware(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if svc == nil || !svc.ProxyEnabled() {
			return next
		}
		header := svc.ProxyHeader()
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			values := r.Header.Values(header)
			r.Header.Del(header)
			if r.Header.Get("Authorization") != "" || isMCPPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			user := svc.ProxyUser(r.Context(), r.RemoteAddr, values)
			next.ServeHTTP(w, r.WithContext(auth.WithProxyUser(r.Context(), user)))
		})
	}
}

// authMiddleware 對受保護的路徑要求 bearer token 或 session（AUTH-R1）。
// 順序：先驗證（失敗 401），session 的非 GET 請求再檢查 CSRF（失敗 403，AUTH-R5）。
//
// 反向代理驗證（AUTH-R14）：受信任的 proxy 帶來的值不在 allowed_users 中時回 403；
// 在清單中而沒有有效的 session 時建立 proxy session 並設定 cookie，這個請求就以它驗證（CSRF 照常檢查）。
// proxy 與 OIDC 的 session 不能存取 /mcp（AUTH-R15、待決問題 A6）。
func authMiddleware(svc *auth.Service, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !needsAuth(r) {
				next.ServeHTTP(w, r)
				return
			}
			unauthorized := newError(http.StatusUnauthorized, CodeUnauthorized, "尚未登入或登入已失效。")

			var principal auth.Principal
			if h := r.Header.Get("Authorization"); h != "" {
				token, ok := strings.CutPrefix(h, "Bearer ")
				if !ok || strings.TrimSpace(token) == "" {
					writeError(w, unauthorized)
					return
				}
				p, err := svc.AuthenticateBearer(r.Context(), strings.TrimSpace(token))
				if err != nil {
					failAuth(w, r, log, err, unauthorized)
					return
				}
				principal = p
			} else {
				var sessionToken string
				if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
					p, err := svc.AuthenticateSession(r.Context(), c.Value)
					switch {
					case err == nil:
						principal, sessionToken = p, c.Value
					case !errors.Is(err, auth.ErrUnauthenticated):
						failAuth(w, r, log, err, unauthorized)
						return
					}
				}
				freshSession := false
				if proxyUser, _ := auth.ProxyUserFrom(r.Context()); proxyUser != "" {
					if !svc.ProxyUserAllowed(proxyUser) {
						writeError(w, newError(http.StatusForbidden, CodeProxyUserNotAllowed,
							"這個帳號（"+auth.ProxyUserShown(proxyUser)+"）不能使用 Notesteme。"))
						return
					}
					if sessionToken == "" {
						info := infoFrom(r.Context())
						cred, err := svc.ProxyLogin(r.Context(), proxyUser, info.IP, info.UserAgent)
						if err != nil {
							failAuth(w, r, log, err, unauthorized)
							return
						}
						now := svc.Now()
						w.Header().Add("Set-Cookie", sessionCookie(cred.Token, info.Secure, cred.ExpiresAt, now).String())
						w.Header().Add("Set-Cookie", csrfCookie(cred.CSRF, info.Secure, cred.ExpiresAt, now).String())
						sessionToken = cred.Token
						principal = auth.Principal{Kind: "session", SessionID: cred.SessionID, Method: "proxy"}
						// 新的 session：這個請求不可能帶著它的 CSRF token，非 GET 請求一律 403（AUTH-R15）。
						freshSession = true
					}
				}
				if sessionToken == "" {
					writeError(w, unauthorized)
					return
				}
				if isMCPPath(r.URL.Path) && principal.Method != "" && principal.Method != "password" {
					writeError(w, unauthorized)
					return
				}
				if !safeMethod(r.Method) {
					var cv string
					if csrfCookie, _ := r.Cookie(auth.CSRFCookie); csrfCookie != nil {
						cv = csrfCookie.Value
					}
					if err := auth.CheckCSRF(sessionToken, cv, r.Header.Get(auth.CSRFHeader)); freshSession || err != nil {
						writeError(w, newError(http.StatusForbidden, CodeCSRF, "CSRF token 缺少或不正確。"))
						return
					}
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyPrincipal, principal)))
		})
	}
}

// failAuth 回應驗證失敗：無效的憑證是 401，其餘（例如 DB 錯誤）是 500。
func failAuth(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error, unauthorized *Error) {
	if errors.Is(err, auth.ErrUnauthenticated) {
		writeError(w, unauthorized)
		return
	}
	log.ErrorContext(r.Context(), "驗證時發生錯誤", "error", err)
	writeError(w, asError(err))
}

// statusRecorder 記錄回應狀態碼供 log 使用，並保留 Flusher 等能力（Unwrap）。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logMiddleware 記錄每個請求（方法、路徑、狀態碼、耗時；request_id 由 logctx 從 context 帶上）。
// 不記錄 query、標頭、cookie 與 body，避免 secret 進入 log（NFR-SEC-3）。
func logMiddleware(log *slog.Logger, clk clock.Clock) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			log.InfoContext(r.Context(), "http request",
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", clk.Now().Sub(start).Milliseconds())
		})
	}
}

// CodeClientIncompatible 是 CLI 與 serve 的版本不相容（CLI-R9、NFR-CMP-4）。
const CodeClientIncompatible = "client_incompatible"

// versionMiddleware 檢查 CLI 帶來的 X-Notesteme-Version（CLI-R9）。沒有這個標頭的請求（瀏覽器、MCP、Agent）不檢查，
// 回應也不帶版本；有的話回應帶上 serve 的版本，不相容時回 426 client_incompatible，訊息指出要升級哪一邊。
// RFC 9110 規定 426 要帶 Upgrade 標頭：值是 serve 的版本（Notesteme/<版本>），也就是 CLI 要對齊的對象。
func versionMiddleware(own string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cv := r.Header.Get(compat.Header)
			if cv == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set(compat.Header, own)
			if v := compat.Check(cv, own); v != compat.Compatible {
				w.Header().Set("Upgrade", "Notesteme/"+own)
				writeError(w, newError(http.StatusUpgradeRequired, CodeClientIncompatible, compat.Message(v, cv, own)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// recoverMiddleware 把 handler 的 panic 轉成 500，並記錄。
func recoverMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler { //nolint:errorlint // 官方建議以相等比較
						panic(v)
					}
					log.ErrorContext(r.Context(), "handler panic", "panic", v)
					writeError(w, asError(nil))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
