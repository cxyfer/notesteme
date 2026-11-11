package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
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

// requestInfoMiddleware 把來源 IP、User-Agent 與是否為 HTTPS 放進 context。
func requestInfoMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		info := requestInfo{
			IP:        ip,
			UserAgent: r.UserAgent(),
			Secure:    r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyRequestInfo, info)))
	})
}

// needsAuth 回報路徑是否受 AUTH-R1 保護：/api/* 與 /mcp，例外只有 POST /api/auth/login。
// /healthz、/readyz 不在 /api 底下，不受保護。
func needsAuth(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/auth/login" {
		return false
	}
	return p == "/api" || strings.HasPrefix(p, "/api/") || p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}

// safeMethod 回報方法是否不會改變狀態（不需要 CSRF token）。
func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// authMiddleware 對受保護的路徑要求 bearer token 或 session（AUTH-R1）。
// 順序：先驗證（失敗 401），session 的非 GET 請求再檢查 CSRF（失敗 403，AUTH-R5）。
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
				c, err := r.Cookie(auth.SessionCookie)
				if err != nil {
					writeError(w, unauthorized)
					return
				}
				p, err := svc.AuthenticateSession(r.Context(), c.Value)
				if err != nil {
					failAuth(w, r, log, err, unauthorized)
					return
				}
				principal = p
				if !safeMethod(r.Method) {
					csrfCookie, _ := r.Cookie(auth.CSRFCookie)
					var cv string
					if csrfCookie != nil {
						cv = csrfCookie.Value
					}
					if err := auth.CheckCSRF(c.Value, cv, r.Header.Get(auth.CSRFHeader)); err != nil {
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

// logMiddleware 記錄每個請求（方法、路徑、狀態碼、耗時）。
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
