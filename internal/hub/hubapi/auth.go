package hubapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/websession"
)

// 登入、session 與帳號連結的路由（M5-S1，HACC-R1～R8），以及依 Access 的身分驗證、限流與權限 middleware（M5-S2）。
//
// middleware 在讀取 body 與查詢資源之前執行（HACC-R13），順序：
//  1. AccessPublic 只受 IP 限流（/healthz、/readyz 不限）。
//  2. 有 Authorization 標頭時只看 Hub token（不退回 cookie）：格式不符、不存在、撤銷、過期都回 401；沒有標頭時看 session cookie。
//     沒有有效身分時以 IP 限流，之後回 401 unauthorized；site.visibility 為 public 時，未登入的請求可以進到需要 read 的路由
//     （內容的可見度由路由自己處理，HACC-R14）。
//  3. 帳號停用時回 403 account_suspended（session 與 PAT 都是）。
//  4. Hub token 打到只接受 session 的路由回 403 session_required（HACC 待決問題 A12）。
//  5. session 的非 GET 請求要帶 CSRF token，否則 403 csrf_invalid（PAT 不需要）。
//  6. 以帳號限流（HACC-R18）。
//  7. 路由需要權限時，有效權限（PAT ∩ 帳號當下的權限）沒有它就回 403 forbidden，帶 required_permission。

type principalKey struct{}

// remoteAddrKey 是公開路由的連線位址（下架申請以 IP 限流，HACC-R15）。
type remoteAddrKey struct{}

// remoteAddrFrom 回傳 middleware 記下的連線位址；沒有時是空字串。
func remoteAddrFrom(ctx context.Context) string {
	s, _ := ctx.Value(remoteAddrKey{}).(string)
	return s
}

// principalFrom 回傳 middleware 驗證過的身分；沒有時是 nil（公開路由或未登入的公開站台）。
func principalFrom(ctx context.Context) *hubauth.Principal {
	p, _ := ctx.Value(principalKey{}).(*hubauth.Principal)
	return p
}

// accessOf 回傳 operation 宣告的存取層級；沒有宣告的 operation 視為需要登入（預設拒絕）。
func accessOf(op *huma.Operation) Access {
	if op != nil {
		if v, ok := op.Extensions[AccessExtension].(string); ok && v != "" {
			return Access(v)
		}
	}
	return AccessAuthenticated
}

// setRetryAfter 在 429 的回應設定 Retry-After 標頭（HACC-R18）。
func setRetryAfter(set func(string, string), e *apierr.Error) {
	if e.RetryAfterS != nil {
		set("Retry-After", strconv.Itoa(*e.RetryAfterS))
	}
}

