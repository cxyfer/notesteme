package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// provider 的 wire format（llm.providers.<id>.type，ADR-0019）。
const (
	TypeAnthropic = "anthropic"
	TypeOpenAI    = "openai" // OpenAI 與相容的 provider（可自訂 base_url）
	// TypeGemini 是 Gemini 的 generateContent（GEM-R4）；只用於 stages.note.engine: gemini（請求要有 FileURI）。
	TypeGemini = "gemini"
)

// 內建 provider 的預設 base_url。anthropic 的端點是 <base>/v1/messages；openai 的 base_url 依慣例含 /v1，端點是 <base>/chat/completions。
const (
	DefaultAnthropicBaseURL = "https://api.anthropic.com"
	// DefaultGeminiBaseURL 是 Gemini 的預設 base_url（GEM-R4）。
	DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
	DefaultOpenAIBaseURL = "https://api.openai.com/v1"
	// AnthropicVersion 是 anthropic-version 標頭（wire 契約）。
	AnthropicVersion = "2023-06-01"
)

const (
	// RequestTimeout 是一個請求的逾時：模型產生完整輸出才回應，長 chunk 可能要幾分鐘。
	// 必須小於任務的租約（queue.DefaultLease），否則租約失效後任務會被重新領取而重複計費（RunOptions.Timeout）。
	RequestTimeout = 5 * time.Minute
	// idleConnTimeout 是閒置連線保留的時間；所有 chunk 共用同一個連線池。
	idleConnTimeout = 90 * time.Second
	// maxResponseBody 是回應大小的上限；超過時不截斷，視為 permanent（GRD-R5）。
	maxResponseBody = 16 << 20
	// maxErrorBody 是錯誤回應最多讀取的位元組，只用於診斷摘要。
	maxErrorBody = 4 << 10
)

// 給使用者看的動作（LLM-R11、R12）。
const (
	ActionChangeModel = "更換模型"
	ActionSetAPIKey   = "設定 API key"
	ActionCheckQuota  = "檢查 provider 額度"
)

// Request 是一次 LLM 請求。固定前綴（Fixed）在同一部影音、同一步驟的所有 chunk 完全相同；
// Data 是 <transcript_data> 區塊；Retry 是上一次驗證失敗的說明（空字串代表第一次）。
type Request struct {
	Model     string
	MaxTokens int
	Spec      *Spec
	Fixed     Fixed
	Data      string
	Retry     string
	// Images 是附在資料區塊之後的圖片（FRM-R7 的拼圖）；anthropic 送 image block，openai 送 image_url 的 data URL（FRM F4）。
	Images []Image
	// FileURI 是 Gemini 直接讀取的影片網址（GEM-R4 的 file_data）；只有 type 為 gemini 的 provider 使用，其他 provider 忽略。
	FileURI string
}

// Image 是送給 vision 模型的一張圖片。
type Image struct {
	MediaType string // 目前只有 image/jpeg
	Data      []byte
}

// Usage 是 provider 回報的 token 用量（LLM-R10）。
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_input_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// Add 累加用量。
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
}

// Response 是成功（HTTP 200）的回應。Output 是強制的 tool 輸入或 JSON 內容；沒有結構化輸出時是 nil，
// 原因寫在 Problem（例如模型直接回了文字），呼叫端視為驗證失敗而不是 API 錯誤（wire 契約）。
type Response struct {
	Output  []byte
	Problem string
	Usage   Usage
}

// Client 是一個 LLM provider。
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// HTTPClient 依 wire format 呼叫 provider。HTTP 必須是 NewHTTP 由 netguard 的 client 建立的（ADR-0014）；
// HTTPClient 本身很輕，可以每個 chunk 建立，連線池在 HTTP 中共用。
type HTTPClient struct {
	ID      string // provider id，只用於錯誤訊息
	Type    string
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// Now 給 Retry-After 的 HTTP 日期格式用；nil 時視為沒有指定。
	Now func() time.Time
}

