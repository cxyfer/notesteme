package subscription

import (
	"errors"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// 檢查失敗的訊息（SUB-R12，逐字）。%s 是「HTTP 503」或「連線失敗」之類。
const (
	msgRetryHTTP  = "暫時無法檢查訂閱（%s），稍後會自動重試。"
	msgRetryYtDlp = "暫時無法檢查訂閱（yt-dlp 失敗），稍後會自動重試。"
	msgFeedGone   = "訂閱來源已不存在（HTTP %d）。請確認網址，或刪除這個訂閱。"
	msgFeedStatus = "訂閱來源回應了錯誤（HTTP %d）。請確認網址，或刪除這個訂閱。"
	msgListGone   = "找不到這個頻道、播放清單或合集，或它不是公開的。"
	msgForbidden  = "YouTube 拒絕了訂閱的檢查請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。"
	msgNotFeed    = "訂閱來源的內容不是有效的 RSS 或 Atom。"
)

// checkError 是一次檢查的失敗（SUB-R12）：Message 是固定的、給人看的訊息，不含 feed 的內容與 yt-dlp 的輸出，
// 所以可以寫進 last_error 與 log。cause 只給單元測試看，不輸出。
type checkError struct {
	Class   errclass.Class
	Message string
	// throttled 為 true 代表平台回 429／412：該平台的 bucket 清空並暫停（SUB-R10）。
	throttled bool
	cause     error
}

func (e *checkError) Error() string { return e.Message }

// asCheckError 把任意錯誤轉成 *checkError；不認得的一律是 retryable 的「連線失敗」，不帶原始錯誤的文字。
func asCheckError(err error) *checkError {
	var ce *checkError
	if errors.As(err, &ce) {
		return ce
	}
	return &checkError{Class: errclass.ClassRetryable, Message: "暫時無法檢查訂閱（連線失敗），稍後會自動重試。", cause: err}
}
