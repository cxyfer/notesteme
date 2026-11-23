package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// 逾時與大小（NOTIFY-R7）。連線逾時 10 秒由 netguard 的 dial 負責。
const (
	requestTimeout  = 15 * time.Second
	maxResponseRead = 64 << 10
	// maxRetryAfter 是 Retry-After 採用的上限（NOTIFY-R8）。
	maxRetryAfter = 6 * time.Hour
)

// 固定格式的訊息（NOTIFY-R7、R8；log、last_error 與 API 只用這些，不帶原始錯誤，因為網址含 token）。
const (
	MsgBlocked    = "這個網址指向內部網路，已封鎖。"
	MsgRedirect   = "通知端點回應了轉址，不跟隨。"
	MsgConnect    = "連線失敗。"
	MsgTimeout    = "連線逾時。"
	MsgScheme     = "只支援 http 與 https 網址。"
	MsgSecretGone = "notifier 的密鑰已被重設，請重新設定。"
	MsgTooLarge   = "通知內容超過 64 KiB。"
)

// NewClient 回傳通知用的 HTTP client：連線經過 netguard（GRD-R1～R5），不跟隨轉址（3xx 原樣交回），整體逾時 15 秒。
// 每個請求都不重用連線（見 send），DNS rebinding 才會在下一次連線時重新檢查。
func NewClient(d netguard.Deps) *http.Client {
	c := netguard.NewClient(d)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Timeout = requestTimeout
	return c
}

// Outcome 是一次送出的結果。
type Outcome struct {
	OK     bool
	Status int // HTTP 狀態碼；沒有回應時是 0
	Class  errclass.Class
	// Message 是固定格式的訊息（不含網址與回應內容）。
	Message string
	// RetryAfter 是端點要求的等待時間（429 的 Retry-After 或 Telegram 的 parameters.retry_after），沒有時是 0。
	RetryAfter time.Duration
}

// request 是要送出的請求；URL 含密鑰（Telegram 的 bot token、Discord 的 webhook token），不可以記錄。
type request struct {
	url     string
	body    []byte
	headers map[string]string
}

// endpointURL 是種類對應的送出網址（NOTIFY-R2）。
func endpointURL(kind string, cfg Config) string {
	switch kind {
	case KindNtfy:
		return cfg["server"] + "/"
	case KindDiscord:
		u := cfg["webhook_url"]
		if strings.Contains(u, "?") {
			return u + "&wait=true"
		}
		return u + "?wait=true"
	case KindTelegram:
		base := cfg["api_base"]
		if base == "" {
			base = DefaultTelegramAPIBase
		}
		return base + "/bot" + cfg["bot_token"] + "/sendMessage"
	}
	return cfg["url"]
}

// endpointHost 是可以記錄的主機名稱（NOTIFY-R13）。
func endpointHost(kind string, cfg Config) string {
	u, err := url.Parse(endpointURL(kind, cfg))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Sign 計算 webhook 的簽章（NOTIFY-R5）：sha256= 加上 hex(HMAC-SHA256(secret, timestamp + "." + body))。
func Sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// buildRequest 組出一次嘗試的請求；webhook 每次嘗試以 now 重新簽章，body 不變（NOTIFY-R5）。
func buildRequest(kind string, cfg Config, body []byte, event, deliveryID, version string, now time.Time) request {
	h := map[string]string{"Content-Type": "application/json", "User-Agent": "Notesteme/" + version}
	switch kind {
	case KindNtfy:
		if t := cfg["token"]; t != "" {
			h["Authorization"] = "Bearer " + t
		}
	case KindWebhook:
		ts := now.Unix()
		h["X-Notesteme-Event"] = event
		h["X-Notesteme-Delivery"] = deliveryID
		h["X-Notesteme-Timestamp"] = strconv.FormatInt(ts, 10)
		h["X-Notesteme-Signature"] = Sign(cfg["secret"], ts, body)
	}
	return request{url: endpointURL(kind, cfg), body: body, headers: h}
}

// send 送出一個請求並分類結果（NOTIFY-R7、R8）。錯誤只轉成固定訊息，不包含網址。
func send(ctx context.Context, c *http.Client, kind string, rq request, now time.Time) Outcome {
	if len(rq.body) > MaxRequestBody {
		return Outcome{Class: errclass.ClassPermanent, Message: MsgTooLarge}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rq.url, bytes.NewReader(rq.body))
	if err != nil {
		return Outcome{Class: errclass.ClassPermanent, Message: MsgConnect}
	}
	for k, v := range rq.headers {
		req.Header.Set(k, v)
	}
	req.Close = true // 每次都重新連線與解析（DNS rebinding 以實際連線的 IP 判斷）
	resp, err := c.Do(req)
	if err != nil {
		return classifyError(err)
	}
	defer resp.Body.Close()
	head, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseRead))
	return classifyResponse(kind, resp.StatusCode, resp.Header.Get("Retry-After"), head, now)
}

// classifyError 把連線層的錯誤轉成固定訊息。
func classifyError(err error) Outcome {
	switch {
	case errors.Is(err, netguard.ErrBlockedAddress):
		return Outcome{Class: errclass.ClassPermanent, Message: MsgBlocked}
	case errors.Is(err, netguard.ErrUnsupportedScheme):
		return Outcome{Class: errclass.ClassPermanent, Message: MsgScheme}
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return Outcome{Class: errclass.ClassRetryable, Message: MsgTimeout}
	}
	return Outcome{Class: errclass.ClassRetryable, Message: MsgConnect}
}

// classifyResponse 依狀態碼分類（NOTIFY-R8）：2xx 成功；408、429、5xx 可重試；3xx 與其他 4xx 是 permanent。
func classifyResponse(kind string, status int, retryAfter string, body []byte, now time.Time) Outcome {
	o := Outcome{Status: status}
	switch {
	case status >= 200 && status < 300:
		o.OK = true
		return o
	case status >= 300 && status < 400:
		o.Class, o.Message = errclass.ClassPermanent, MsgRedirect
		return o
	}
	o.Message = fmt.Sprintf("通知端點回應 HTTP %d。", status)
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
		o.Class = errclass.ClassRetryable
		if status == http.StatusTooManyRequests {
			o.RetryAfter = ParseRetryAfter(retryAfter, now)
			if kind == KindTelegram {
				if d := telegramRetryAfter(body); d > o.RetryAfter {
					o.RetryAfter = d
				}
			}
		}
		return o
	}
	o.Class = errclass.ClassPermanent
	return o
}

// ParseRetryAfter 解析 Retry-After（秒數或 HTTP 日期），上限 6 小時；無法解析或已過時回傳 0。
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n <= 0 {
			return 0
		}
		if n > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	if d <= 0 {
		return 0
	}
	return min(d, maxRetryAfter)
}

// telegramRetryAfter 解析 Telegram 429 的 {"parameters": {"retry_after": 秒數}}，上限 6 小時。
func telegramRetryAfter(body []byte) time.Duration {
	var v struct {
		Parameters struct {
			RetryAfter float64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &v) != nil || v.Parameters.RetryAfter <= 0 {
		return 0
	}
	if v.Parameters.RetryAfter > maxRetryAfter.Seconds() {
		return maxRetryAfter
	}
	return time.Duration(v.Parameters.RetryAfter * float64(time.Second))
}
