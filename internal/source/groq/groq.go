// Package groq 是 Groq 的語音轉錄（ASR）來源（SRC-R3、ADR-0006）：
// 把已下載的音檔送到 OpenAI 相容的 POST /audio/transcriptions，取得 segment 級的字幕。
//
// 連線一律由呼叫端提供的 http.Client 發出（netguard 的 Transport，GRD-R1）；
// API key 只放在 Authorization 標頭，不寫進網址、錯誤訊息或 log（NFR-SEC-3）。
package groq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

const (
	// ID 是來源 ID（steps.md 第 3 節）。
	ID = "groq"
	// DefaultBaseURL 是 Groq 的 OpenAI 相容 API 位址。
	DefaultBaseURL = "https://api.groq.com/openai/v1"
	// DefaultModel 是預設的轉錄模型。
	DefaultModel = "whisper-large-v3-turbo"
	// Timing 是 M1 宣告的時間粒度（ADR-0006 的 Caps.Timing）：
	// 只取 segment 級時間戳，word 級是否可用待 M0 確認（SRC 規格的待決問題）。
	Timing = "segment"
)

// key 被拒時給使用者看的訊息與修正動作（SRC-R10）。
const (
	MsgKeyRejected = "Groq 拒絕了 API key。請確認 API key 正確、帳號仍有額度。"
	ActionSetKey   = "設定 API key"
	// MsgKeyMalformed 是 API key 含控制字元（例如貼上時多了換行）時的訊息。
	MsgKeyMalformed = "API key 格式不正確。請重新設定 Groq 的 API key。"
)

// MaxFileBytes 是 Groq 單一音檔的大小上限（免費方案 25 MiB）。
// 呼叫端（pipeline 的 transcribeASR）超過時先切塊（SRC-R12）；Transcribe 仍檢查一次，超過就 permanent，不上傳。
const MaxFileBytes = 25 << 20

// msgFileTooLarge 是音檔超過 MaxFileBytes 時給使用者看的訊息。
const msgFileTooLarge = "音檔超過語音轉錄服務的大小上限（25 MiB）。"

// MaxFileBytes 回傳單一音檔的大小上限，超過時呼叫端要先切塊。
func (c *Client) MaxFileBytes() int64 { return MaxFileBytes }

// requestTimeout 是整個請求（含上傳音檔）的逾時；回應大小上限用 netguard.LimitFeed（GRD-R5）。
const requestTimeout = 10 * time.Minute

// maxErrorBody 是非 200 回應最多讀取（並丟棄）的 body 大小。
const maxErrorBody = 4 << 10

// ErrNoTranscript 表示 Groq 回傳的 segments 為空（例如靜音或沒有語音）；呼叫端改用下一個來源。
var ErrNoTranscript = fmt.Errorf("groq：沒有轉錄結果：%w", errclass.ErrNoResult)

// 401、403 都是 needs_action；ErrNoKey 與 ErrKeyRejected 讓呼叫端分辨「沒設定」與「被拒絕」。
var (
	// ErrNoKey 表示沒有設定 API key（SRC-R10）；不會發出請求。
	ErrNoKey = fmt.Errorf("沒有設定 API key：%w", errclass.ErrNotConfigured)
	// ErrKeyMalformed 表示 API key 含控制字元，不可能放進標頭；不會發出請求。
	ErrKeyMalformed = errors.New("API key 格式不正確")
	// ErrKeyRejected 表示 API key 無效或沒有權限（401、403）。
	ErrKeyRejected = errors.New("API key 無效或沒有權限")
)

// Client 是 Groq 的 HTTP client。
type Client struct {
	// HTTP 是對外 HTTP 的 client（netguard 的 Transport）。
	HTTP *http.Client
	// BaseURL 為空時使用 DefaultBaseURL。
	BaseURL string
	// Model 為空時使用 DefaultModel。
	Model string
	// APIKey 是 Groq 的 API key（環境變數，SEC-R1 優先）。
	APIKey string
	// KeyFunc 在 APIKey 為空時動態取得 API key（例如由資料庫解密讀取）。
	KeyFunc func(ctx context.Context) (string, error)

	// timeout 非 0 時取代 requestTimeout（讓測試不必真的等數分鐘）。
	timeout time.Duration
}

