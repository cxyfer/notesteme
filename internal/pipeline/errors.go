package pipeline

import (
	"errors"
	"fmt"

	"github.com/cxyfer/notesteme/internal/netguard"
)

// 使用者看得到的錯誤（API 對應成 4xx／501）。
var (
	// ErrUnrecognizedURL：ING-R1。
	ErrUnrecognizedURL = errors.New("無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。")
	// ErrChannel：ING-R5。
	ErrChannel = errors.New("這是頻道。要追蹤新內容，請建立訂閱。")
)

// InvalidError 是請求內容不正確（422）。
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalidf(format string, a ...any) *InvalidError {
	return &InvalidError{Msg: fmt.Sprintf(format, a...)}
}

// NotImplementedError 表示功能屬於之後的切片（501）。
type NotImplementedError struct{ What string }

func (e *NotImplementedError) Error() string { return "這個功能尚未實作：" + e.What + "。" }

// PolicyError 是對外請求被防護擋下（GRD-R2、R4、R5）：Msg 是給使用者看的訊息（netguard.PolicyMessage），
// Err 是原始錯誤，用 errors.Is 對 netguard.ErrBlockedAddress、ErrTooManyRedirects、ErrTooLarge 判斷種類。
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
