package api

import (
	"net/http"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// Error 是所有錯誤回應的格式（與 Hub 共用，internal/apierr）。
//
// TODO: 之後與 plan 第 5.2 節的錯誤類別（retryable、wait、needs_action…）對齊，
// 目前只放 HTTP 層需要的 code。
type Error = apierr.Error

// 錯誤代碼。
const (
	CodeUnauthorized = "unauthorized"
	CodeInvalidPass  = "invalid_password"
	CodeLoginLocked  = "login_locked"
	CodeCSRF         = "csrf_invalid"
	// CodeNoPassword 是還沒有設定登入密碼（--insecure-no-auth 啟動，AUTH-R3）時的登入與修改密碼。
	CodeNoPassword = "no_password"
	// CodeInsecureHost 是無驗證模式下不是以 loopback 主機名稱存取的請求（AUTH-R3，防 DNS rebinding）。
	CodeInsecureHost = "insecure_host"
	// AUTH-R14、R18、R19、R21（M4）
	CodeProxyUserNotAllowed = "proxy_user_not_allowed"
	CodeOIDCStateInvalid    = "oidc_state_invalid"
	CodeOIDCFailed          = "oidc_failed"
	CodeOIDCNotAllowed      = "oidc_not_allowed"
	CodeTokenInactive       = "token_inactive"
	CodeForbidden           = "forbidden"
	CodeTokenExists         = "token_exists"
	CodeNotFound            = apierr.CodeNotFound
	CodeInvalidInput        = apierr.CodeInvalidInput
	CodeNotReady            = "not_ready"
	CodeNotImplemented      = apierr.CodeNotImplemented
	CodeInternal            = apierr.CodeInternal

	// 對外請求被防護擋下（GRD 介面）。
	CodeBlockedAddress    = "blocked_address"
	CodeTooManyRedirects  = "too_many_redirects"
	CodeResponseTooLarge  = "response_too_large"
	CodeUnsupportedScheme = "unsupported_scheme"

	// 本機路徑不在 inbox 或 media_roots 之內（GRD-R7）。
	CodePathOutsideRoots = "path_outside_roots"

	// 影音在垃圾桶中（DEL-R1、R10）：讀取與處理回 404，新增回 409。
	CodeInTrash = "in_trash"

	// 處理方案與 prompt（PRF、PRM）。找不到時用 not_found（404）。
	CodeProfileExists    = "profile_exists"
	CodeCandidateDecided = "term_candidate_decided"
	CodeProfileInUse     = pipeline.CodeProfileInUse
	CodeBuiltinProfile   = "builtin_profile"
	CodePromptExists     = "prompt_exists"
	CodePromptInUse      = pipeline.CodePromptInUse
	CodeBuiltinPrompt    = "builtin_prompt"

	// ASS 樣式（OUT-R6）。找不到時用 not_found（404）。
	CodeStyleExists  = "style_exists"
	CodeBuiltinStyle = "builtin_style"

	// 外部工具失敗（例如 yt-dlp 展開播放清單，ING-R4）；稍後重試或處理環境後可能成功。
	CodeUpstream = "upstream_failed"
)

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func errForbidden(message string) *Error {
	return newError(http.StatusForbidden, CodeForbidden, message)
}

func errForbiddenScope(scope string) *Error {
	e := errForbidden("權限不足：需要 " + scope + " scope。")
	e.RequiredScope = scope
	return e
}

// inTrashError 是影音在垃圾桶中的錯誤（DEL-R1、R10）：讀取與處理它的端點回 404，新增它回 409。
// 內容相同：訊息、動作「還原」與影音 ID。
func inTrashError(status int, itemID string) *Error {
	e := newError(status, CodeInTrash, pipeline.ErrInTrash.Error())
	e.Actions = []string{"還原"}
	e.ItemID = itemID
	return e
}

// defaultMessages 是 Huma 自己產生的錯誤使用的繁中訊息（定義在 apierr，huma.NewError 也在那裡設定）。
var defaultMessages = func() map[int]struct{ code, msg string } {
	m := map[int]struct{ code, msg string }{}
	for k, v := range apierr.DefaultMessages {
		m[k] = struct{ code, msg string }{v.Code, v.Message}
	}
	return m
}()

// writeError 在 Huma 之外（middleware）寫出錯誤回應。
func writeError(w http.ResponseWriter, e *Error) { apierr.Write(w, e) }

// asError 把任意 error 轉成 *Error；不是 *Error 的一律視為內部錯誤（不洩漏細節）。
func asError(err error) *Error { return apierr.As(err) }
