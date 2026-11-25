package api

import (
	"net/http"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
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

	// 產出檔與記錄的 sha256 不符（NFR-REL-2）：已標記損毀，能自動重新產生時已重新處理。
	CodeArtifactCorrupt = "artifact_corrupt"

	// 外部工具失敗（例如 yt-dlp 展開播放清單，ING-R4）；稍後重試或處理環境後可能成功。
	CodeUpstream = "upstream_failed"
)

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// fieldsError 是欄位錯誤的回應（NFR-I18N-2）：msg 是 domain 錯誤的訊息，fields 放進 errors。
// msg 是「第一個欄位的路徑：說明」時改用範本 field_invalid（繁中逐字相同；其他語言路徑不變、說明翻譯），
// 否則照原樣（固定句子與 domain 格式由目錄翻譯）。
func fieldsError(status int, code, msg string, fields []validate.FieldError) *Error {
	var e *Error
	if len(fields) > 0 && msg == fields[0].Path+"："+fields[0].Message {
		e = apierr.Newf(status, code, "field_invalid", fields[0].Path, fields[0].Message)
	} else {
		e = newError(status, code, msg)
	}
	e.Errors = fields
	return e
}

// firstFieldError 是以第一個欄位的「路徑：說明」為訊息的欄位錯誤（handler 自己驗證的欄位）。fields 不可以是空的。
func firstFieldError(status int, code string, fields []validate.FieldError) *Error {
	return fieldsError(status, code, fields[0].Path+"："+fields[0].Message, fields)
}

func errForbidden(message string) *Error {
	return newError(http.StatusForbidden, CodeForbidden, message)
}

func errForbiddenScope(scope string) *Error {
	e := apierr.Newf(http.StatusForbidden, CodeForbidden, "forbidden_scope", scope)
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

// writeError 在 Huma 之外（middleware）寫出錯誤回應；訊息依請求的 Accept-Language 選擇語言（NFR-I18N-2）。
func writeError(w http.ResponseWriter, r *http.Request, e *Error) { apierr.WriteFor(w, r, e) }

// asError 把任意 error 轉成 *Error；不是 *Error 的一律視為內部錯誤（不洩漏細節）。
func asError(err error) *Error { return apierr.As(err) }
