package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// requireScope 檢查目前請求的身分是否具備指定的 scope（AUTH-R8）。
// 必須在 Huma 解析參數與驗證本體之前（透過 registerScoped）及 handler 第一行執行。
// 沒有通過驗證的身分回傳 401；token 缺少該 scope 回傳 403 並帶 required_scope。
func requireScope(ctx context.Context, scope string) error {
	p, ok := principalFrom(ctx)
	if !ok {
		return newError(http.StatusUnauthorized, CodeUnauthorized, "尚未登入或登入已失效。")
	}
	if !p.HasScope(scope) {
		return errForbiddenScope(scope)
	}
	return nil
}

// requireOwner 只讓 owner（session 或本機）通過：token 即使帶了 scope 也不行，因為這些操作會執行程式、登出或洩漏管理資訊。
func requireOwner(ctx context.Context, what string) error {
	p, ok := principalFrom(ctx)
	if !ok {
		return newError(http.StatusUnauthorized, CodeUnauthorized, "尚未登入或登入已失效。")
	}
	if p.Kind == "token" {
		return errForbidden("只有 owner 可以" + what + "。")
	}
	return nil
}

func writeHumaError(hc huma.Context, e *Error) {
	hc.SetHeader("Content-Type", "application/json")
	hc.SetStatus(e.Status)
	_ = json.NewEncoder(hc.BodyWriter()).Encode(e)
}

func humaRequireScope(scope string) func(huma.Context, func(huma.Context)) {
	return func(hc huma.Context, next func(huma.Context)) {
		if err := requireScope(hc.Context(), scope); err != nil {
			writeHumaError(hc, asError(err))
			return
		}
		next(hc)
	}
}

func humaRequireOwner(what string) func(huma.Context, func(huma.Context)) {
	return func(hc huma.Context, next func(huma.Context)) {
		if err := requireOwner(hc.Context(), what); err != nil {
			writeHumaError(hc, asError(err))
			return
		}
		next(hc)
	}
}

// registerScoped 註冊需要指定 scope 的 Huma 路由，讓權限檢查在讀取／驗證請求本體之前先執行。
func registerScoped[I, O any](api huma.API, op huma.Operation, scope string, handler func(context.Context, *I) (*O, error)) {
	op.Middlewares = append(op.Middlewares, humaRequireScope(scope))
	huma.Register(api, op, handler)
}

// registerOwnerOnly 註冊只有 owner 可以呼叫的 Huma 路由，在讀取／驗證請求本體之前先拒絕 token。
func registerOwnerOnly[I, O any](api huma.API, op huma.Operation, what string, handler func(context.Context, *I) (*O, error)) {
	op.Middlewares = append(op.Middlewares, humaRequireOwner(what))
	huma.Register(api, op, handler)
}

// requireScopeHandler 把 scope 檢查套用到直接掛在 mux 上的 http.Handler（例如 SSE /api/events）。
func requireScopeHandler(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := requireScope(r.Context(), scope); err != nil {
			writeError(w, asError(err))
			return
		}
		next.ServeHTTP(w, r)
	})
}
