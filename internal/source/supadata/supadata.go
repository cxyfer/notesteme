// Package supadata 是 Supadata 字幕來源（SRC-R3、ADR-0006）：
// 平台字幕都不符合時，用 GET /v1/transcript 取得 YouTube 影片的字幕。
//
// 連線一律由呼叫端提供的 http.Client 發出（netguard 的 Transport，GRD-R1）；
// API key 只放在請求標頭，不寫進網址、錯誤訊息或 log（NFR-SEC-3）。
package supadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// ID 是來源 ID（steps.md 第 3 節）。
const ID = "supadata"

// DefaultBaseURL 是 Supadata API 的位址。
const DefaultBaseURL = "https://api.supadata.ai"

// Supadata 的取得模式（影響結果，所以屬於快取鍵的 params）。
const (
	// ModeNative 只取平台既有的字幕。
	ModeNative = "native"
	// ModeGenerate 一律由 Supadata 以 AI 產生字幕。
	ModeGenerate = "generate"
	// ModeAuto 先取平台字幕，沒有才產生；是預設值。
	ModeAuto = "auto"
)

// ValidMode 回報 m 是不是 Supadata 認得的模式。
func ValidMode(m string) bool {
	return m == ModeNative || m == ModeGenerate || m == ModeAuto
}

// requestTimeout 是整個請求的逾時；回應大小上限用 netguard.LimitFeed（GRD-R5）。
const requestTimeout = 90 * time.Second

// ErrNoTranscript 表示 Supadata 沒有這部影片的字幕（404 或內容為空）；呼叫端改用下一個來源。
var ErrNoTranscript = fmt.Errorf("supadata：沒有字幕：%w", errclass.ErrNoResult)

// 401、402 都是 needs_action，但要使用者做的事不同，呼叫端用這兩個錯誤分辨。
var (
	// ErrNoKey 表示沒有設定 API key；不會發出請求。
	ErrNoKey = fmt.Errorf("沒有設定 API key：%w", errclass.ErrNotConfigured)
	// ErrKeyRejected 表示 API key 無效（401）。
	ErrKeyRejected = errors.New("API key 無效")
	// ErrPlanUnsupported 表示目前的 Supadata 方案不支援這個功能（402）。
	ErrPlanUnsupported = errors.New("方案不支援這個功能")
)

// Client 是 Supadata 的 HTTP client。
type Client struct {
	// HTTP 是對外 HTTP 的 client（netguard 的 Transport）。
	HTTP *http.Client
	// BaseURL 為空時使用 DefaultBaseURL。
	BaseURL string
	// APIKey 是 Supadata 的 API key（環境變數，SEC-R1 優先）。
	APIKey string
	// KeyFunc 在 APIKey 為空時動態取得 API key（例如由資料庫解密讀取）。
	KeyFunc func(ctx context.Context) (string, error)
}

// Configured 回報目前有沒有可用的 API key（環境變數或資料庫）；不發出請求。
func (c *Client) Configured(ctx context.Context) (bool, error) {
	key, err := c.resolveKey(ctx)
	if err != nil {
		return false, err
	}
	return key != "", nil
}

func (c *Client) resolveKey(ctx context.Context) (string, error) {
	if c.APIKey != "" {
		return c.APIKey, nil
	}
	if c.KeyFunc == nil {
		return "", nil
	}
	return c.KeyFunc(ctx)
}

// API key 格式不正確（含控制字元，例如貼上時多了換行）時給使用者看的訊息與修正動作（SRC-R10）。
const (
	msgKeyMalformed = "API key 格式不正確。請重新設定 Supadata 的 API key。"
	actionSetKey    = "設定 API key"
)

// ErrKeyMalformed 表示 API key 含控制字元，不可能放進標頭；不會發出請求。
var ErrKeyMalformed = errors.New("API key 格式不正確")

