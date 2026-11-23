package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// Batch API（BATCH）的 wire：Anthropic Message Batches 與 OpenAI Batch API。每個請求的內容與一般 API 完全相同
// （encode 的 body，含 prompt cache 的標記，BATCH-R2）；結果用一般 API 的解碼器取出輸出（BATCH-R4）。

// 結果檔的上限（BATCH-R11）：單一結果 10 MiB、整個結果檔 256 MiB；超過時整個 batch 視為 batch_failed。
const (
	MaxBatchResultLine = 10 << 20
	MaxBatchResults    = 256 << 20
	// MaxBatchRequests 是一個 batch 最多的請求數（BATCH-R2、H2）。
	MaxBatchRequests = 100
	// batchCallTimeout 是建立、查詢、取消的時間上限；batchResultsTimeout 是下載結果檔的時間上限。
	batchCallTimeout    = 2 * time.Minute
	batchResultsTimeout = 10 * time.Minute
	// openAIBatchEndpoint 是 OpenAI batch 的請求端點（BATCH-R2）。
	openAIBatchEndpoint = "/v1/chat/completions"
)

// batch 的狀態（BatchStatus.State）。
const (
	BatchInProgress = "in_progress"
	// BatchEnded 是 provider 處理完畢，結果可以取回（Anthropic processing_status: ended；OpenAI completed）。
	BatchEnded = "ended"
	// BatchTerminated 是 provider 把 batch 標為 failed、expired 或 cancelled（只有 OpenAI；BATCH-R4）。
	// 部分請求可能已有結果（輸出檔），其他請求沒有結果。
	BatchTerminated = "terminated"
)

// 一個請求的結果種類（BatchResult.Kind）。
const (
	ResultSucceeded = "succeeded"
	ResultErrored   = "errored"
	ResultExpired   = "expired"
	ResultCanceled  = "canceled"
)

var (
	// ErrForeignResults 是 provider 回傳了其他主機的結果網址（BATCH-R11）：不取用，整個 batch 視為 batch_failed。
	ErrForeignResults = errors.New("llm: batch 的結果網址不在 provider 的主機上")
	// ErrBatchResultsTooLarge 是結果檔或單一結果超過上限（BATCH-R11）。
	ErrBatchResultsTooLarge = errors.New("llm: batch 的結果超過大小上限")
)

// BatchSupported 回報 provider 是否可以使用 Batch API（BATCH-R1）：type 是 anthropic 或 openai，
// 而且 base_url 是內建的（相容服務不一定支援 batch）。
func BatchSupported(cfg ProviderConfig) bool {
	switch cfg.Type {
	case TypeAnthropic:
		return sameBase(cfg.BaseURL, DefaultAnthropicBaseURL)
	case TypeOpenAI:
		return sameBase(cfg.BaseURL, DefaultOpenAIBaseURL)
	}
	return false
}

func sameBase(a, b string) bool { return strings.TrimRight(a, "/") == strings.TrimRight(b, "/") }

// BatchRequest 是 batch 中的一個請求；CustomID 是任務 ID（BATCH-R2）。
type BatchRequest struct {
	CustomID string
	Request  Request
}

// BatchStatus 是 batch 目前的狀態。
type BatchStatus struct {
	ID    string
	State string // BatchInProgress、BatchEnded、BatchTerminated
	// Provider 是 provider 原本的狀態字串（只用於 log）。
	Provider string
	EndedAt  *time.Time
	// Anthropic：results_url。OpenAI：輸出檔與錯誤檔。
	resultsURL                string
	outputFileID, errorFileID string
}

// BatchResult 是一個請求的結果。Kind 是 succeeded 時 Body 是一般 API 的回應本體（用 DecodeBatchBody 解碼）。
type BatchResult struct {
	CustomID string
	Kind     string
	Body     json.RawMessage
}

// BatchClient 呼叫 provider 的 Batch API。HTTP 必須是 netguard 的 client（ADR-0014）。
type BatchClient struct {
	ID      string
	Type    string
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	Now     func() time.Time
}

// NewBatchClient 由一般 API 的 client 建立 BatchClient；client 不是 *HTTPClient 時回傳 nil。
func NewBatchClient(c Client) *BatchClient {
	hc, ok := c.(*HTTPClient)
	if !ok || hc == nil {
		return nil
	}
	return &BatchClient{ID: hc.ID, Type: hc.Type, BaseURL: hc.BaseURL, APIKey: hc.APIKey, HTTP: hc.HTTP, Now: hc.Now}
}