// Complete 送出請求。錯誤一律是 *errclass.Error（或 ctx 的錯誤）：
//   - 404：模型不存在或已停用，needs_action（LLM-R11）。
//   - 401、403：API key 被拒絕，needs_action（LLM-R12 的同一個動作）。
//   - 429、5xx（含 anthropic 的 529）、連線失敗：retryable，帶 Retry-After（LLM-R6）。
//   - 其他 4xx 與轉址：permanent（請求本身不對，重試也不會成功）。
func (c *HTTPClient) Complete(ctx context.Context, req Request) (Response, error) {
	if req.Spec == nil {
		return Response{}, errors.New("llm: Request.Spec 不可為 nil")
	}
	if c.Type == TypeGemini {
		if c.HTTP == nil {
			return Response{}, c.fail(errclass.ClassPermanent, "沒有 HTTP client", nil)
		}
		return c.completeGemini(ctx, req)
	}
	url, body, err := c.encode(req)
	if err != nil {
		return Response{}, c.fail(errclass.ClassPermanent, "組出請求失敗", err)
	}
	if c.HTTP == nil {
		return Response{}, c.fail(errclass.ClassPermanent, "沒有 HTTP client", nil)
	}
	reqCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, c.fail(errclass.ClassPermanent, "組出請求失敗", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")
	if c.Type == TypeAnthropic {
		hr.Header.Set("x-api-key", c.APIKey)
		hr.Header.Set("anthropic-version", AnthropicVersion)
	} else {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	// 不跟隨轉址（標頭帶有 API key）；淺複製 http.Client，Transport 與連線池共用。
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		// 被 netguard 擋下的錯誤原樣保留，pipeline 會把它歸為 permanent（GRD）；url.Error 只含網址，不含標頭中的 key。
		return Response{}, c.fail(errclass.ClassRetryable, "連線失敗", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return Response{}, c.statusError(req.Model, resp, snippet)
	}
	raw, err := io.ReadAll(netguard.LimitBody(resp, maxResponseBody))
	if err != nil {
		if errors.Is(err, netguard.ErrTooLarge) {
			return Response{}, c.fail(errclass.ClassPermanent, "回應超過大小上限", err)
		}
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, c.fail(errclass.ClassRetryable, "讀取回應失敗", err)
	}
	if c.Type == TypeAnthropic {
		return decodeAnthropic(raw, req.Spec.ToolName())
	}
	return decodeOpenAI(raw)
}

// NewHTTP 由 netguard 的 client 建立 LLM 共用的 client：等待回應標頭的逾時放寬到 RequestTimeout（模型產生完整輸出才回應），
// 閒置連線 idleConnTimeout 後關閉；netguard 的連線防護原樣保留。Transport 只複製這一次，所有請求共用連線池。
// base 是 nil 時回傳 nil。
func NewHTTP(base *http.Client) *http.Client {
	return newHTTPWithTimeout(base, RequestTimeout)
}

// newHTTPWithTimeout 同 NewHTTP，等待回應標頭的逾時是 header（Gemini 讀影片要比較久，GEM-R4）。
func newHTTPWithTimeout(base *http.Client, header time.Duration) *http.Client {
	if base == nil {
		return nil
	}
	hc := *base
	switch tr := hc.Transport.(type) {
	case *http.Transport:
		tr = tr.Clone()
		tr.ResponseHeaderTimeout = header
		tr.IdleConnTimeout = idleConnTimeout
		hc.Transport = tr
	case interface {
		WithTimeouts(header, idle time.Duration) http.RoundTripper
	}:
		hc.Transport = tr.WithTimeouts(header, idleConnTimeout)
	}
	return &hc
}

func (c *HTTPClient) fail(class errclass.Class, detail string, err error) *errclass.Error {
	return &errclass.Error{Class: class, Source: "llm:" + c.ID, Detail: detail, Err: err}
}

// statusError 依狀態碼分類錯誤。訊息只帶狀態碼與 provider 回報的錯誤摘要，不帶請求內容與 key。
func (c *HTTPClient) statusError(model string, resp *http.Response, body []byte) error {
	code := resp.StatusCode
	typ := errorType(body)
	detail := fmt.Sprintf("HTTP %d", code)
	if typ != "" {
		detail += " " + typ
	}
	switch {
	case code == http.StatusTooManyRequests && typ == "insufficient_quota":
		// OpenAI 的額度用盡：不是暫時性的，重試也不會成功，要使用者處理。
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: detail,
			Message: fmt.Sprintf("LLM provider %s 的額度不足（insufficient_quota）。", c.ID), Actions: []string{ActionCheckQuota},
		}
	case code == http.StatusNotFound:
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: detail,
			Message: fmt.Sprintf("模型 %s 已停用或不存在。", model), Actions: []string{ActionChangeModel},
		}
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: detail,
			Message: fmt.Sprintf("LLM provider %s 拒絕了 API key（HTTP %d）。", c.ID, code), Actions: []string{ActionSetAPIKey},
		}
	case code == http.StatusTooManyRequests || code >= 500:
		e := c.fail(errclass.ClassRetryable, detail, nil)
		e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"), c.Now)
		return e
	default:
		e := c.fail(errclass.ClassPermanent, detail, nil)
		e.Message = fmt.Sprintf("LLM provider %s 拒絕了請求（HTTP %d），重試也不會成功。", c.ID, code)
		return e
	}
}

