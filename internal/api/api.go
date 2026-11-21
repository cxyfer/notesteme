// Package api 是 HTTP 層：net/http + Huma v2（humago adapter）。
//
// 受保護的路徑（/api/* 與 /mcp，除了 POST /api/auth/login）在進入 Huma 之前，
// 由 authMiddleware 驗證 session／bearer token 與 CSRF（AUTH-R1、AUTH-R5）。
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
	"github.com/cxyfer/notesteme/internal/queue"
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
	// Prompts 是 prompt 庫（PRM 的 6 條路由）；nil 時相關的路由回應 501。
	Prompts *prompts.Service
	// Queue 提供佇列的狀態與控制（上限、暫停，PIPE-R8、PIPE-R10）；nil 時相關的路由回應 501。
	Queue *queue.Queue
	// Tasks 結束正在執行的任務（取消影音，PIPE-R15）；nil 時只在資料庫中取消。
	Tasks TaskCanceller
	// ReadyChecks 是 /readyz 在「DB 可寫」之外要檢查的項目（OPS-R5），例如外部工具是否可執行。
	ReadyChecks []ReadyCheck
	// Events 是 GET /api/events 的 SSE handler（PIPE-R14）；nil 時回應 501。
	// SSE 是長連線串流，不經過 Huma，直接掛在 mux 上，但仍在驗證 middleware 之後。
	Events http.Handler
	// Paths 檢查 POST /api/items 的本機路徑是否在 inbox 與 media_roots 之內（GRD-R7）。
	// serve 的 TCP 與本機 socket 用 pathguard.New；獨立模式用 pathguard.Unrestricted（GRD-R8）。
	// nil 時一律拒絕（fail closed）。
	Paths *pathguard.Guard
	// Inbox 是 inbox 目錄：用路徑新增 inbox 中的檔案時，識別鍵是相對它的路徑（ING-05）；空白代表沒有 inbox。
	Inbox string
	// DataDir 是資料目錄；上傳先寫到其下的 tmp/（POST /api/items 的 multipart）。空白時上傳回 500。
	DataDir string
	// UploadMaxBytes 是單一上傳檔案的大小上限（library.upload_max_bytes）；0 代表預設的 4 GiB。
	UploadMaxBytes int64
	// Cookies 加密存放平台 cookie（POST /api/cookies，SEC-R4）；nil 時該路由回應 501。
	Cookies CookieStore
	// Resumer 在 cookie 儲存後恢復等待它的 blocked 步驟（PIPE-R4）；nil 時不恢復。
	Resumer Resumer
	// System、Tools、Backup、Providers 是 OPS 切片的依賴；nil 時對應的路由回應 501。
	// 介面只定最小骨架：System 與 Tools 由 OPS-B、Backup 由 OPS-A、Providers 由 OPS-C 擴充。
	System    SystemInfo
	Tools     ToolManager
	Backup    BackupSource
	Providers ProviderRegistry
	// Services 管理外部服務商的 API key（GET／PUT /api/settings/services，SEC-R4、SEC-R6）；nil 時回應 501。
	Services ServiceSettingsStore
	// CursorKey 是列表游標的 HMAC 金鑰（LIB-R4）；空的時候用 processCursorKey（每個 process 隨機產生一次）。
	// 金鑰只存在記憶體，重新啟動後舊游標會回 422，用戶端從第一頁重新讀取即可。
	CursorKey []byte
}

// processCursorKey 是沒有指定 CursorKey 時用的金鑰：每個 process 只產生一次，
// 同一個 process 的所有 handler（TCP 與本機 socket）共用，游標可以跨 listener 使用。
var processCursorKey = sync.OnceValue(func() []byte {
	k := make([]byte, 32)
	rand.Read(k) // Go 1.24 起 crypto/rand.Read 不會回傳錯誤
	return k
})

// ReadyCheck 是 /readyz 的一個檢查項目；Check 回傳 nil 代表正常。
type ReadyCheck struct {
	Name  string
	Check func(ctx context.Context) error
}

// readyTimeout 是 /readyz 檢查 DB 是否可寫的上限。
const readyTimeout = 5 * time.Second

// New 建立完整的 HTTP handler：受保護的路徑要有 session 或 token（AUTH-R1）。
func New(d Deps) http.Handler {
	return build(d, authMiddleware(d.Auth, d.Log))
}

