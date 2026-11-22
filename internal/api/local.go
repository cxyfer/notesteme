package api

import (
	"context"
	"net/http"

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
func NewLocal(d Deps) http.Handler {
	return build(d, localMiddleware, false)
}

// localMiddleware 把 owner 身分放進 context，取代 authMiddleware。
func localMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.Principal{Kind: PrincipalLocal}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyPrincipal, p)))
	})
}
