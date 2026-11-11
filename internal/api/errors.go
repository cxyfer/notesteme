package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Error 是所有錯誤回應的格式：至少有 code（機器可讀）與 message（預設繁體中文，NFR-I18N-2）。
//
// TODO: 之後與 plan 第 5.2 節的錯誤類別（retryable、wait、needs_action…）對齊，
// 目前只放 HTTP 層需要的 code。
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code" doc:"機器可讀的錯誤代碼"`
	Message string `json:"message" doc:"給人看的說明"`
	// RequiredScope 在 token 缺少 scope 時指出需要哪一個（AUTH-R8）。
	RequiredScope string `json:"required_scope,omitempty"`
	// RetryAfterS 在被鎖定時指出剩餘秒數（AUTH-R6）。
	RetryAfterS *int     `json:"retry_after_s,omitempty"`
	Details     []string `json:"details,omitempty"`
}

// Error 實作 error。
func (e *Error) Error() string { return e.Message }

// GetStatus 實作 huma.StatusError。
func (e *Error) GetStatus() int { return e.Status }

// 錯誤代碼。
const (
	CodeUnauthorized   = "unauthorized"
	CodeInvalidPass    = "invalid_password"
	CodeCSRF           = "csrf_invalid"
	CodeForbidden      = "forbidden"
	CodeNotFound       = "not_found"
	CodeInvalidInput   = "invalid_request"
	CodeNotReady       = "not_ready"
	CodeNotImplemented = "not_implemented"
	CodeInternal       = "internal_error"
)

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// defaultMessages 是 Huma 自己產生的錯誤（解析、驗證等）使用的繁中訊息。
var defaultMessages = map[int]struct{ code, msg string }{
	http.StatusBadRequest:            {CodeInvalidInput, "請求格式不正確。"},
	http.StatusNotFound:              {CodeNotFound, "找不到指定的資源。"},
	http.StatusMethodNotAllowed:      {CodeInvalidInput, "不支援這個 HTTP 方法。"},
	http.StatusRequestEntityTooLarge: {CodeInvalidInput, "請求內容太大。"},
	http.StatusUnsupportedMediaType:  {CodeInvalidInput, "不支援這種內容類型。"},
	http.StatusUnprocessableEntity:   {CodeInvalidInput, "請求內容不正確。"},
	http.StatusInternalServerError:   {CodeInternal, "伺服器發生錯誤。"},
}

func init() {
	// Huma 產生的錯誤（請求驗證失敗等）也使用同一種格式。這是 Huma 的全域設定。
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		e := &Error{Status: status, Code: CodeInvalidInput, Message: msg}
		if d, ok := defaultMessages[status]; ok {
			e.Code, e.Message = d.code, d.msg
		}
		for _, err := range errs {
			if err != nil {
				e.Details = append(e.Details, err.Error())
			}
		}
		return e
	}
}

// writeError 在 Huma 之外（middleware）寫出錯誤回應。
func writeError(w http.ResponseWriter, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}

// asError 把任意 error 轉成 *Error；不是 *Error 的一律視為內部錯誤（不洩漏細節）。
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}
