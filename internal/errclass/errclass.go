// Package errclass 定義外部來源與工具錯誤的分類（plan 第 5.2 節、ADR-0009）：
// 分類決定步驟的狀態與系統的行為（重試、等待、停下來等使用者處理、失敗）。
package errclass

import (
	"errors"
	"fmt"
	"time"
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

// 來源中立的哨兵錯誤：來源套件用 %w 包裝它們，呼叫端（pipeline）不必認得每個來源自己的錯誤。
var (
	// ErrNoResult 表示來源沒有這部影片的結果（Supadata 的 206、Groq 的 segments 為空）；呼叫端改用下一個來源。
	ErrNoResult = errors.New("來源沒有結果")
	// ErrNotConfigured 表示來源沒有設定（缺 API key），沒有發出請求；呼叫端記為 not_configured 並略過。
	ErrNotConfigured = errors.New("來源沒有設定")
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
	// ResumeOn 是步驟 blocked 時機器可讀的恢復條件，格式為 cookie:<platform>（例如 cookie:bilibili、
	// cookie:youtube）；使用者補上該平台的 cookie 後，系統依它找出要恢復的步驟。空字串表示沒有自動恢復的條件。
	// 只有 needs_action 這類會停下來的錯誤才有意義。
	ResumeOn string
	// RetryAfter 是來源指定的重試等待時間（HTTP Retry-After，LLM-R6），或 wait 類錯誤下一次檢查前的等待時間（LLM-R7 限流）；
	// 0 代表沒有指定，由呼叫端用預設的退避。
	RetryAfter time.Duration
	Err        error
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
