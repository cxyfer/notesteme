package api

import (
	"context"
	"net/http"
)

// requireScope 檢查目前請求的身分是否具備指定的 scope（AUTH-R8）。
// 必須放在每個受保護 handler 的第一行（在 Pipeline == nil 與 501 檢查之前）。
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

// scopedNotImplemented 回傳先檢查 scope、再回應 501 的 Huma handler（避免 501 繞過 AUTH-R8）。
func scopedNotImplemented(scope string) func(context.Context, *struct{}) (*emptyOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*emptyOutput, error) {
		if err := requireScope(ctx, scope); err != nil {
			return nil, err
		}
		return notImplemented(ctx, nil)
	}
}