// requestTimeout 是整個請求的逾時。
func (c *Client) requestTimeout() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return requestTimeout
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

// Configured 回報有沒有 API key；沒有時 Transcribe 不會發出請求。
// 從資料庫讀取 key 失敗（例如暫時的讀取錯誤、密文損毀）時回傳錯誤，不當成「沒有設定」：
// 那是可以重試或要回報的儲存問題，不是使用者沒設定 key。
func (c *Client) Configured(ctx context.Context) (bool, error) {
	key, err := c.resolveKey(ctx)
	if err != nil {
		return false, err
	}
	return key != "", nil
}

// Endpoint 回傳實際使用的 base_url 與 model（空值已換成預設值）；兩者影響結果，所以屬於快取鍵的 params。
func (c *Client) Endpoint() (baseURL, model string) {
	baseURL, model = c.BaseURL, c.Model
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if model == "" {
		model = DefaultModel
	}
	return baseURL, model
}

// Result 是轉錄結果。
type Result struct {
	// Lang 是 BCP-47 語言代碼，例如 en；Whisper 回報的語言不在對照表內時為空。
	Lang string
	// Cues 的時間為 segment 級，Timing 為 subtitle.TimingSource。
	Cues []subtitle.Cue
	// DurationS 是 Groq 回報的音檔長度（秒）；沒有回報時取最後一個 cue 的結束時間。
	DurationS float64
}