func writeHumaErr(ctx huma.Context, e *apierr.Error) {
	ctx.SetHeader("Content-Type", "application/json")
	setRetryAfter(ctx.SetHeader, e)
	ctx.SetStatus(e.Status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(e)
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// isPermission 回報 Access 是不是一個權限名稱（而不是 public、session、authenticated）。
func isPermission(a Access) bool { return slices.Contains(hubconfig.Permissions, string(a)) }

// unlimitedOps 是不受限流的公開路由（健康檢查）。
var unlimitedOps = map[string]bool{"healthz": true, "readyz": true}

func (rt *routes) rateLimits() hubconfig.RateLimits {
	if rt.d.Config == nil {
		return hubconfig.RateLimits{}
	}
	return rt.d.Config.RateLimits
}

// authenticate 依 Authorization 標頭或 session cookie 取得身分；沒有有效身分時回 (nil, nil)。
// bearer 為 true 表示請求帶了 Authorization 標頭：驗證失敗時一律回 401，不退回匿名（HACC-R9，安全修補 A）。
func (rt *routes) authenticate(ctx huma.Context) (p *hubauth.Principal, bearer bool, err error) {
	if rt.d.Auth == nil {
		return nil, false, nil
	}
	if h := strings.TrimSpace(ctx.Header("Authorization")); h != "" {
		scheme, tok, ok := strings.Cut(h, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return nil, true, nil
		}
		p, err := rt.d.Auth.AuthenticateToken(ctx.Context(), strings.TrimSpace(tok))
		if hubauth.IsNoSession(err) {
			return nil, true, nil
		}
		return p, true, err
	}
	c, err := huma.ReadCookie(ctx, hubauth.SessionCookie)
	if err != nil || c.Value == "" {
		return nil, false, nil
	}
	p, err = rt.d.Auth.Authenticate(ctx.Context(), c.Value)
	if hubauth.IsNoSession(err) {
		return nil, false, nil
	}
	return p, false, err
}

func (rt *routes) accessMiddleware(ctx huma.Context, next func(huma.Context)) {
	op := ctx.Operation()
	access := accessOf(op)
	if p := loopbackPrincipal(ctx.Context()); p != nil {
		rt.loopbackAccess(ctx, next, p, access)
		return
	}
	if access == AccessPublic {
		if op == nil || !unlimitedOps[op.OperationID] {
			if e := rt.limitAnonymous(ctx.RemoteAddr()); e != nil {
				writeHumaErr(ctx, e)
				return
			}
		}
		next(huma.WithValue(ctx, remoteAddrKey{}, ctx.RemoteAddr()))
		return
	}
	p, bearer, err := rt.authenticate(ctx)
	if err != nil {
		writeHumaErr(ctx, rt.apiError(ctx.Context(), err))
		return
	}
	if p == nil {
		// 無效的憑證也計入 IP 的額度：猜 token 的請求同樣受限。
		if e := rt.limitAnonymous(ctx.RemoteAddr()); e != nil {
			writeHumaErr(ctx, e)
			return
		}
		// 帶了 token 卻無效（撤銷、過期、格式錯誤）時不退回匿名：用戶端要知道 token 已經不能用（HACC-R9）。
		if !bearer && access == Access(hubconfig.PermRead) && rt.d.Config != nil && rt.d.Config.SiteVisibility == hubconfig.VisibilityPublic {
			next(ctx)
			return
		}
		writeHumaErr(ctx, hubauth.ErrUnauthorized())
		return
	}
	if access == AccessSession && !p.IsSession() {
		writeHumaErr(ctx, hubauth.ErrSessionRequired())
		return
	}
	if p.IsSession() && !safeMethod(ctx.Method()) {
		csrf, _ := huma.ReadCookie(ctx, hubauth.CSRFCookie)
		cv := ""
		if csrf != nil {
			cv = csrf.Value
		}
		if websession.CheckCSRF(p.Token, cv, ctx.Header(hubauth.CSRFHeader)) != nil {
			writeHumaErr(ctx, hubauth.ErrCSRF())
			return
		}
	}
	if e := rt.limitAccount(p.User.ID); e != nil {
		writeHumaErr(ctx, e)
		return
	}
	if isPermission(access) {
		ok, err := rt.d.Auth.HasPermission(ctx.Context(), p, string(access))
		if err != nil {
			writeHumaErr(ctx, rt.apiError(ctx.Context(), err))
			return
		}
		if !ok {
			writeHumaErr(ctx, hubauth.ErrForbidden(string(access)))
			return
		}
	}
	next(huma.WithValue(ctx, principalKey{}, p))
}

// loopbackAccess 是 Hub MCP 的 tool 在程序內呼叫的路由（mcp.go）：身分已在 /mcp 驗證、限流已計入，
// 這裡只做唯讀與權限的檢查（與 hubmcp 的檢查構成雙重檢查，HMCP-R4）。
func (rt *routes) loopbackAccess(ctx huma.Context, next func(huma.Context), p *hubauth.Principal, access Access) {
	if ctx.Method() != http.MethodGet || access == AccessSession {
		writeHumaErr(ctx, mcpForbidden("Hub MCP 只能讀取。"))
		return
	}
	if isPermission(access) {
		ok, err := rt.d.Auth.HasPermission(ctx.Context(), p, string(access))
		if err != nil {
			writeHumaErr(ctx, rt.apiError(ctx.Context(), err))
			return
		}
		if !ok {
			writeHumaErr(ctx, hubauth.ErrForbidden(string(access)))
			return
		}
	}
	next(huma.WithValue(ctx, principalKey{}, p))
}

// apiError 把錯誤轉成回應；不是 *apierr.Error 的寫進 log 並回 500（不洩漏細節）。
func (rt *routes) apiError(ctx context.Context, err error) *apierr.Error {
	var e *apierr.Error
	if errors.As(err, &e) {
		return e
	}
	rt.d.Log.ErrorContext(ctx, "Hub 請求失敗", "error", err)
	return apierr.As(nil)
}

// ---- cookie ----

func oauthCookie(state string) *http.Cookie {
	return &http.Cookie{
		Name: hubauth.OAuthCookie, Value: state, Path: hubauth.OAuthCookiePath, MaxAge: int(hubauth.StateTTL / time.Second),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	}
}

func clearCookie(name, path string, httpOnly bool) *http.Cookie {
	return &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: httpOnly, Secure: true, SameSite: http.SameSiteLaxMode}
}

func sessionCookies(ns *hubauth.NewSession) []*http.Cookie {
	age := int(hubauth.SessionIdleTimeout / time.Second)
	return []*http.Cookie{
		{Name: hubauth.SessionCookie, Value: ns.Token, Path: "/", MaxAge: age, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode},
		// CSRF cookie 讓前端讀取後放進 X-CSRF-Token，所以不是 HttpOnly（同 Client）。
		{Name: hubauth.CSRFCookie, Value: ns.CSRF, Path: "/", MaxAge: age, Secure: true, SameSite: http.SameSiteLaxMode},
	}
}

func clearSessionCookies() []*http.Cookie {
	return []*http.Cookie{clearCookie(hubauth.SessionCookie, "/", true), clearCookie(hubauth.CSRFCookie, "/", false)}
}

// ---- 瀏覽器的 OAuth 流程（不經過 Huma）----

func writeRawErr(w http.ResponseWriter, e *apierr.Error) {
	w.Header().Set("Cache-Control", "no-store")
	setRetryAfter(w.Header().Set, e)
	apierr.Write(w, e)
}

// oauthStart 是 GET /auth/<provider>/start（HACC-R1、R2）。
func (rt *routes) oauthStart(w http.ResponseWriter, r *http.Request) {
	if rt.d.Auth == nil {
		writeRawErr(w, apierr.NotImplemented())
		return
	}
	u, state, err := rt.d.Auth.Begin(r.Context(), r.PathValue("provider"), hubstore.IntentLogin, "")
	if err != nil {
		writeRawErr(w, rt.apiError(r.Context(), err))
		return
	}
	http.SetCookie(w, oauthCookie(state))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", u)
	w.WriteHeader(http.StatusFound)
}

// oauthCallback 是 GET /auth/<provider>/callback（HACC-R3～R6）。成功後轉到固定的目的地，不接受轉址參數。
func (rt *routes) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if rt.d.Auth == nil {
		writeRawErr(w, apierr.NotImplemented())
		return
	}
	// callback 的網址帶著授權碼：不讓瀏覽器把它放進 Referer。
	w.Header().Set("Referrer-Policy", "no-referrer")
	cookieState := ""
	if c, err := r.Cookie(hubauth.OAuthCookie); err == nil {
		cookieState = c.Value
	}
	var current *hubauth.Principal
	if c, err := r.Cookie(hubauth.SessionCookie); err == nil && c.Value != "" {
		// 停用或失效的 session 當作沒有登入：登入流程本身會再檢查帳號狀態。
		if p, err := rt.d.Auth.Authenticate(r.Context(), c.Value); err == nil {
			current = p
		}
	}
	res, err := rt.d.Auth.Complete(r.Context(), r.PathValue("provider"), r.URL.Query(), cookieState, current)
	// 不論成功與否，這次等待中的登入都結束了。
	http.SetCookie(w, clearCookie(hubauth.OAuthCookie, hubauth.OAuthCookiePath, true))
	if err != nil {
		writeRawErr(w, rt.apiError(r.Context(), err))
		return
	}
	if res.Session != nil {
		for _, c := range sessionCookies(res.Session) {
			http.SetCookie(w, c)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", res.Redirect)
	w.WriteHeader(http.StatusFound)
}

// ---- /api/v1 ----

type providersOutput struct {
	Body struct {
		Providers []hubauth.Provider `json:"providers"`
	}
}

func (rt *routes) authProviders(context.Context, *struct{}) (*providersOutput, error) {
	o := &providersOutput{}
	o.Body.Providers = []hubauth.Provider{}
	if rt.d.Auth != nil {
		o.Body.Providers = rt.d.Auth.Providers()
	}
	return o, nil
}

type cookiesOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

func cookieValues(cs []*http.Cookie) []http.Cookie {
	out := make([]http.Cookie, 0, len(cs))
	for _, c := range cs {
		out = append(out, *c)
	}
	return out
}

// mustPrincipal 回傳身分；middleware 保證非公開的路由一定有（公開站台的匿名 read 除外）。
func mustPrincipal(ctx context.Context) (*hubauth.Principal, error) {
	if p := principalFrom(ctx); p != nil {
		return p, nil
	}
	return nil, hubauth.ErrUnauthorized()
}

func (rt *routes) logout(ctx context.Context, _ *struct{}) (*cookiesOutput, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := rt.d.Auth.Logout(ctx, p); err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return &cookiesOutput{SetCookie: cookieValues(clearSessionCookies())}, nil
}

type meOutput struct {
	Body hubauth.Me
}

func (rt *routes) me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	me, err := rt.d.Auth.Me(ctx, p)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return &meOutput{Body: me}, nil
}

type providerPath struct {
	Provider string `path:"provider" enum:"google,github,linuxdo" doc:"登入方式"`
}

type linkOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      struct {
		AuthorizeURL string `json:"authorize_url" doc:"供應商的授權網址；瀏覽器前往這裡完成連結"`
	}
}

