package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// 結束碼（CLI-R6）。
const (
	exitOK      = 0 // 成功
	exitError   = 1 // 執行錯誤
	exitUsage   = 2 // 參數錯誤
	exitBlocked = 3 // 需要使用者處理（blocked）
	exitWaiting = 4 // 等待中（waiting、pending，或 --wait 超過 --timeout）
)

// errNotImplemented 是 API 與 CLI 共用的「功能還沒做」錯誤代碼（見 api.CodeNotImplemented）。
const errNotImplemented = "not_implemented"

// usageError 是參數錯誤：不認得的子命令或旗標、缺少位置參數、列舉值錯誤等，結束碼 2。
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// apiError 是 API 回傳的錯誤（非 2xx）。
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return e.Message
}

// notImplementedf 建立「還沒實作」的錯誤：結束碼 1，ErrorCode 是 not_implemented（與 API 的 501 一致）。
func notImplementedf(format string, a ...any) error {
	return &apiError{Status: http.StatusNotImplemented, Code: errNotImplemented, Message: fmt.Sprintf(format, a...)}
}

// errorFromResponse 把非 2xx 的回應轉成 apiError；內容不是 API 的錯誤格式時，用 HTTP 狀態代替。
func errorFromResponse(status int, body []byte) *apiError {
	e := &apiError{Status: status}
	var parsed struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		e.Code, e.Message = parsed.Code, parsed.Message
	}
	if e.Message == "" {
		e.Message = fmt.Sprintf("HTTP %d %s", status, strings.TrimSpace(snippet(body)))
	}
	return e
}

// snippet 截短回應內容，放進錯誤訊息。
func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// exitFor 把錯誤對應到結束碼與 API 的錯誤代碼（CLI-R6）：
// 參數錯誤與 API 的 400、422 是 2；其他錯誤（401、403、404、5xx、501、連線失敗）是 1。
func exitFor(err error) (code int, errorCode string) {
	var ue *usageError
	var ae *apiError
	var ir *importRejected
	switch {
	case errors.As(err, &ue):
		return exitUsage, ""
	case errors.As(err, &ir):
		// config import 的內容不正確：檔案內容不是命令列參數，結束碼是 1（SEC-R10）。
		return exitError, errCodeConfigInvalid
	case errors.As(err, &ae):
		if ae.Status == http.StatusBadRequest || ae.Status == http.StatusUnprocessableEntity {
			return exitUsage, ae.Code
		}
		return exitError, ae.Code
	}
	return exitError, ""
}