func (c *BatchClient) base() string { return strings.TrimRight(c.BaseURL, "/") }

func (c *BatchClient) fail(class errclass.Class, detail string, err error) *errclass.Error {
	return &errclass.Error{Class: class, Source: "llm:" + c.ID, Detail: detail, Err: err}
}

// Submit 送出一個 batch，回傳 provider 的 batch ID。Anthropic：POST /v1/messages/batches；
// OpenAI：先上傳 JSONL（purpose: batch），再 POST /v1/batches（BATCH-R2）。
func (c *BatchClient) Submit(ctx context.Context, reqs []BatchRequest) (string, error) {
	if len(reqs) == 0 || len(reqs) > MaxBatchRequests {
		return "", c.fail(errclass.ClassPermanent, fmt.Sprintf("batch 的請求數 %d 不在 1～%d", len(reqs), MaxBatchRequests), nil)
	}
	hc := &HTTPClient{ID: c.ID, Type: c.Type, BaseURL: c.BaseURL}
	bodies := make([]json.RawMessage, len(reqs))
	for i, r := range reqs {
		if r.Request.Spec == nil {
			return "", c.fail(errclass.ClassPermanent, "batch 的請求沒有 Spec", nil)
		}
		_, body, err := hc.encode(r.Request)
		if err != nil {
			return "", c.fail(errclass.ClassPermanent, "組出請求失敗", err)
		}
		bodies[i] = body
	}
	if c.Type == TypeAnthropic {
		items := make([]map[string]any, len(reqs))
		for i, r := range reqs {
			items[i] = map[string]any{"custom_id": r.CustomID, "params": bodies[i]}
		}
		body, err := json.Marshal(map[string]any{"requests": items})
		if err != nil {
			return "", err
		}
		var v struct {
			ID string `json:"id"`
		}
		if err := c.call(ctx, http.MethodPost, c.base()+"/v1/messages/batches", "application/json", body, &v); err != nil {
			return "", err
		}
		if !validExternalID(v.ID) {
			return "", c.fail(errclass.ClassRetryable, detailBadJSON, nil)
		}
		return v.ID, nil
	}
	var jsonl bytes.Buffer
	for i, r := range reqs {
		line, err := json.Marshal(map[string]any{"custom_id": r.CustomID, "method": http.MethodPost, "url": openAIBatchEndpoint, "body": bodies[i]})
		if err != nil {
			return "", err
		}
		jsonl.Write(line)
		jsonl.WriteByte('\n')
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	if err := mw.WriteField("purpose", "batch"); err != nil {
		return "", err
	}
	fw, err := mw.CreateFormFile("file", "batch.jsonl")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(jsonl.Bytes()); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	var file struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, c.base()+"/files", mw.FormDataContentType(), form.Bytes(), &file); err != nil {
		return "", err
	}
	if !validExternalID(file.ID) {
		return "", c.fail(errclass.ClassRetryable, detailBadJSON, nil)
	}
	body, err := json.Marshal(map[string]any{"input_file_id": file.ID, "endpoint": openAIBatchEndpoint, "completion_window": "24h"})
	if err != nil {
		return "", err
	}
	var b struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, c.base()+"/batches", "application/json", body, &b); err != nil {
		return "", err
	}
	if !validExternalID(b.ID) {
		return "", c.fail(errclass.ClassRetryable, detailBadJSON, nil)
	}
	return b.ID, nil
}

// validExternalID 檢查 provider 回傳的 batch 與檔案 ID：會放進網址路徑，只接受安全的字元。
func validExternalID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Status 查詢 batch 的狀態。
func (c *BatchClient) Status(ctx context.Context, id string) (BatchStatus, error) {
	if !validExternalID(id) {
		return BatchStatus{}, c.fail(errclass.ClassPermanent, "batch ID 不正確", nil)
	}
	if c.Type == TypeAnthropic {
		return c.anthropicStatus(ctx, http.MethodGet, c.base()+"/v1/messages/batches/"+id)
	}
	return c.openAIStatus(ctx, http.MethodGet, c.base()+"/batches/"+id)
}

// Cancel 向 provider 取消 batch（BATCH-R5、R9），回傳取消之後的狀態。
func (c *BatchClient) Cancel(ctx context.Context, id string) (BatchStatus, error) {
	if !validExternalID(id) {
		return BatchStatus{}, c.fail(errclass.ClassPermanent, "batch ID 不正確", nil)
	}
	if c.Type == TypeAnthropic {
		return c.anthropicStatus(ctx, http.MethodPost, c.base()+"/v1/messages/batches/"+id+"/cancel")
	}
	return c.openAIStatus(ctx, http.MethodPost, c.base()+"/batches/"+id+"/cancel")
}

