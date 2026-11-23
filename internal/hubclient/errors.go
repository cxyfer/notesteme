package hubclient

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind 是 Hub 錯誤對應到 Client 的種類（ADR-0032 第 5 點、HSHR-R8）。
type Kind int

const (
	// KindUnavailable：連線失敗、逾時、5xx 與其他非預期的狀態碼（retryable）。
	KindUnavailable Kind = iota + 1
	// KindUnauthorized：401（needs_action）。
	KindUnauthorized
	// KindForbidden：403（needs_action）。
	KindForbidden
	// KindRateLimited：429（retryable）。
	KindRateLimited
	// KindNotFound：404、410（permanent）。
	KindNotFound
	// KindInvalid：Hub 的 422，原樣轉出（permanent）。
	KindInvalid
	// KindIntegrity：內容的 sha256 與版本不符（permanent，HSHR-R10）。
	KindIntegrity
	// KindContentInvalid：內容不是合法的字幕（HSHR-R10）。
	KindContentInvalid
)

// 錯誤代碼（Client 的 API 回應用）。
const (
	CodeUnavailable     = "hub_unavailable"
	CodeUnauthorized    = "hub_unauthorized"
	CodeForbidden       = "hub_forbidden"
	CodeRateLimited     = "hub_rate_limited"
	CodeNotFound        = "hub_version_not_found"
	CodeIntegrity       = "hub_integrity"
	CodeContentInvalid  = "hub_content_invalid"
	CodeNotConfigured   = "hub_not_configured"
	defaultRetryAfterS  = 60
	maxRetryAfterS      = 86400
	maxHubMessageRunes  = 300
	maxHubFieldErrors   = 20
	reasonTimeout       = "連線逾時"
	reasonConnectFailed = "連線失敗"
)

// MsgNotConfigured 是沒有 hub.url 或 token 時的訊息（HSHR-R1）。
const MsgNotConfigured = "尚未設定 Hub。請在設定檔加入 hub.url，並在設定頁填入 Hub token。"

// ActionConfigure 是沒有設定 Hub 時的修正動作。
const ActionConfigure = "設定 Hub"

// ErrNotConfigured 表示沒有 hub.url 或沒有 token。
var ErrNotConfigured = errors.New(MsgNotConfigured)

// permissions 是 Hub 的權限名稱（HACC）；required_permission 只接受這些，避免回顯 Hub 的任意字串。
var permissions = []string{"read", "download", "regenerate", "upload", "moderate", "admin"}

// FieldError 是 Hub 422 的逐欄位錯誤。
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Error 是呼叫 Hub 失敗的錯誤。訊息一律由 Client 產生（422 以外），不含 token 與 Hub 的原文。
type Error struct {
	Kind Kind
	// Status 是 Hub 的 HTTP 狀態碼；連線失敗時為 0。
	Status int
	// Reason 是 hub_unavailable 的原因：「HTTP 503」、「連線逾時」或「連線失敗」。
	Reason string
	// Timeout 表示請求因逾時而取消。
	Timeout bool
	// Permission 是 403 時 Hub 指出缺少的權限（已確認是已知的權限名稱）。
	Permission string
	// RetryAfterS 是 429 時建議等待的秒數。
	RetryAfterS int
	// HubCode、HubMessage、Errors 是 Hub 的 422 內容（已清理）。
	HubCode    string
	HubMessage string
	Errors     []FieldError
	// Detail 是內容驗證失敗的原因（HSHR-R10 的 <原因>）。
	Detail string
}

// Code 回傳 Client 的錯誤代碼。
func (e *Error) Code() string {
	switch e.Kind {
	case KindUnauthorized:
		return CodeUnauthorized
	case KindForbidden:
		return CodeForbidden
	case KindRateLimited:
		return CodeRateLimited
	case KindNotFound:
		return CodeNotFound
	case KindInvalid:
		if e.HubCode != "" {
			return e.HubCode
		}
		return "invalid_request"
	case KindIntegrity:
		return CodeIntegrity
	case KindContentInvalid:
		return CodeContentInvalid
	}
	return CodeUnavailable
}