func (rt *routes) linkIdentity(ctx context.Context, in *providerPath) (*linkOutput, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	u, state, err := rt.d.Auth.BeginLink(ctx, p, in.Provider)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &linkOutput{SetCookie: []http.Cookie{*oauthCookie(state)}}
	o.Body.AuthorizeURL = u
	return o, nil
}

func (rt *routes) unlinkIdentity(ctx context.Context, in *providerPath) (*struct{}, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := rt.d.Auth.Unlink(ctx, p, in.Provider); err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return nil, nil
}

// registerAuth 註冊 M5-S1 實作的 /api/v1 路由（權限矩陣的 Access 一併宣告，HACC A9）。
func registerAuth(api huma.API, rt *routes) {
	reg := func(op huma.Operation, access Access, spec string) huma.Operation {
		op.Description = "規則：" + spec + "。"
		op.Extensions = map[string]any{AccessExtension: string(access)}
		return op
	}
	huma.Register(api, reg(huma.Operation{
		OperationID: "auth-providers", Method: http.MethodGet, Path: "/api/v1/auth/providers",
		Summary: "列出已啟用的登入方式（依 google、github、linuxdo 的順序）",
	}, AccessPublic, "HACC-R1"), rt.authProviders)
	huma.Register(api, reg(huma.Operation{
		OperationID: "auth-logout", Method: http.MethodPost, Path: "/api/v1/auth/logout", DefaultStatus: http.StatusNoContent,
		Summary: "登出，session 立即失效", Errors: []int{http.StatusUnauthorized, http.StatusForbidden},
	}, AccessSession, "HACC-R8"), rt.logout)
	huma.Register(api, reg(huma.Operation{
		OperationID: "me", Method: http.MethodGet, Path: "/api/v1/me",
		Summary: "目前的帳號、登入方式、群組與有效權限", Errors: []int{http.StatusUnauthorized, http.StatusForbidden},
	}, AccessAuthenticated, "HACC-R6、ADR-0032"), rt.me)
	huma.Register(api, reg(huma.Operation{
		OperationID: "me-identities-link", Method: http.MethodPost, Path: "/api/v1/me/identities/{provider}/link",
		Summary: "連結新的登入方式：回傳供應商的授權網址",
		Errors:  []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict},
	}, AccessSession, "HACC-R6"), rt.linkIdentity)
	huma.Register(api, reg(huma.Operation{
		OperationID: "me-identities-unlink", Method: http.MethodDelete, Path: "/api/v1/me/identities/{provider}", DefaultStatus: http.StatusNoContent,
		Summary: "移除登入方式", Errors: []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict},
	}, AccessSession, "HACC-R7"), rt.unlinkIdentity)
}

func registerOAuth(mux *http.ServeMux, rt *routes) {
	mux.HandleFunc("GET /auth/{provider}/start", rt.limitRaw(rt.oauthStart))
	mux.HandleFunc("GET /auth/{provider}/callback", rt.limitRaw(rt.oauthCallback))
}