func (c *BatchClient) anthropicStatus(ctx context.Context, method, u string) (BatchStatus, error) {
	var v struct {
		ID               string  `json:"id"`
		ProcessingStatus string  `json:"processing_status"`
		EndedAt          *string `json:"ended_at"`
		ResultsURL       *string `json:"results_url"`
	}
	if err := c.call(ctx, method, u, "", nil, &v); err != nil {
		return BatchStatus{}, err
	}
	st := BatchStatus{ID: v.ID, Provider: v.ProcessingStatus, State: BatchInProgress}
	if v.ProcessingStatus == "ended" {
		st.State = BatchEnded
		if v.ResultsURL != nil {
			st.resultsURL = *v.ResultsURL
		}
		if v.EndedAt != nil {
			if t, err := time.Parse(time.RFC3339, *v.EndedAt); err == nil {
				st.EndedAt = &t
			}
		}
	}
	return st, nil
}

func (c *BatchClient) openAIStatus(ctx context.Context, method, u string) (BatchStatus, error) {
	var v struct {
		ID           string  `json:"id"`
		Status       string  `json:"status"`
		OutputFileID *string `json:"output_file_id"`
		ErrorFileID  *string `json:"error_file_id"`
		CompletedAt  *int64  `json:"completed_at"`
		FailedAt     *int64  `json:"failed_at"`
		ExpiredAt    *int64  `json:"expired_at"`
		CancelledAt  *int64  `json:"cancelled_at"`
	}
	if err := c.call(ctx, method, u, "", nil, &v); err != nil {
		return BatchStatus{}, err
	}
	st := BatchStatus{ID: v.ID, Provider: v.Status, State: BatchInProgress}
	var end *int64
	switch v.Status {
	case "completed":
		st.State, end = BatchEnded, v.CompletedAt
	case "failed":
		st.State, end = BatchTerminated, v.FailedAt
	case "expired":
		st.State, end = BatchTerminated, v.ExpiredAt
	case "cancelled":
		st.State, end = BatchTerminated, v.CancelledAt
	}
	if end != nil {
		t := time.Unix(*end, 0).UTC()
		st.EndedAt = &t
	}
	if v.OutputFileID != nil {
		st.outputFileID = *v.OutputFileID
	}
	if v.ErrorFileID != nil {
		st.errorFileID = *v.ErrorFileID
	}
	return st, nil
}