// HTTPStatus 是 Client 的 API 回應的狀態碼。
func (e *Error) HTTPStatus() int {
	switch e.Kind {
	case KindNotFound:
		return http.StatusNotFound
	case KindInvalid, KindContentInvalid:
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadGateway
}

// Error 回傳給使用者看的訊息（ADR-0032 第 5 點的表）。
func (e *Error) Error() string {
	switch e.Kind {
	case KindUnauthorized:
		return "Hub token 無效或已過期。請在 Hub 建立新的 token，再到設定頁更新。"
	case KindForbidden:
		if e.Permission != "" {
			return "Hub token 沒有「" + e.Permission + "」權限。請在 Hub 建立有這個權限的 token。"
		}
		return "Hub 拒絕了這個請求。請確認 Hub 帳號沒有被停用，以及 token 的權限。"
	case KindRateLimited:
		return fmt.Sprintf("Hub 暫時拒絕請求（太頻繁），請在 %d 秒後再試。", e.RetryAfterS)
	case KindNotFound:
		return "Hub 上找不到這個版本，可能已被移除或你沒有權限查看。"
	case KindInvalid:
		if e.HubMessage != "" {
			return e.HubMessage
		}
		return "Hub 拒絕了這個請求。"
	case KindIntegrity:
		return "Hub 回傳的內容與版本的 sha256 不符，已拒絕使用。"
	case KindContentInvalid:
		return "Hub 回傳的字幕格式不正確：" + e.Detail + "。"
	}
	return "無法連線到 Hub（" + e.Reason + "）。請稍後再試。"
}

// PrecheckReason 是 ASR 前查詢失敗時提醒中的原因（HSHR-R15）。
func (e *Error) PrecheckReason() string {
	switch e.Kind {
	case KindUnauthorized, KindForbidden:
		return "token 無效或權限不足"
	case KindIntegrity, KindContentInvalid:
		return "內容驗證失敗"
	case KindRateLimited, KindNotFound, KindInvalid:
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return e.Reason
}

// statusError 依 Hub 的回應建立錯誤；body 是已讀取（有上限）的回應本體。
func statusError(status int, header http.Header, body hubErrorBody) *Error {
	switch {
	case status == http.StatusUnauthorized:
		return &Error{Kind: KindUnauthorized, Status: status}
	case status == http.StatusForbidden:
		e := &Error{Kind: KindForbidden, Status: status}
		if slices.Contains(permissions, body.RequiredPermission) {
			e.Permission = body.RequiredPermission
		}
		return e
	case status == http.StatusTooManyRequests:
		return &Error{Kind: KindRateLimited, Status: status, RetryAfterS: retryAfter(body.RetryAfterS, header.Get("Retry-After"))}
	case status == http.StatusNotFound || status == http.StatusGone:
		return &Error{Kind: KindNotFound, Status: status}
	case status == http.StatusUnprocessableEntity:
		e := &Error{Kind: KindInvalid, Status: status, HubCode: cleanCode(body.Code), HubMessage: cleanText(body.Message, maxHubMessageRunes)}
		for i, fe := range body.Errors {
			if i >= maxHubFieldErrors {
				break
			}
			e.Errors = append(e.Errors, FieldError{Path: cleanText(fe.Path, 100), Message: cleanText(fe.Message, maxHubMessageRunes)})
		}
		return e
	}
	return &Error{Kind: KindUnavailable, Status: status, Reason: fmt.Sprintf("HTTP %d", status)}
}

// retryAfter 取 body 的 retry_after_s 或 Retry-After 標頭；不合理時用預設值。
func retryAfter(body *int, header string) int {
	if body != nil && *body > 0 && *body <= maxRetryAfterS {
		return *body
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(header), "%d", &n); err == nil && n > 0 && n <= maxRetryAfterS {
		return n
	}
	return defaultRetryAfterS
}

// cleanCode 只接受小寫英數字與底線的短代碼。
func cleanCode(s string) string {
	if len(s) == 0 || len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return ""
		}
	}
	return s
}

// cleanText 去掉控制字元、截到 max 個字元（Hub 的文字是不可信輸入）。
func cleanText(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' || unicode.Is(unicode.Bidi_Control, r) {
			continue
		}
		if n >= max {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