// transcriptionResponse 是 response_format=verbose_json 的回應。
type transcriptionResponse struct {
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

// Transcribe 轉錄 audioPath 的音檔；langHint 是 ISO-639-1 語言提示，空字串表示讓 Whisper 自行偵測。
//
// 錯誤分類：
//   - 沒有 API key、401、403：needs_action，分別帶 ErrNoKey、ErrKeyRejected。
//   - 413（檔案過大）與其他 4xx、3xx（不跟隨轉址）、回應超過大小上限、無法讀取音檔：permanent。
//   - 429、5xx、網路錯誤、無法解析的回應：retryable。
//   - segments 為空：ErrNoTranscript，這個來源沒有結果。
//   - ctx 被取消時回傳 ctx.Err()，不是來源的錯。
func (c *Client) Transcribe(ctx context.Context, audioPath, langHint string) (*Result, error) {
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
	if strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: ID, Err: ErrKeyMalformed,
			Message: MsgKeyMalformed, Actions: []string{ActionSetKey},
		}
	}
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, fail(errclass.ClassPermanent, "無法讀取音檔", err)
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > MaxFileBytes {
		return nil, &errclass.Error{
			Class: errclass.ClassPermanent, Source: ID, Message: msgFileTooLarge,
			Detail: fmt.Sprintf("音檔 %d bytes，超過上限 %d bytes", fi.Size(), MaxFileBytes),
		}
	}

	base, model := c.Endpoint()

	// 以 pipe 串流上傳，不把整個音檔讀進記憶體。
	// 注意：body 長度未知，會使用 chunked（Transfer-Encoding: chunked）上傳；
	// 若遇到要求 Content-Length 的 proxy，需改為先把 multipart 寫進暫存檔再上傳。
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeForm(mw, f, filepath.Base(audioPath), model, langHint))
	}()

	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(base, "/")+"/audio/transcriptions", pr)
	if err != nil {
		pr.Close()
		return nil, fail(errclass.ClassPermanent, "", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	// 標頭帶有 API key，所以不跟隨轉址，3xx 當成 permanent。複製 client，不改動呼叫端提供的那一個。
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Groq 要轉錄完才回標頭，長音檔會超過 Transport 預設的 ResponseHeaderTimeout（netguard 為 30 秒），
	// 所以複製 Transport 並把標頭逾時放寬到整個請求的逾時；DialContext 等 netguard 的防護原樣保留。
	// Transport 不是 *http.Transport 時（例如測試替身）維持原樣，無法調整標頭逾時。
	switch tr := hc.Transport.(type) {
	case *http.Transport:
		tr = tr.Clone()
		tr.ResponseHeaderTimeout = c.requestTimeout()
		defer tr.CloseIdleConnections() // 複本只用這一次，結束時關掉閒置連線
		hc.Transport = tr
	case interface {
		WithResponseHeaderTimeout(time.Duration) http.RoundTripper
	}:
		// netguard.NewClient 的 Transport 外層多了 scheme 檢查，由它自己複製內層並放寬逾時。
		hc.Transport = tr.WithResponseHeaderTimeout(c.requestTimeout())
		defer hc.CloseIdleConnections()
	}
	resp, err := hc.Do(req)
	if err != nil {
		pr.Close() // 讓上傳的 goroutine 結束
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// url.Error 只含網址（沒有標頭），不會帶出 API key。
		return nil, fail(errclass.ClassRetryable, "", err)
	}
	defer resp.Body.Close()
	defer pr.Close()

	// 先依狀態碼分類，再讀 body：非 200 的 body 不影響分類（例如 401 搭超大 body 仍是 needs_action），
	// 也不會因為 body 過大或讀取失敗而被誤判；只讀固定的少量，讓連線有機會重用。
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	}
	// 錯誤訊息只帶狀態碼，不帶回應內文。
	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return nil, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: ID, Detail: fmt.Sprintf("HTTP %d", code), Err: ErrKeyRejected,
			Message: MsgKeyRejected, Actions: []string{ActionSetKey},
		}
	case code == http.StatusTooManyRequests || code >= 500:
		return nil, fail(errclass.ClassRetryable, fmt.Sprintf("HTTP %d", code), nil)
	case code == http.StatusRequestEntityTooLarge:
		return nil, fail(errclass.ClassPermanent, "HTTP 413（音檔超過 provider 的單檔上限）", nil)
	case code >= 300 && code < 400:
		return nil, fail(errclass.ClassPermanent, fmt.Sprintf("HTTP %d（不跟隨轉址）", code), nil)
	case code != http.StatusOK:
		return nil, fail(errclass.ClassPermanent, fmt.Sprintf("HTTP %d", code), nil)
	}

	// 只有 200 才讀完整 body。LimitBody 多讀 1 個 byte 才能分辨「剛好到上限」與「超過上限」，超過時不截斷。
	body, err := io.ReadAll(netguard.LimitBody(resp, netguard.LimitFeed))
	if err != nil {
		if errors.Is(err, netguard.ErrTooLarge) {
			return nil, fail(errclass.ClassPermanent, "回應超過大小上限", err)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail(errclass.ClassRetryable, "讀取回應失敗", err)
	}

	var out transcriptionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fail(errclass.ClassRetryable, "回應不是預期的 JSON", err)
	}
	res := &Result{Lang: BCP47(out.Language), DurationS: out.Duration}
	for _, seg := range out.Segments {
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			continue
		}
		start, end := ms(seg.Start), ms(seg.End)
		end = max(end, start)
		res.Cues = append(res.Cues, subtitle.Cue{
			Index: len(res.Cues) + 1, StartMS: start, EndMS: end, Text: text, Timing: subtitle.TimingSource,
		})
	}
	if len(res.Cues) == 0 {
		return nil, ErrNoTranscript
	}
	if last := float64(res.Cues[len(res.Cues)-1].EndMS) / 1000; res.DurationS < last {
		res.DurationS = last
	}
	return res, nil
}

// writeForm 寫出 multipart 欄位；file 放最後，避免 provider 在讀完設定前就開始處理。
func writeForm(mw *multipart.Writer, file io.Reader, name, model, langHint string) error {
	if err := mw.WriteField("model", model); err != nil {
		return err
	}
	if err := mw.WriteField("response_format", "verbose_json"); err != nil {
		return err
	}
	if langHint != "" {
		if err := mw.WriteField("language", langHint); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	return mw.Close()
}

// ms 把秒轉成毫秒；負數、NaN 與無限大視為 0。
func ms(sec float64) int64 {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 {
		return 0
	}
	return int64(math.Round(sec * 1000))
}

func fail(class errclass.Class, detail string, err error) error {
	return &errclass.Error{Class: class, Source: ID, Detail: detail, Err: err}
}
