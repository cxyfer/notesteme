package netguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MaxRedirects 是對外請求最多跟隨的轉址次數（GRD-R4）。
const MaxRedirects = 5

// 回應大小的上限（GRD-R5）。
const (
	// LimitFeed 是 RSS 與 transcript 的上限。
	LimitFeed int64 = 10 << 20
	// LimitAudio 是音檔的上限。
	LimitAudio int64 = 2 << 30
)

var (
	// ErrTooManyRedirects 表示轉址超過 MaxRedirects 次；用 errors.Is 判斷。
	ErrTooManyRedirects = errors.New("netguard: 轉址次數過多")
	// ErrTooLarge 表示回應超過大小上限；用 errors.Is 判斷，errors.As 到 *TooLargeError 可取得上限。
	ErrTooLarge = errors.New("netguard: 回應超過大小上限")
	// ErrUnsupportedScheme 表示轉址到 http、https 以外的 scheme（GRD-R1）。
	ErrUnsupportedScheme = errors.New("netguard: 只接受 http 與 https")
)

// TooLargeError 是帶有上限的 ErrTooLarge。
type TooLargeError struct{ Limit int64 }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%s（%s）", ErrTooLarge.Error(), formatLimit(e.Limit))
}

// Is 讓 errors.Is(err, ErrTooLarge) 成立。
func (e *TooLargeError) Is(target error) bool { return target == ErrTooLarge }

// NewClient 回傳對外 HTTP 的 client：連線經過 Transport（ADR-0014），
// 轉址的每一跳都檢查 scheme，最多跟隨 MaxRedirects 次。逾時與大小由呼叫端自己限制。
func NewClient(d Deps) *http.Client {
	return &http.Client{
		Transport: Transport(d),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// via 含最初的請求，所以 len(via) 是「已經跟隨的轉址次數 + 1」。
			if len(via) > MaxRedirects {
				return ErrTooManyRedirects
			}
			if s := req.URL.Scheme; s != "http" && s != "https" {
				return fmt.Errorf("%w：%q", ErrUnsupportedScheme, s)
			}
			return nil
		},
	}
}

// LimitBody 回傳限制 resp.Body 大小的 reader：超過 max 時讀取回傳 *TooLargeError，不截斷。
// Content-Length 已經超過 max 時，第一次讀取就失敗，不下載任何內容。
// 內部多讀 1 個 byte，才能分辨「剛好到上限」與「超過上限」。
func LimitBody(resp *http.Response, max int64) io.Reader {
	l := &limitedBody{r: resp.Body, max: max}
	if resp.ContentLength > max {
		l.err = &TooLargeError{Limit: max}
	}
	return l
}

type limitedBody struct {
	r   io.Reader
	max int64
	n   int64
	err error
}

func (l *limitedBody) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if allowed := l.max + 1 - l.n; int64(len(p)) > allowed {
		p = p[:allowed]
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		// 多讀到的那 1 個 byte 不交給呼叫端。
		l.err = &TooLargeError{Limit: l.max}
		return n - int(l.n-l.max), l.err
	}
	return n, err
}

// PolicyMessage 把防護造成的錯誤對應成給使用者看的訊息（GRD-R2、R4、R5）；不是防護的錯誤回傳 false。
func PolicyMessage(err error) (string, bool) {
	var tl *TooLargeError
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrBlockedAddress):
		return "這個網址指向內部網路，已封鎖。", true
	case errors.Is(err, ErrTooManyRedirects):
		return fmt.Sprintf("轉址超過 %d 次。", MaxRedirects), true
	case errors.As(err, &tl):
		return fmt.Sprintf("回應超過大小上限（%s）。", formatLimit(tl.Limit)), true
	case errors.Is(err, ErrTooLarge):
		return "回應超過大小上限。", true
	}
	return "", false
}

// formatLimit 以 GiB、MiB、KiB 顯示上限；不是整數倍時顯示 bytes。
func formatLimit(n int64) string {
	switch {
	case n > 0 && n%(1<<30) == 0:
		return fmt.Sprintf("%d GiB", n>>30)
	case n > 0 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n > 0 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