// errorTypePattern 是 error.type 允許的字元；不符時不記錄（第三方相容服務的欄位可能回顯 key 或 prompt 片段）。
var errorTypePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// errorType 取出 provider 錯誤回應中的 error.type（兩家都是 {"error": {"type"}}）。
// 錯誤訊息（error.message）一律不記錄：第三方相容服務可能把 key 或 prompt 的片段回顯在裡面。
func errorType(body []byte) string {
	var v struct {
		Error struct {
			Type any `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	t, _ := v.Error.Type.(string)
	if !errorTypePattern.MatchString(t) {
		return ""
	}
	return t
}

// maxRetryAfterSeconds 是 Retry-After 的上限（1 小時）。
const maxRetryAfterSeconds = 3600

// retryAfter 解析 Retry-After：秒數或 HTTP 日期；無法解析或是負數時回傳 0。上限 1 小時。
func retryAfter(v string, now func() time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		// 先夾在 [0, 3600] 再轉成 Duration：大數乘上 time.Second 會溢位成負數。
		d = time.Duration(min(max(n, 0), maxRetryAfterSeconds)) * time.Second
	} else if _, err := strconv.ParseFloat(v, 64); err == nil {
		// 超出 int64 的秒數（或小數）不是合法的 delta-seconds，但仍是「很久」：取上限。
		if strings.HasPrefix(v, "-") {
			return 0
		}
		d = time.Duration(maxRetryAfterSeconds) * time.Second
	} else if t, err := http.ParseTime(v); err == nil && now != nil {
		d = t.Sub(now())
	}
	if d < 0 {
		return 0
	}
	return min(d, maxRetryAfterSeconds*time.Second)
}

// encode 依 wire format 組出端點與 body（ADR-0019）。
func (c *HTTPClient) encode(req Request) (url string, body []byte, err error) {
	base := strings.TrimRight(c.BaseURL, "/")
	userBlocks := []map[string]any{
		// 固定前綴的結尾加 cache 標記（LLM-R3）；openai 會自動快取相同的前綴，不需要標記。
		{"type": "text", "text": req.Fixed.Prefix, "cache_control": map[string]any{"type": "ephemeral"}},
		{"type": "text", "text": req.Data},
	}
	for _, img := range req.Images {
		userBlocks = append(userBlocks, map[string]any{"type": "image", "source": map[string]any{
			"type": "base64", "media_type": img.MediaType, "data": base64.StdEncoding.EncodeToString(img.Data),
		}})
	}
	if req.Retry != "" {
		userBlocks = append(userBlocks, map[string]any{"type": "text", "text": req.Retry})
	}
	var v any
	if c.Type == TypeAnthropic {
		url = base + "/v1/messages"
		v = map[string]any{
			"model":      req.Model,
			"max_tokens": max(req.MaxTokens, 1),
			"system":     []map[string]any{{"type": "text", "text": req.Fixed.System}},
			"messages":   []map[string]any{{"role": "user", "content": userBlocks}},
			"tools": []map[string]any{{
				"name":         req.Spec.ToolName(),
				"description":  "輸出 " + req.Spec.Stage + " 步驟的結果。",
				"input_schema": req.Spec.Schema,
			}},
			"tool_choice": map[string]any{"type": "tool", "name": req.Spec.ToolName()},
		}
	} else {
		url = base + "/chat/completions"
		parts := []string{req.Fixed.Prefix, req.Data}
		if req.Retry != "" {
			parts = append(parts, req.Retry)
		}
		var user any = strings.Join(parts, "\n")
		if len(req.Images) > 0 {
			// 有圖片時 content 改成區塊陣列：文字在前、圖片在後（openai 的 image_url 以 data URL 傳送）。
			blocks := []map[string]any{{"type": "text", "text": strings.Join(parts, "\n")}}
			for _, img := range req.Images {
				blocks = append(blocks, map[string]any{"type": "image_url", "image_url": map[string]any{
					"url": "data:" + img.MediaType + ";base64," + base64.StdEncoding.EncodeToString(img.Data),
				}})
			}
			user = blocks
		}
		v = map[string]any{
			"model": req.Model,
			"messages": []map[string]any{
				{"role": "system", "content": req.Fixed.System},
				{"role": "user", "content": user},
			},
			"response_format": map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name": req.Spec.ToolName(), "strict": true, "schema": req.Spec.Schema,
				},
			},
		}
	}
	body, err = json.Marshal(v)
	return url, body, err
}

// decodeAnthropic 取出強制的 tool_use block 的 input。沒有這個 block 時（模型偶爾直接回文字）Output 是 nil。
func decodeAnthropic(raw []byte, tool string) (Response, error) {
	var v struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      Usage  `json:"usage"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Response{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "回應不是預期的 JSON", Err: err}
	}
	res := Response{Usage: v.Usage}
	if v.StopReason == "max_tokens" {
		res.Problem = "the output was cut off by max_tokens"
		return res, nil
	}
	for _, b := range v.Content {
		if b.Type == "tool_use" && b.Name == tool {
			if len(b.Input) == 0 {
				res.Problem = "the " + tool + " tool_use block has no input"
				return res, nil
			}
			res.Output = b.Input
			return res, nil
		}
	}
	res.Problem = "no " + tool + " tool_use block in the response"
	return res, nil
}

// decodeOpenAI 取出第一個 choice 的 content（strict json_schema 的輸出是 JSON 字串）。
func decodeOpenAI(raw []byte) (Response, error) {
	var v struct {
		Choices []struct {
			Message struct {
				Content *string `json:"content"`
				Refusal *string `json:"refusal"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Response{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "回應不是預期的 JSON", Err: err}
	}
	res := Response{Usage: Usage{
		InputTokens: v.Usage.PromptTokens, OutputTokens: v.Usage.CompletionTokens, CacheReadTokens: v.Usage.PromptTokensDetails.CachedTokens,
	}}
	switch {
	case len(v.Choices) == 0:
		res.Problem = "the response has no choices"
	case v.Choices[0].Message.Refusal != nil && *v.Choices[0].Message.Refusal != "":
		res.Problem = "the model refused"
	case v.Choices[0].FinishReason == "length":
		res.Problem = "the output was cut off by the token limit"
	case v.Choices[0].Message.Content == nil:
		res.Problem = "the response has no content"
	default:
		res.Output = []byte(*v.Choices[0].Message.Content)
	}
	return res, nil
}