// Results 逐行讀出已結束（或被終止）的 batch 的結果，每一個結果呼叫一次 fn（BATCH-R4、R11）。
// 結果網址不在 base_url 的主機上時不連線，回傳 ErrForeignResults；超過大小上限回傳 ErrBatchResultsTooLarge。
// 格式不對的行略過（不可信的輸入；那個請求視為沒有結果）。
func (c *BatchClient) Results(ctx context.Context, st BatchStatus, fn func(BatchResult) error) error {
	if c.Type == TypeAnthropic {
		if st.resultsURL == "" {
			return nil
		}
		if !c.sameHost(st.resultsURL) {
			return ErrForeignResults
		}
		return c.readLines(ctx, st.resultsURL, func(line []byte) error {
			var v struct {
				CustomID string `json:"custom_id"`
				Result   struct {
					Type    string          `json:"type"`
					Message json.RawMessage `json:"message"`
				} `json:"result"`
			}
			if json.Unmarshal(line, &v) != nil || v.CustomID == "" {
				return nil
			}
			r := BatchResult{CustomID: v.CustomID}
			switch v.Result.Type {
			case ResultSucceeded:
				r.Kind, r.Body = ResultSucceeded, v.Result.Message
			case ResultErrored, ResultExpired, ResultCanceled:
				r.Kind = v.Result.Type
			default:
				return nil
			}
			return fn(r)
		})
	}
	for _, f := range []string{st.outputFileID, st.errorFileID} {
		if f == "" {
			continue
		}
		if !validExternalID(f) {
			return c.fail(errclass.ClassPermanent, "檔案 ID 不正確", nil)
		}
		err := c.readLines(ctx, c.base()+"/files/"+f+"/content", func(line []byte) error {
			var v struct {
				CustomID string `json:"custom_id"`
				Response *struct {
					StatusCode int             `json:"status_code"`
					Body       json.RawMessage `json:"body"`
				} `json:"response"`
				Error json.RawMessage `json:"error"`
			}
			if json.Unmarshal(line, &v) != nil || v.CustomID == "" {
				return nil
			}
			r := BatchResult{CustomID: v.CustomID, Kind: ResultErrored}
			if v.Response != nil && v.Response.StatusCode == http.StatusOK && len(v.Response.Body) > 0 && isNull(v.Error) {
				r.Kind, r.Body = ResultSucceeded, v.Response.Body
			}
			return fn(r)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func isNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

// sameHost 回報 u 與 base_url 是否同一個 scheme 與主機（BATCH-R11）。
func (c *BatchClient) sameHost(u string) bool {
	a, err1 := url.Parse(u)
	b, err2 := url.Parse(c.BaseURL)
	if err1 != nil || err2 != nil || a.User != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

// DecodeBatchBody 用一般 API 的解碼器取出成功結果的輸出（與 Complete 相同，BATCH-R4）。
// spec 為 nil 時只取用量（已經沒有對應任務的結果，BATCH-R9）。
func (c *BatchClient) DecodeBatchBody(raw []byte, spec *Spec) (Response, error) {
	if len(raw) > MaxBatchResultLine {
		return Response{}, c.fail(errclass.ClassPermanent, detailTooLarge, nil)
	}
	if c.Type == TypeAnthropic {
		tool := ""
		if spec != nil {
			tool = spec.ToolName()
		}
		return decodeAnthropic(raw, tool)
	}
	return decodeOpenAI(raw)
}

// readLines 下載 u 並逐行交給 fn：單一行最多 MaxBatchResultLine、全部最多 MaxBatchResults。
func (c *BatchClient) readLines(ctx context.Context, u string, fn func([]byte) error) error {
	ctx, cancel := context.WithTimeout(ctx, batchResultsTimeout)
	defer cancel()
	resp, err := c.do(ctx, http.MethodGet, u, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return (&HTTPClient{ID: c.ID, Now: c.Now}).statusError("", resp, snippet)
	}
	br := bufio.NewReaderSize(netguard.LimitBody(resp, MaxBatchResults), 64<<10)
	for {
		line, err := readLine(br, MaxBatchResultLine)
		if len(bytes.TrimSpace(line)) > 0 {
			if ferr := fn(line); ferr != nil {
				return ferr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(err, netguard.ErrTooLarge) || errors.Is(err, ErrBatchResultsTooLarge) {
				return ErrBatchResultsTooLarge
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return c.fail(errclass.ClassRetryable, "讀取 batch 結果失敗", err)
		}
	}
}

// readLine 讀出一行（不含換行）；超過 limit 時回傳 ErrBatchResultsTooLarge。
func readLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > limit+1 {
			return nil, ErrBatchResultsTooLarge
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

// call 送出請求並把 200 的 JSON 回應解碼到 out。
func (c *BatchClient) call(ctx context.Context, method, u, contentType string, body []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, batchCallTimeout)
	defer cancel()
	resp, err := c.do(ctx, method, u, contentType, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return (&HTTPClient{ID: c.ID, Now: c.Now}).statusError("", resp, snippet)
	}
	raw, err := io.ReadAll(netguard.LimitBody(resp, maxResponseBody))
	if err != nil {
		if errors.Is(err, netguard.ErrTooLarge) {
			return c.fail(errclass.ClassPermanent, detailTooLarge, err)
		}
		return c.fail(errclass.ClassRetryable, "讀取回應失敗", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return c.fail(errclass.ClassRetryable, detailBadJSON, err)
	}
	return nil
}

// do 送出一個請求：帶 API key 的標頭、不跟隨轉址（同 Complete）。
func (c *BatchClient) do(ctx context.Context, method, u, contentType string, body []byte) (*http.Response, error) {
	if c.HTTP == nil {
		return nil, c.fail(errclass.ClassPermanent, "沒有 HTTP client", nil)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, c.fail(errclass.ClassPermanent, "組出請求失敗", err)
	}
	if contentType != "" {
		hr.Header.Set("Content-Type", contentType)
	}
	hr.Header.Set("Accept", "application/json")
	if c.Type == TypeAnthropic {
		hr.Header.Set("x-api-key", c.APIKey)
		hr.Header.Set("anthropic-version", AnthropicVersion)
	} else {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.fail(errclass.ClassRetryable, "連線失敗", err)
	}
	return resp, nil
}
