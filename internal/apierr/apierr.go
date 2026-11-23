// Package apierr 是 Client（internal/api）與 Hub（internal/hub）共用的錯誤回應格式（ADR-0032 第 1 點）。
//
// Huma 的錯誤建構函式（huma.NewError）是全域設定：兩邊在同一個程序內（驗收 harness）時只能有一份，
// 所以型別與設定都放在這裡，由 internal/api 以 type alias 使用。這個套件不能 import 任何 domain 套件，
// Hub 才不會間接帶進 pipeline（ADR-0030 第 1 點）。
package apierr

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/validate"
)

// Error 是所有錯誤回應的格式：至少有 code（機器可讀）與 message（預設繁體中文，NFR-I18N-2）。
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code" doc:"機器可讀的錯誤代碼"`
	Message string `json:"message" doc:"給人看的說明"`
	// RequiredScope 在 Client 的 token 缺少 scope 時指出需要哪一個（AUTH-R8）。
	RequiredScope string `json:"required_scope,omitempty"`
	// RequiredPermission 在 Hub 的身分缺少權限時指出需要哪一個（HACC-R13）；Client 不使用。
	RequiredPermission string `json:"required_permission,omitempty"`
	// RetryAfterS 在被鎖定或限流時指出剩餘秒數（AUTH-R6、HACC-R18）。
	RetryAfterS *int `json:"retry_after_s,omitempty"`
	// Failed 在 /readyz 回報 503 時列出失敗的項目（OPS-R5）。
	Failed  []string `json:"failed,omitempty"`
	Details []string `json:"details,omitempty"`
	// Actions 是可以執行的修正動作，例如影音在垃圾桶中時的「還原」（DEL-R1、R10）。
	Actions []string `json:"actions,omitempty"`
	// ItemID 指出錯誤是哪部影音（in_trash 時，client 用它還原）。
	ItemID string `json:"item_id,omitempty"`
	// Purged 在清空垃圾桶部分失敗時指出已經永久刪除幾部（DEL-R4）；用指標，0 也要輸出。
	Purged *int `json:"purged,omitempty"`
	// Errors 是 422 的逐欄位錯誤，依固定順序排列（PRF-R4、PRM-R1）；只增不改，沒有它的 422 與 M1 相同。
	Errors []validate.FieldError `json:"errors,omitempty"`
	// UsedBy 在刪除被引用的處理方案或 prompt 時列出引用它的對象（409，PRM-R8）。
	UsedBy []string `json:"used_by,omitempty"`
	// Line 在匯入的字幕有問題時指出是第幾句（OUT-R15，從 1 起算，不是檔案的行號）；FileLine 是檔案中的行號，不一定知道。
	Line     *int `json:"line,omitempty"`
	FileLine *int `json:"file_line,omitempty"`
	// UndefinedVariables 在 prompt 內文用到未定義的變數時列出它們（422，PRM-R3）。
	UndefinedVariables []string `json:"undefined_variables,omitempty"`
	// SubscriptionID 在重複訂閱時指出既有的訂閱（409，SUB-R1）。
	SubscriptionID string `json:"subscription_id,omitempty"`
}

// Error 實作 error。
func (e *Error) Error() string { return e.Message }

// GetStatus 實作 huma.StatusError。
func (e *Error) GetStatus() int { return e.Status }

// 兩邊共用的錯誤代碼。
const (
	CodeInvalidInput   = "invalid_request"
	CodeNotFound       = "not_found"
	CodeInternal       = "internal_error"
	CodeNotImplemented = "not_implemented"
)

// Default 是某個 HTTP 狀態碼的預設代碼與訊息。
type Default struct{ Code, Message string }

// DefaultMessages 是 Huma 自己產生的錯誤（解析、驗證等）使用的繁中訊息。
var DefaultMessages = map[int]Default{
	http.StatusBadRequest:            {CodeInvalidInput, "請求格式不正確。"},
	http.StatusNotFound:              {CodeNotFound, "找不到指定的資源。"},
	http.StatusMethodNotAllowed:      {CodeInvalidInput, "不支援這個 HTTP 方法。"},
	http.StatusRequestEntityTooLarge: {CodeInvalidInput, "請求內容太大。"},
	http.StatusUnsupportedMediaType:  {CodeInvalidInput, "不支援這種內容類型。"},
	http.StatusUnprocessableEntity:   {CodeInvalidInput, "請求內容不正確。"},
	http.StatusInternalServerError:   {CodeInternal, "伺服器發生錯誤。"},
}

// New 建立錯誤。
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// NotImplemented 是還沒有實作的路由的 501（M2 S0、M3-S0、M5-S0 的 stub）。
func NotImplemented() *Error {
	return New(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
}

func init() {
	// Huma 產生的錯誤（請求驗證失敗等）也使用同一種格式。這是 Huma 的全域設定，只能在這裡設定一次。
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		e := &Error{Status: status, Code: CodeInvalidInput, Message: msg}
		if d, ok := DefaultMessages[status]; ok {
			e.Code, e.Message = d.Code, d.Message
		}
		for _, err := range errs {
			if err != nil {
				e.Details = append(e.Details, err.Error())
			}
		}
		return e
	}
}

// Write 在 Huma 之外（middleware、一般 handler）寫出錯誤回應。
func Write(w http.ResponseWriter, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(e)
}

// As 把任意 error 轉成 *Error；不是 *Error 的一律視為內部錯誤（不洩漏細節）。
func As(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return New(http.StatusInternalServerError, CodeInternal, DefaultMessages[http.StatusInternalServerError].Message)
}
