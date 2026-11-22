package pipeline

import (
	"errors"
	"fmt"

	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 使用者看得到的錯誤（API 對應成 4xx／501）。
var (
	// ErrUnrecognizedURL：ING-R1。
	ErrUnrecognizedURL = errors.New("無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。")
	// ErrChannel：ING-R5。
	ErrChannel = errors.New("這是頻道。要追蹤新內容，請建立訂閱。")

	// 處理方案（PRF）與 prompt（PRM）。API 對應成 404 not_found 或 409。
	ErrProfileNotFound       = errors.New("找不到這個處理方案。")
	ErrProfileExists         = errors.New("已經有同名的處理方案。")
	ErrBuiltinProfile        = errors.New("內建的處理方案不能刪除。")
	ErrPromptNotFound        = errors.New("找不到這個 prompt。")
	ErrPromptVersionNotFound = errors.New("找不到這個 prompt 的指定版本。")
)

// InvalidError 是請求內容不正確（422）。
// Fields 不是空的時，API 把它放進回應的 errors（路徑依固定順序，D9）；Msg 是第一個錯誤的說明。
type InvalidError struct {
	Msg    string
	Fields []validate.FieldError
}

func (e *InvalidError) Error() string { return e.Msg }

// invalidFields 由欄位錯誤組出 *InvalidError；Msg 是第一個錯誤的「路徑：說明」。fs 不可以是空的。
func invalidFields(fs []validate.FieldError) *InvalidError {
	msg := "請求內容不正確。"
	if len(fs) > 0 {
		msg = fs[0].Path + "：" + fs[0].Message
	}
	return &InvalidError{Msg: msg, Fields: fs}
}

// 資源被別處引用而不能刪除時的 InUseError.Code（API 回 409，錯誤碼就是 Code）。
const (
	CodeProfileInUse = "profile_in_use"
	CodePromptInUse  = "prompt_in_use"
)

// InUseError 表示資源還被引用，不能刪除（409）。UsedBy 列出引用它的對象（處理方案名稱、"default_profile" 等）。
type InUseError struct {
	Code   string
	UsedBy []string
}

func (e *InUseError) Error() string {
	switch e.Code {
	case CodePromptInUse:
		return "這個 prompt 還在使用中，不能刪除。"
	case CodeProfileInUse:
		return "這個處理方案還在使用中，不能刪除。"
	}
	return "這個項目還在使用中，不能刪除。"
}

func invalidf(format string, a ...any) *InvalidError {
	return &InvalidError{Msg: fmt.Sprintf(format, a...)}
}

// NotImplementedError 表示功能屬於之後的切片（501）。
type NotImplementedError struct{ What string }

func (e *NotImplementedError) Error() string { return "這個功能尚未實作：" + e.What + "。" }

// UpstreamError 表示外部工具或服務失敗，使用者稍後重試或處理環境之後可能成功（502）。
// Msg 是給使用者看的訊息；Err 是原始錯誤（只寫進 log，不回傳給 client）。
type UpstreamError struct {
	Msg string
	Err error
}

func (e *UpstreamError) Error() string { return e.Msg }
func (e *UpstreamError) Unwrap() error { return e.Err }

// PolicyError 是對外請求被防護擋下（GRD-R1、R2、R4、R5）：Msg 是給使用者看的訊息（netguard.PolicyMessage），
// Err 是原始錯誤，用 errors.Is 對 netguard.ErrBlockedAddress、ErrTooManyRedirects、ErrTooLarge、ErrUnsupportedScheme 判斷種類。
type PolicyError struct {
	Msg string
	Err error
}

func (e *PolicyError) Error() string { return e.Msg }
func (e *PolicyError) Unwrap() error { return e.Err }

// asPolicyError 在 err 是防護造成的錯誤時回傳 *PolicyError，否則回傳 nil。
func asPolicyError(err error) *PolicyError {
	if msg, ok := netguard.PolicyMessage(err); ok {
		return &PolicyError{Msg: msg, Err: err}
	}
	return nil
}
