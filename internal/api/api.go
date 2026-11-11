// Package api 是 HTTP 層：net/http + Huma v2（humago adapter）。
//
// 受保護的路徑（/api/* 與 /mcp，除了 POST /api/auth/login）在進入 Huma 之前，
// 由 authMiddleware 驗證 session／bearer token 與 CSRF（AUTH-R1、AUTH-R5）。
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// Deps 是 handler 需要的依賴。
type Deps struct {
	Store   *store.Store
	Auth    *auth.Service
	Clock   clock.Clock
	Log     *slog.Logger
	Version string
}

// readyTimeout 是 /readyz 檢查 DB 是否可寫的上限。
const readyTimeout = 5 * time.Second

// New 建立完整的 HTTP handler。
func New(d Deps) http.Handler {
	mux := http.NewServeMux()

	cfg := huma.DefaultConfig("Notesteme", d.Version)
	// OpenAPI 放在 /api 底下，隨 AUTH-R1 一起受保護；不提供公開的文件頁。
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.CreateHooks = nil
	api := humago.New(mux, cfg)

	r := &routes{d: d}
	r.register(api)

	var h http.Handler = mux
	h = authMiddleware(d.Auth, d.Log)(h)
	h = requestInfoMiddleware(h)
	h = logMiddleware(d.Log, d.Clock)(h)
	h = recoverMiddleware(d.Log)(h)
	return h
}

type routes struct{ d Deps }

type statusBody struct {
	Status string `json:"status" example:"ok"`
}

type statusOutput struct{ Body statusBody }

type loginInput struct {
	Body struct {
		Password string `json:"password" minLength:"1" doc:"登入密碼"`
	}
}

type loginOutput struct {
	SetCookie []string `header:"Set-Cookie"`
	Body      struct {
		ExpiresAt time.Time `json:"expires_at" doc:"session 目前的到期時間"`
	}
}

type logoutInput struct {
	Session string `cookie:"notesteme_session"`
}

type logoutOutput struct {
	SetCookie []string `header:"Set-Cookie"`
}

// Item 是影音項目；欄位在 ING 切片加入。
type Item struct{}

type listItemsOutput struct {
	Body struct {
		Items []Item `json:"items"`
	}
}

type emptyOutput struct{}

func (rt *routes) register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "healthz", Method: http.MethodGet, Path: "/healthz",
		Summary: "存活檢查（免驗證）",
	}, rt.healthz)
	huma.Register(api, huma.Operation{
		OperationID: "readyz", Method: http.MethodGet, Path: "/readyz",
		Summary: "就緒檢查：確認資料庫可寫（免驗證）",
	}, rt.readyz)

	huma.Register(api, huma.Operation{
		OperationID: "auth-login", Method: http.MethodPost, Path: "/api/auth/login",
		Summary: "以密碼登入，設定 session 與 CSRF cookie",
	}, rt.login)
	huma.Register(api, huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: "/api/auth/logout",
		Summary: "登出，session 立即失效",
	}, rt.logout)

	huma.Register(api, huma.Operation{
		OperationID: "items-list", Method: http.MethodGet, Path: "/api/items",
		Summary: "影音清單",
	}, rt.listItems)
	huma.Register(api, huma.Operation{
		OperationID: "items-create", Method: http.MethodPost, Path: "/api/items",
		Summary: "新增影音（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "queue-pause", Method: http.MethodPost, Path: "/api/queue/pause",
		Summary: "暫停佇列（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "mcp", Method: http.MethodPost, Path: "/mcp",
		Summary: "MCP Streamable HTTP（尚未實作）",
	}, notImplemented)
}

func (rt *routes) healthz(context.Context, *struct{}) (*statusOutput, error) {
	return &statusOutput{Body: statusBody{Status: "ok"}}, nil
}

func (rt *routes) readyz(ctx context.Context, _ *struct{}) (*statusOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	if err := rt.d.Store.CheckWritable(ctx); err != nil {
		rt.d.Log.ErrorContext(ctx, "readyz：資料庫不可寫", "error", err)
		return nil, newError(http.StatusServiceUnavailable, CodeNotReady, "資料庫目前無法寫入。")
	}
	return &statusOutput{Body: statusBody{Status: "ok"}}, nil
}

func (rt *routes) login(ctx context.Context, in *loginInput) (*loginOutput, error) {
	info := infoFrom(ctx)
	cred, err := rt.d.Auth.Login(ctx, in.Body.Password, info.IP, info.UserAgent)
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		return nil, newError(http.StatusUnauthorized, CodeInvalidPass, auth.ErrInvalidPassword.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "登入時發生錯誤", "error", err)
		return nil, asError(err)
	}
	out := &loginOutput{}
	out.Body.ExpiresAt = cred.ExpiresAt
	out.SetCookie = []string{
		sessionCookie(cred.Token, info.Secure, cred.ExpiresAt, rt.d.Clock.Now()).String(),
		csrfCookie(cred.CSRF, info.Secure, cred.ExpiresAt, rt.d.Clock.Now()).String(),
	}
	return out, nil
}

func (rt *routes) logout(ctx context.Context, in *logoutInput) (*logoutOutput, error) {
	if err := rt.d.Auth.Logout(ctx, in.Session); err != nil {
		rt.d.Log.ErrorContext(ctx, "登出時發生錯誤", "error", err)
		return nil, asError(err)
	}
	info := infoFrom(ctx)
	return &logoutOutput{SetCookie: []string{
		expiredCookie(auth.SessionCookie, true, info.Secure).String(),
		expiredCookie(auth.CSRFCookie, false, info.Secure).String(),
	}}, nil
}

func (rt *routes) listItems(context.Context, *struct{}) (*listItemsOutput, error) {
	out := &listItemsOutput{}
	out.Body.Items = []Item{}
	return out, nil
}

// notImplemented 是尚未實作的路由；驗證與 CSRF 已先由 middleware 處理。
func notImplemented(context.Context, *struct{}) (*emptyOutput, error) {
	return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
}

func sessionCookie(token string, secure bool, expires, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name: auth.SessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure,
		Expires: expires, MaxAge: int(expires.Sub(now).Seconds()),
	}
}

// csrfCookie 不設 HttpOnly：前端要讀它，放進 X-CSRF-Token 標頭（double-submit）。
func csrfCookie(token string, secure bool, expires, now time.Time) *http.Cookie {
	return &http.Cookie{
		Name: auth.CSRFCookie, Value: token, Path: "/",
		HttpOnly: false, SameSite: http.SameSiteLaxMode, Secure: secure,
		Expires: expires, MaxAge: int(expires.Sub(now).Seconds()),
	}
}

func expiredCookie(name string, httpOnly, secure bool) *http.Cookie {
	return &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode, Secure: secure,
	}
}