// validKey 回報 API key 能不能放進 HTTP 標頭：不含控制字元。
func validKey(key string) bool {
	return !strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// Result 是取得的字幕。
type Result struct {
	// Lang 是 Supadata 回報的語言，可能為空。
	Lang string
	Cues []subtitle.Cue
}

// transcriptResponse 是 GET /v1/transcript（text=false）的回應。
type transcriptResponse struct {
	Lang    string `json:"lang"`
	Content []struct {
		Text     string `json:"text"`
		Offset   int64  `json:"offset"`
		Duration int64  `json:"duration"`
	} `json:"content"`
	// JobID 在影片太長、Supadata 改為非同步處理時出現。
	JobID string `json:"jobId"`
}

// Fetch 取得 videoURL 的字幕。
//
// 錯誤分類（plan 第 5.2 節；狀態碼的意義見 https://docs.supadata.ai/errors/list）：
//   - 404、403（內容私人或受限）、206（transcript-unavailable）、內容為空：ErrNoTranscript，
//     這個來源沒有結果，呼叫端改用下一個來源。
//   - 401（key 無效）、402（方案不支援）：needs_action，分別帶 ErrKeyRejected、ErrPlanUnsupported。
//   - 429、5xx、網路錯誤、無法解析的回應：retryable。
//   - 3xx（不跟隨轉址）、400 及其他 4xx、非同步任務（202）：permanent；非同步任務的輪詢還沒做。
func (c *Client) Fetch(ctx context.Context, videoURL, mode string) (*Result, error) {
	key, err := c.resolveKey(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail(errclass.ClassRetryable, "讀取 API key 失敗", err)
	}
	if key == "" {
		return nil, fail(errclass.ClassNeedsAction, "", ErrNoKey)
	}
	// 含控制字元的 key 會讓標頭非法，被當成網路錯誤而一直重試；直接要使用者重新設定。
	if !validKey(key) {
		return nil, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: ID, Err: ErrKeyMalformed,
			Message: msgKeyMalformed, Actions: []string{actionSetKey},
		}
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	q := url.Values{"url": {videoURL}, "mode": {mode}}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/v1/transcript?"+q.Encode(), nil)
	if err != nil {
		return nil, fail(errclass.ClassPermanent, "", err)
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Accept", "application/json")

	// API key 放在自訂標頭，Go 跨網域轉址時不會移除它；所以不跟隨轉址，3xx 當成 permanent。
	// 複製 client，不改動呼叫端提供的那一個。
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(req)
	if err != nil {
		// url.Error 只含網址（沒有標頭），不會帶出 API key。
		return nil, fail(errclass.ClassRetryable, "", err)
	}
	defer resp.Body.Close()
	// LimitBody 多讀 1 個 byte 才能分辨「剛好到上限」與「超過上限」；一般的 LimitReader 到上限時只會回 EOF，
	// 截斷的 JSON 會被當成暫時性錯誤一直重試，甚至接受後面帶垃圾的完整文件。
	body, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		if errors.Is(err, netguard.ErrTooLarge) {
			return nil, fail(errclass.ClassPermanent, "回應超過大小上限", err)
		}
		return nil, fail(errclass.ClassRetryable, "讀取回應失敗", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusPartialContent:
		// 404：沒有結果；403：內容私人或受限；206 transcript-unavailable：沒有字幕。
		// 都是這個來源沒有結果，不是 key 的問題。
		return nil, ErrNoTranscript
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fail(errclass.ClassNeedsAction, "HTTP 401（API key 無效）", ErrKeyRejected)
	case resp.StatusCode == http.StatusPaymentRequired:
		return nil, fail(errclass.ClassNeedsAction, "HTTP 402（方案不支援）", ErrPlanUnsupported)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fail(errclass.ClassRetryable, fmt.Sprintf("HTTP %d", resp.StatusCode), nil)
	case resp.StatusCode == http.StatusAccepted:
		return nil, fail(errclass.ClassPermanent, "回應為非同步任務（202），還沒有支援輪詢", nil)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return nil, fail(errclass.ClassPermanent, fmt.Sprintf("HTTP %d（不跟隨轉址）", resp.StatusCode), nil)
	case resp.StatusCode != http.StatusOK:
		return nil, fail(errclass.ClassPermanent, fmt.Sprintf("HTTP %d", resp.StatusCode), nil)
	}

	var out transcriptResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fail(errclass.ClassRetryable, "回應不是預期的 JSON", err)
	}
	if out.JobID != "" && len(out.Content) == 0 {
		return nil, fail(errclass.ClassPermanent, "回應為非同步任務，還沒有支援輪詢", nil)
	}
	res := &Result{Lang: out.Lang}
	for _, seg := range out.Content {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		res.Cues = append(res.Cues, subtitle.Cue{
			Index: len(res.Cues) + 1, StartMS: seg.Offset, EndMS: seg.Offset + seg.Duration,
			Text: text, Timing: subtitle.TimingSource,
		})
	}
	if len(res.Cues) == 0 {
		return nil, ErrNoTranscript
	}
	return res, nil
}

func fail(class errclass.Class, detail string, err error) error {
	return &errclass.Error{Class: class, Source: ID, Detail: detail, Err: err}
}
