package pipeline

import (
	"errors"
	"fmt"
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
