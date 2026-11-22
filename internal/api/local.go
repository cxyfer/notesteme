package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cxyfer/notesteme/internal/auth"
)

// PrincipalLocal 是 NewLocal 的 handler 賦予每個請求的身分種類。
const PrincipalLocal = "local"

// NewLocal 建立給本機使用的 HTTP handler：路由與 New 完全相同，
// 但每個請求都以 owner 身分（Kind "local"，Scopes 為 nil 代表全部權限）執行，
// 不需要 session cookie、bearer token 或 CSRF token。
//
// 這個 handler 本身不做任何驗證，所以只能掛在已經由其他機制限定呼叫端的地方：
// 程序內的 RoundTripper（standalone 模式），以及權限 0600 並檢查對端 uid 的 Unix socket。
// 絕對不能交給 TCP listener。
//
// 例外：請求帶了 bearer token 時改用那個 token 的身分（只會縮小權限）。`notesteme mcp --token` 在本機模式下
// 用它限制 Agent 的 scope（MCP-R1，MCP 規格待決問題，待 owner 追認）；無效的 token 回 401。
func NewLocal(d Deps) http.Handler {
	return build(d, localMiddleware(d.Auth, d.Log), false)
}

// localMiddleware 把 owner 身分放進 context，取代 authMiddleware；帶 bearer token 的請求改用 token 的身分。
func localMiddleware(svc *auth.Service, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := auth.Principal{Kind: PrincipalLocal}
			if h := r.Header.Get("Authorization"); h != "" {
				unauthorized := newError(http.StatusUnauthorized, CodeUnauthorized, "尚未登入或登入已失效。")
				token, ok := strings.CutPrefix(h, "Bearer ")
				if !ok || strings.TrimSpace(token) == "" || svc == nil { // 沒有驗證服務時不放大成 owner
					writeError(w, unauthorized)
					return
				}
				tp, err := svc.AuthenticateBearer(r.Context(), strings.TrimSpace(token))
				if err != nil {
					failAuth(w, r, log, err, unauthorized)
					return
				}
				p = tp
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyPrincipal, p)))
		})
	}
}
