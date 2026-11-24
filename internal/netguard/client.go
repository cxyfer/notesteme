package netguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"time"
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

// schemeTransport 在發出請求之前檢查 scheme：不是 http、https（含沒有 scheme 的相對網址）就回傳
// ErrUnsupportedScheme，與轉址到其他 scheme 的錯誤相同（GRD-R1）。
// 沒有它，http.Client 會對起始網址回一般的 "unsupported protocol scheme" 錯誤，呼叫端無法分類。
//
// 轉址（GRD-R4）：allow_private 只給「起點本身就在 allowlist」的請求使用。起點是公開主機的轉址改走 strict，
// 每一跳以不含 allowlist 的預設規則檢查，外部的 feed、音檔主機不能把 notesteme 導到內網。
// strict 有自己的連線池，不會沿用先前連到 allowlist 位址的閒置連線。allowlist 是空的時 strict 為 nil。
type schemeTransport struct {
	base   *http.Transport
	strict *http.Transport
	allow  Allowlist
}

func (t *schemeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL != nil {
		if s := req.URL.Scheme; s != "http" && s != "https" {
			// RoundTripper 的約定：出錯時也要關閉 request body。
			if req.Body != nil {
				_ = req.Body.Close()
			}
			return nil, fmt.Errorf("%w：%q", ErrUnsupportedScheme, s)
		}
	}
	// req.Response 只在 http.Client 跟隨轉址時有值：沿著它找到最初的請求。
	if t.strict != nil && req.Response != nil {
		origin := req
		for origin.Response != nil && origin.Response.Request != nil {
			origin = origin.Response.Request
		}
		if !t.originAllowlisted(origin.URL.Hostname()) {
			return t.strict.RoundTrip(req)
		}
	}
	return t.base.RoundTrip(req)
}

// originAllowlisted 回報轉址鏈的起點本身是不是在 allowlist：主機名稱列在 allowlist，
// 或起點是 allowlist 網段內的 IP 字面值。不重新解析主機名稱：名稱由外部控制，DNS rebinding 可以讓
// 第二次解析回內網位址。代價：沒有列出、只是解析到 allowlist 網段的主機名稱，它的轉址不使用 allowlist。
func (t *schemeTransport) originAllowlisted(host string) bool {
	if host == "" {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return t.allow.containsIP(ip.Unmap().WithZone(""))
	}
	return t.allow.containsHost(normalizeHost(host))
}

// clone 以 f 調整 base 與 strict 的複本；其他防護不變。
func (t *schemeTransport) clone(f func(*http.Transport)) *schemeTransport {
	c := *t
	c.base = t.base.Clone()
	f(c.base)
	if t.strict != nil {
		c.strict = t.strict.Clone()
		f(c.strict)
	}
	return &c
}

// CloseIdleConnections 讓 http.Client.CloseIdleConnections 能關閉底層的閒置連線。
func (t *schemeTransport) CloseIdleConnections() {
	t.base.CloseIdleConnections()
	if t.strict != nil {
		t.strict.CloseIdleConnections()
	}
}

// WithResponseHeaderTimeout 回傳複製的 Transport，等待回應標頭的逾時改為 d；其他防護（dial 檢查、scheme 檢查）不變。
// 轉錄這類要伺服器處理完才回標頭的請求使用；用完後呼叫複本的 CloseIdleConnections。
func (t *schemeTransport) WithResponseHeaderTimeout(d time.Duration) http.RoundTripper {
	return t.clone(func(tr *http.Transport) { tr.ResponseHeaderTimeout = d })
}

// WithTimeouts 回傳複製的 Transport，等待回應標頭的逾時改為 header、閒置連線 idle 之後關閉；其他防護不變。
// 給長期共用一個連線池的呼叫端（LLM provider）：只在建立時呼叫一次。
func (t *schemeTransport) WithTimeouts(header, idle time.Duration) http.RoundTripper {
	return t.clone(func(tr *http.Transport) {
		tr.ResponseHeaderTimeout = header
		tr.IdleConnTimeout = idle
	})
}

// NewClient 回傳對外 HTTP 的 client：連線經過 Transport（ADR-0014），
// 起始網址與轉址的每一跳都檢查 scheme，最多跟隨 MaxRedirects 次。逾時與大小由呼叫端自己限制。
func NewClient(d Deps) *http.Client {
	st := &schemeTransport{base: Transport(d), allow: d.mergedAllowlist()}
	if len(st.allow.Prefixes) > 0 || len(st.allow.Hosts) > 0 {
		st.strict = Transport(Deps{Resolver: d.Resolver, Dialer: d.Dialer, RootCAs: d.RootCAs})
	}
	return &http.Client{
		Transport: st,
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

// PolicyMessage 把防護造成的錯誤對應成給使用者看的訊息（GRD-R1、R2、R4、R5）；不是防護的錯誤回傳 false。
func PolicyMessage(err error) (string, bool) {
	var tl *TooLargeError
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrBlockedAddress):
		return "這個網址指向內部網路，已封鎖。", true
	case errors.Is(err, ErrTooManyRedirects):
		return fmt.Sprintf("轉址超過 %d 次。", MaxRedirects), true
	case errors.Is(err, ErrUnsupportedScheme):
		return "只支援 http 與 https 網址。", true
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
