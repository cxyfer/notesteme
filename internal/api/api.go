// Package api 是 HTTP 層：net/http + Huma v2（humago adapter）。
//
// 受保護的路徑（/api/* 與 /mcp，除了 POST /api/auth/login）在進入 Huma 之前，
// 由 authMiddleware 驗證 session／bearer token 與 CSRF（AUTH-R1、AUTH-R5）。
package api

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// Deps 是 handler 需要的依賴。
type Deps struct {
	Store   *store.Store
	Auth    *auth.Service
	Clock   clock.Clock
	Log     *slog.Logger
	Version string
	// Pipeline 提供影音的新增與查詢；nil 時相關的路由回應 501。
	Pipeline *pipeline.Pipeline
	// ReadyChecks 是 /readyz 在「DB 可寫」之外要檢查的項目（OPS-R5），例如外部工具是否可執行。
	ReadyChecks []ReadyCheck
}

// ReadyCheck 是 /readyz 的一個檢查項目；Check 回傳 nil 代表正常。
type ReadyCheck struct {
	Name  string
	Check func(ctx context.Context) error
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
		Summary:       "新增影音：成功建立回 201，已存在回 200（ING-R2）",
		DefaultStatus: http.StatusCreated,
	}, rt.createItems)
	// 之後切片才會實作的路由：先明確回應 501，不要讓它們看起來像不存在（404）。
	huma.Register(api, huma.Operation{
		OperationID: "resolve", Method: http.MethodPost, Path: "/api/resolve",
		Summary: "辨識網址指向什麼內容，不建立影音（ING-R1、R5、R9）", DefaultStatus: http.StatusOK,
	}, rt.resolve)
	huma.Register(api, huma.Operation{
		OperationID: "estimate", Method: http.MethodPost, Path: "/api/estimate",
		Summary: "估算花費（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "items-get", Method: http.MethodGet, Path: "/api/items/{id}",
		Summary: "影音詳情：各步驟的狀態與各字幕來源的結果",
	}, rt.getItem)
	huma.Register(api, huma.Operation{
		OperationID: "items-cues", Method: http.MethodGet, Path: "/api/items/{id}/cues",
		Summary: "字幕內容（cues）",
	}, rt.getCues)
	huma.Register(api, huma.Operation{
		OperationID: "items-subtitles", Method: http.MethodGet, Path: "/api/items/{id}/subtitles",
		Summary: "下載字幕：從 cues 即時渲染成 srt、vtt、txt 或 cues-json（OUT-R1）",
	}, rt.getSubtitles)
	huma.Register(api, huma.Operation{
		OperationID: "items-rerun", Method: http.MethodPost, Path: "/api/items/{id}/rerun",
		Summary: "從指定步驟重新處理，產出新版本（PIPE-R7）", DefaultStatus: http.StatusAccepted,
	}, rt.rerun)
	huma.Register(api, huma.Operation{
		OperationID: "queue-pause", Method: http.MethodPost, Path: "/api/queue/pause",
		Summary: "暫停佇列（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "settings-get", Method: http.MethodGet, Path: "/api/settings/{section}",
		Summary: "讀取全域設定區段（目前只有 language；其他區段尚未實作）",
	}, rt.getSettings)
	huma.Register(api, huma.Operation{
		OperationID: "settings-put", Method: http.MethodPut, Path: "/api/settings/{section}",
		Summary: "取代全域設定區段（目前只有 language；其他區段尚未實作）",
	}, rt.putSettings)
	huma.Register(api, huma.Operation{
		OperationID: "providers-create", Method: http.MethodPost, Path: "/api/providers",
		Summary: "新增 provider（尚未實作）",
	}, notImplemented)
	huma.Register(api, huma.Operation{
		OperationID: "mcp", Method: http.MethodPost, Path: "/mcp",
		Summary: "MCP Streamable HTTP（尚未實作）",
	}, notImplemented)
}

func (rt *routes) healthz(context.Context, *struct{}) (*statusOutput, error) {
	return &statusOutput{Body: statusBody{Status: "ok"}}, nil
}

// readyz 檢查資料庫可寫與各項 ReadyChecks；有失敗項目時回傳 503 並列出（OPS-R5）。
// 回應只含項目名稱，不含任何內容資料或內部路徑。
func (rt *routes) readyz(ctx context.Context, _ *struct{}) (*statusOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	var failed []string
	if err := rt.d.Store.CheckWritable(ctx); err != nil {
		rt.d.Log.ErrorContext(ctx, "readyz：資料庫不可寫", "error", err)
		failed = append(failed, "database")
	}
	for _, c := range rt.d.ReadyChecks {
		if err := c.Check(ctx); err != nil {
			rt.d.Log.WarnContext(ctx, "readyz：檢查失敗", "check", c.Name, "error", err)
			failed = append(failed, c.Name)
		}
	}
	if len(failed) > 0 {
		e := newError(http.StatusServiceUnavailable, CodeNotReady, "服務尚未就緒："+strings.Join(failed, "、")+" 無法使用。")
		e.Failed = failed
		return nil, e
	}
	return &statusOutput{Body: statusBody{Status: "ok"}}, nil
}

func (rt *routes) login(ctx context.Context, in *loginInput) (*loginOutput, error) {
	info := infoFrom(ctx)
	cred, err := rt.d.Auth.Login(ctx, in.Body.Password, info.IP, info.UserAgent)
	var locked *auth.LockedError
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		return nil, newError(http.StatusUnauthorized, CodeInvalidPass, auth.ErrInvalidPassword.Error())
	case errors.As(err, &locked):
		secs := int(math.Ceil(locked.RetryAfter.Seconds()))
		e := newError(http.StatusTooManyRequests, CodeLoginLocked, locked.Error())
		e.RetryAfterS = &secs
		return nil, e
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