// build 組出路由與 middleware；authn 決定受保護路徑的驗證方式（New 與 NewLocal 的唯一差異）。
func build(d Deps, authn func(http.Handler) http.Handler) http.Handler {
	if len(d.CursorKey) == 0 {
		d.CursorKey = processCursorKey()
	}
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
	if d.Events != nil {
		mux.Handle("GET /api/events", requireScopeHandler(auth.ScopeRead, d.Events))
	} else {
		registerScoped(api, huma.Operation{
			OperationID: "events", Method: http.MethodGet, Path: "/api/events",
			Summary: "狀態變更事件的 SSE 串流（尚未實作）",
		}, auth.ScopeRead, notImplemented)
	}

	r.registerMedia(mux)

	// cookie 上傳的大小限制在驗證之後、Huma 之前（沒登入的請求不會被讀取本體）；
	// 影音檔上傳（multipart 的 POST /api/items）同一層，串流處理而不經過 Huma。
	var h http.Handler = cookieBodyLimit(mux)
	h = uploadIntercept(d)(h)
	h = authn(h)
	h = requestInfoMiddleware(d.Auth)(h)
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
	registerOwnerOnly(api, huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: "/api/auth/logout",
		Summary: "登出，session 立即失效",
	}, "登出", rt.logout)

	registerScoped(api, huma.Operation{
		OperationID: "items-list", Method: http.MethodGet, Path: "/api/items",
		Summary: "影音清單",
	}, auth.ScopeRead, rt.listItems)
	registerScoped(api, huma.Operation{
		OperationID: "items-create", Method: http.MethodPost, Path: "/api/items",
		Summary:       "新增影音：成功建立回 201，已存在回 200（ING-R2）",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeIngest, rt.createItems)
	// 之後切片才會實作的路由：先明確回應 501，不要讓它們看起來像不存在（404）。
	registerScoped(api, huma.Operation{
		OperationID: "resolve", Method: http.MethodPost, Path: "/api/resolve",
		Summary: "辨識網址指向什麼內容，不建立影音（ING-R1、R5、R9）", DefaultStatus: http.StatusOK,
	}, auth.ScopeIngest, rt.resolve)
	registerScoped(api, huma.Operation{
		OperationID: "estimate", Method: http.MethodPost, Path: "/api/estimate",
		Summary: "估算花費，不建立影音（ING-R8）", DefaultStatus: http.StatusOK,
	}, auth.ScopeIngest, rt.estimate)
	registerScoped(api, huma.Operation{
		OperationID: "items-get", Method: http.MethodGet, Path: "/api/items/{id}",
		Summary: "影音詳情：各步驟的狀態與各字幕來源的結果",
	}, auth.ScopeRead, rt.getItem)
	registerScoped(api, huma.Operation{
		OperationID: "items-cues", Method: http.MethodGet, Path: "/api/items/{id}/cues",
		Summary: "字幕內容（cues）",
	}, auth.ScopeRead, rt.getCues)
	registerScoped(api, huma.Operation{
		OperationID: "items-subtitles", Method: http.MethodGet, Path: "/api/items/{id}/subtitles",
		Summary: "下載字幕：從 cues 即時渲染成 srt、vtt、txt 或 cues-json（OUT-R1）",
	}, auth.ScopeRead, rt.getSubtitles)
	registerScoped(api, huma.Operation{
		OperationID: "items-rerun", Method: http.MethodPost, Path: "/api/items/{id}/rerun",
		Summary: "從指定步驟重新處理，產出新版本（PIPE-R7）", DefaultStatus: http.StatusAccepted,
	}, auth.ScopeIngest, rt.rerun)
	registerScoped(api, huma.Operation{
		OperationID: "items-runs", Method: http.MethodGet, Path: "/api/items/{id}/runs",
		Summary: "步驟實際執行過的紀錄與當時生效的設定，由新到舊（PIPE-R13）",
	}, auth.ScopeRead, rt.listRuns)
	rt.registerQueue(api)
	rt.registerCookies(api)
	rt.registerCancel(api)
	// DEL 切片的路由，各自放在獨立的檔案；實作前 pipeline 回 501。
	rt.registerTrash(api)
	rt.registerPurge(api)
	// 之後切片才會實作的路由：先明確回應 501。
	for _, op := range []huma.Operation{
		{OperationID: "tasks-retry", Method: http.MethodPost, Path: "/api/tasks/{id}/retry", Summary: "重試任務（尚未實作）"},
		{OperationID: "tasks-cancel", Method: http.MethodPost, Path: "/api/tasks/{id}/cancel", Summary: "取消單一任務（尚未實作）"},
	} {
		registerScoped(api, op, auth.ScopeAdmin, notImplemented)
	}
	rt.registerTokens(api)
	// OPS 切片的路由，各自放在獨立的檔案；實作前一律回 501。
	rt.registerSystem(api)
	rt.registerProviders(api)
	rt.registerTools(api)
	rt.registerBackup(api)
	rt.registerPrompts(api)
	rt.registerProfiles(api)
	rt.registerChannels(api)
	rt.registerM2Stubs(api)
	registerScoped(api, huma.Operation{
		OperationID: "settings-get", Method: http.MethodGet, Path: "/api/settings/{section}",
		Summary: "讀取全域設定區段（language、timing、llm、agent、subtitle_rules、stages、supadata 與 services）",
	}, auth.ScopeAdmin, rt.getSettings)
	registerScoped(api, huma.Operation{
		OperationID: "settings-put", Method: http.MethodPut, Path: "/api/settings/{section}",
		Summary: "取代全域設定區段（language、timing、llm、agent、subtitle_rules、stages、supadata 與 services）；內容不正確回 422 與逐欄位的 errors",
	}, auth.ScopeAdmin, rt.putSettings)
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
	if err := requireOwner(ctx, "登出"); err != nil {
		return nil, err
	}
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
