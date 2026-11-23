package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// OIDCCookie 綁定一次 OIDC 登入（AUTH-R17）；只送回 /api/auth/oidc 底下。
const OIDCCookie = "notesteme_oidc"

const oidcCookiePath = "/api/auth/oidc"

// registerOIDC 註冊 OIDC 登入（AUTH-R16～R19）與登入頁用的登入方式。三條都是 GET、免驗證（needsAuth）。
// OIDC 沒有啟用時 start 與 callback 回 404。
func (rt *routes) registerOIDC(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "auth-oidc-start", Method: http.MethodGet, Path: pathOIDCStart,
		Summary: "開始 OIDC 登入：302 到身分提供者（PKCE S256），設定 notesteme_oidc cookie；沒有啟用 OIDC 時 404（AUTH-R16、R17）",
		Errors:  []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway},
	}, rt.oidcStart)
	huma.Register(api, huma.Operation{
		OperationID: "auth-oidc-callback", Method: http.MethodGet, Path: pathOIDCCallback,
		Summary: "OIDC 的 callback：驗證 state、換 token、驗證 id_token 與 sub，建立 session 後 302 到 next（AUTH-R18、R19）",
		Errors:  []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
	}, rt.oidcCallback)
	huma.Register(api, huma.Operation{
		OperationID: "auth-methods", Method: http.MethodGet, Path: pathAuthMethods,
		Summary: "登入頁可用的登入方式：OIDC 是否啟用與 issuer 的主機名稱（免驗證）",
	}, rt.authMethods)
}

type oidcStartInput struct {
	Next string `query:"next" doc:"登入後要前往的站內路徑；不安全或超過 2048 bytes 時換成 /（AUTH-R17）"`
}

type redirectOutput struct {
	Status    int
	Location  string   `header:"Location"`
	SetCookie []string `header:"Set-Cookie"`
}

type oidcCallbackInput struct {
	State  string `query:"state"`
	Code   string `query:"code"`
	Error  string `query:"error"`
	Cookie string `cookie:"notesteme_oidc"`
}

type authMethodsOutput struct {
	Body struct {
		OIDC struct {
			Enabled bool   `json:"enabled"`
			Host    string `json:"host,omitempty" doc:"issuer 的主機名稱（按鈕文字「用 <主機名稱> 登入」）"`
		} `json:"oidc"`
		// InsecureNoAuth 是 serve 帶了 --insecure-no-auth：UI 在頂部持續顯示警告（AUTH-R3）。
		InsecureNoAuth bool `json:"insecure_no_auth" doc:"以 --insecure-no-auth 啟動、沒有任何登入驗證時是 true；UI 在頂部持續顯示警告（AUTH-R3）"`
	}
}

func errOIDCNotFound() error {
	return newError(http.StatusNotFound, CodeNotFound, "找不到這個路徑。")
}

func (rt *routes) oidcStart(ctx context.Context, in *oidcStartInput) (*redirectOutput, error) {
	if rt.d.Auth == nil || !rt.d.Auth.OIDCEnabled() {
		return nil, errOIDCNotFound()
	}
	start, err := rt.d.Auth.StartOIDC(ctx, in.Next)
	switch {
	case errors.Is(err, auth.ErrOIDCBusy):
		return nil, newError(http.StatusTooManyRequests, CodeOIDCFailed, err.Error())
	case err != nil:
		rt.d.Log.WarnContext(ctx, "OIDC 登入無法開始", "reason", auth.OIDCFailureReason(err), "error_kind", errKind(err))
		return nil, newError(http.StatusBadGateway, CodeOIDCFailed, "無法連線到身分提供者，請稍後再試。")
	}
	info := infoFrom(ctx)
	c := &http.Cookie{
		Name: OIDCCookie, Value: start.Cookie, Path: oidcCookiePath,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: info.Secure,
		MaxAge: int(auth.OIDCLoginTTL / time.Second),
	}
	return &redirectOutput{Status: http.StatusFound, Location: start.URL, SetCookie: []string{c.String()}}, nil
}

func (rt *routes) oidcCallback(ctx context.Context, in *oidcCallbackInput) (*redirectOutput, error) {
	if rt.d.Auth == nil || !rt.d.Auth.OIDCEnabled() {
		return nil, errOIDCNotFound()
	}
	info := infoFrom(ctx)
	cred, next, err := rt.d.Auth.FinishOIDC(ctx, auth.OIDCCallback{
		State: in.State, Cookie: in.Cookie, Code: in.Code, IdPError: in.Error, IP: info.IP, UA: info.UserAgent,
	})
	var denied *auth.OIDCDeniedError
	var failed *auth.OIDCFailedError
	switch {
	case errors.Is(err, auth.ErrOIDCState):
		rt.d.Log.WarnContext(ctx, "OIDC callback 的 state 無效")
		return nil, newError(http.StatusBadRequest, CodeOIDCStateInvalid, err.Error())
	case errors.As(err, &denied):
		rt.d.Log.WarnContext(ctx, "身分提供者拒絕了 OIDC 登入", "error_code", denied.Code)
		return nil, newError(http.StatusUnauthorized, CodeOIDCFailed, denied.Error())
	case errors.As(err, &failed):
		// 只記原因（例如「簽章不正確」「nonce 不符」），不記 code、token 與 client secret（AUTH-R18）。
		rt.d.Log.WarnContext(ctx, "OIDC 登入失敗", "reason", auth.OIDCFailureReason(err))
		return nil, newError(http.StatusUnauthorized, CodeOIDCFailed, failed.Error())
	case errors.Is(err, auth.ErrOIDCNotAllowed):
		return nil, newError(http.StatusForbidden, CodeOIDCNotAllowed, err.Error())
	case errors.Is(err, auth.ErrOIDCDisabled):
		return nil, errOIDCNotFound()
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "OIDC 登入時發生錯誤", "error_kind", errKind(err))
		return nil, asError(err)
	}
	now := rt.d.Clock.Now()
	clear := &http.Cookie{
		Name: OIDCCookie, Value: "", Path: oidcCookiePath, MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: info.Secure,
	}
	return &redirectOutput{
		Status:   http.StatusFound,
		Location: localRedirect(next),
		SetCookie: []string{
			sessionCookie(cred.Token, info.Secure, cred.ExpiresAt, now).String(),
			csrfCookie(cred.CSRF, info.Secure, cred.ExpiresAt, now).String(),
			clear.String(),
		},
	}, nil
}

// localRedirect 確保 Location 是站內的相對路徑（next 已經過 SafeNext，這裡再擋一次，不經過 path.Clean）。
func localRedirect(next string) string {
	if auth.SafeNext(next) != next {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return next
}

// errKind 只回傳錯誤的種類，不回傳內容（內容可能夾帶 IdP 的回應片段）。
func errKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func (rt *routes) authMethods(ctx context.Context, _ *struct{}) (*authMethodsOutput, error) {
	out := &authMethodsOutput{}
	out.Body.InsecureNoAuth = rt.d.InsecureNoAuth
	if rt.d.Auth != nil && rt.d.Auth.OIDCEnabled() {
		out.Body.OIDC.Enabled = true
		if u, err := url.Parse(rt.d.Auth.OIDCIssuer()); err == nil {
			out.Body.OIDC.Host = u.Hostname()
		}
	}
	return out, nil
}
