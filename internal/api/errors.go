package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
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
}

// Error 實作 error。
func (e *Error) Error() string { return e.Message }

// GetStatus 實作 huma.StatusError。
func (e *Error) GetStatus() int { return e.Status }

// 錯誤代碼。
const (
	CodeUnauthorized   = "unauthorized"
	CodeInvalidPass    = "invalid_password"
	CodeLoginLocked    = "login_locked"
	CodeCSRF           = "csrf_invalid"
	CodeForbidden      = "forbidden"
	CodeTokenExists    = "token_exists"
	CodeNotFound       = "not_found"
	CodeInvalidInput   = "invalid_request"
	CodeNotReady       = "not_ready"
	CodeNotImplemented = "not_implemented"
	CodeInternal       = "internal_error"

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
	CodeProfileExists  = "profile_exists"
	CodeProfileInUse   = pipeline.CodeProfileInUse
	CodeBuiltinProfile = "builtin_profile"
	CodePromptExists   = "prompt_exists"
	CodePromptInUse    = pipeline.CodePromptInUse
	CodeBuiltinPrompt  = "builtin_prompt"

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
