// Package errclass 定義外部來源與工具錯誤的分類（plan 第 5.2 節、ADR-0009）：
// 分類決定步驟的狀態與系統的行為（重試、等待、停下來等使用者處理、失敗）。
package errclass

import (
	"errors"
	"fmt"
)

// Class 是錯誤類別。
type Class string

const (
	// ClassRetryable：網路逾時、429、5xx；指數退避重試。
	ClassRetryable Class = "retryable"
	// ClassWait：自動字幕還沒生成、直播還沒結束；設定 not_before 再檢查。
	ClassWait Class = "wait"
	// ClassNeedsAction：cookie 過期、會員限定、缺 API key；停下來等使用者處理。
	ClassNeedsAction Class = "needs_action"
	// ClassBudgetBlocked：超過預算。
	ClassBudgetBlocked Class = "budget_blocked"
	// ClassPermanent：影片已刪除等，重試也不會成功。
	ClassPermanent Class = "permanent"
)

// Error 是外部來源（yt-dlp 等）回報的錯誤，附上分類。Source 是來源或工具的名稱。
type Error struct {
	Class    Class
	Source   string
	ExitCode int
	// Detail 是工具輸出的摘要，只供 log 與診斷。
	Detail string
	// Message 與 Actions 是給使用者看的訊息與修正動作（SRC-R9）；只有 needs_action 這類
	// 使用者要處理的錯誤會填。空的 Message 表示沒有具體說明，由呼叫端用通用訊息。
	Message string
	Actions []string
	Err     error
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s 失敗（%s", e.Source, e.Class)
	if e.ExitCode != 0 {
		msg += fmt.Sprintf("，結束碼 %d", e.ExitCode)
	}
	msg += "）"
	if e.Detail != "" {
		msg += "：" + e.Detail
	}
	if e.Err != nil {
		msg += "：" + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// ClassOf 回傳 err 的錯誤類別；不是 *Error 時視為 retryable 以外的未知錯誤，ok 為 false。
func ClassOf(err error) (Class, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Class, true
	}
	return "", false
}
