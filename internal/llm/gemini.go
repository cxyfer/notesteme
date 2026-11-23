package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// Gemini 的 wire（GEM-R4、R7）。
const (
	// GeminiKeyHeader 是 API key 的標頭：key 不放在 URL 的 query，網址才能安全地出現在錯誤與 log 中。
	GeminiKeyHeader = "x-goog-api-key"
	// GeminiRequestTimeout 是一個 generateContent 請求的逾時（GEM-R4：10 分鐘）。
	// 任務的租約也是 10 分鐘（queue.DefaultLease），實際上限是 RunGemini 的 Timeout（租約剩下的時間減去安全邊際，GEM G8）。
	GeminiRequestTimeout = 10 * time.Minute
	// ActionGeminiFallback 是改用一般流程的動作（GEM-R3、R5、R7）。
	ActionGeminiFallback = "改用一般流程重新處理"
)

// geminiModelPattern 是可以放進端點路徑的模型名稱：不含 /、?、#、:，避免改變端點。
var geminiModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// geminiStatusPattern 是 error.status 允許的字元（google.rpc.Code 的名稱）；不符時不記錄。
var geminiStatusPattern = regexp.MustCompile(`^[A-Z_]{1,40}$`)

// geminiPart 是 contents[].parts[] 的一項；file_data 與 text 二擇一。
type geminiPart struct {
	FileData *geminiFileData `json:"file_data,omitempty"`
	Text     string          `json:"text,omitempty"`
}

type geminiFileData struct {
	FileURI string `json:"file_uri"`
}

// encodeGemini 組出 generateContent 的端點與 body（GEM-R4）：第一個 part 是影片網址，
// 接著是指示（Fixed.System）、<video_metadata>（Fixed.Prefix）與驗證失敗的說明；結構化輸出用 responseSchema。
func (c *HTTPClient) encodeGemini(req Request) (endpoint string, body []byte, err error) {
	if req.FileURI == "" {
		return "", nil, errors.New("gemini 的請求沒有影片網址（只能用於 stages.note.engine: gemini）")
	}
	if !geminiModelPattern.MatchString(req.Model) {
		return "", nil, fmt.Errorf("模型名稱 %q 不能用於 Gemini 的端點", req.Model)
	}
	if req.Spec.GeminiSchema == nil {
		return "", nil, fmt.Errorf("%s 沒有 Gemini 的 responseSchema", req.Spec.Stage)
	}
	if len(req.Images) > 0 {
		return "", nil, errors.New("gemini 的請求不支援圖片")
	}
	base := strings.TrimRight(c.BaseURL, "/")
	endpoint = base + "/v1beta/models/" + url.PathEscape(req.Model) + ":generateContent"
	parts := []geminiPart{{FileData: &geminiFileData{FileURI: req.FileURI}}, {Text: req.Fixed.System}}
	if req.Fixed.Prefix != "" {
		parts = append(parts, geminiPart{Text: req.Fixed.Prefix})
	}
	if req.Data != "" {
		parts = append(parts, geminiPart{Text: req.Data})
	}
	if req.Retry != "" {
		parts = append(parts, geminiPart{Text: req.Retry})
	}
	gen := map[string]any{
		"responseMimeType": "application/json",
		"responseSchema":   req.Spec.GeminiSchema,
	}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	body, err = json.Marshal(map[string]any{
		"contents":         []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": gen,
	})
	return endpoint, body, err
}

// completeGemini 送出 generateContent。錯誤分類（GEM-R7）：
//   - 429、5xx、連線失敗：retryable，帶 Retry-After。
//   - 400 INVALID_ARGUMENT、403 PERMISSION_DENIED：Gemini 無法處理這部影片，needs_action，動作「改用一般流程重新處理」；
//     但 400 的原因是 API_KEY_INVALID 時是 key 被拒絕（動作「設定 API key」）。
//   - 401：key 被拒絕；404：模型不存在（LLM-R11）；其他 4xx 與轉址：permanent。
//
// 訊息只帶狀態碼與 error.status，不帶 error.message（可能回顯請求內容）、請求內容與 key。
func (c *HTTPClient) completeGemini(ctx context.Context, req Request) (Response, error) {
	if req.FileURI == "" {
		// 其他步驟（segment、correct、translate、chapters、frames、playground）選了 gemini 的 provider（GEM G8）。
		e := c.fail(errclass.ClassPermanent, "gemini 只能用於 Gemini 直接生成", nil)
		e.Message = "type 為 gemini 的 provider 只能用於 stages.note.engine: gemini。"
		return Response{}, e
	}
	endpoint, body, err := c.encodeGemini(req)
	if err != nil {
		return Response{}, c.fail(errclass.ClassPermanent, "組出請求失敗", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, GeminiRequestTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, c.fail(errclass.ClassPermanent, "組出請求失敗", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set(GeminiKeyHeader, c.APIKey)
	hc := *c.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := hc.Do(hr)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, c.fail(errclass.ClassRetryable, "連線失敗", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return Response{}, c.geminiStatusError(req.Model, resp, snippet)
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
	return c.decodeGemini(raw)
}

// geminiError 是 Google API 的錯誤回應：{"error": {"code", "status", "message", "details": [{"reason"}]}}。
type geminiError struct {
	Error struct {
		Status  string `json:"status"`
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

// geminiErrorInfo 取出 error.status 與 details 中是否有 API_KEY_INVALID；status 不符 geminiStatusPattern 時是空字串。
func geminiErrorInfo(body []byte) (status string, badKey bool) {
	var v geminiError
	if json.Unmarshal(body, &v) != nil {
		return "", false
	}
	if geminiStatusPattern.MatchString(v.Error.Status) {
		status = v.Error.Status
	}
	for _, d := range v.Error.Details {
		if d.Reason == "API_KEY_INVALID" {
			badKey = true
		}
	}
	return status, badKey
}

func (c *HTTPClient) geminiStatusError(model string, resp *http.Response, body []byte) error {
	code := resp.StatusCode
	status, badKey := geminiErrorInfo(body)
	detail := fmt.Sprintf("HTTP %d", code)
	if status != "" {
		detail += " " + status
	}
	keyRejected := &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: detail,
		Message: fmt.Sprintf("LLM provider %s 拒絕了 API key（HTTP %d）。", c.ID, code), Actions: []string{ActionSetAPIKey},
	}
	switch {
	case code == http.StatusUnauthorized || (code == http.StatusBadRequest && badKey):
		return keyRejected
	case code == http.StatusBadRequest || code == http.StatusForbidden:
		// GEM-R7：影片太長、無法存取等。訊息中的狀態名稱一律是 google.rpc.Code 的字元，不是 provider 的任意文字。
		name := status
		if name == "" {
			name = http.StatusText(code)
		}
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: detail,
			Message: fmt.Sprintf("Gemini 無法處理這部影片（HTTP %d %s），請改用一般流程。", code, name),
			Actions: []string{ActionGeminiFallback},
		}
	case code == http.StatusNotFound:
		return &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: detail,
			Message: fmt.Sprintf("模型 %s 已停用或不存在。", model), Actions: []string{ActionChangeModel},
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

// geminiSafetyReasons 是因為內容政策而停止產生的 finishReason：視為沒有結構化輸出（同 openai 的 refusal），交給驗證重試（GEM G8）。
var geminiSafetyReasons = map[string]bool{
	"SAFETY": true, "RECITATION": true, "BLOCKLIST": true, "PROHIBITED_CONTENT": true, "SPII": true, "IMAGE_SAFETY": true,
}

// decodeGemini 取出第一個 candidate 的文字（responseMimeType 為 application/json 時是 JSON 字串）。
// 輸入本身被拒絕（promptFeedback.blockReason）時同一個請求重送也不會通過：needs_action，動作「改用一般流程重新處理」。
func (c *HTTPClient) decodeGemini(raw []byte) (Response, error) {
	var v struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		UsageMetadata struct {
			PromptTokenCount        int `json:"promptTokenCount"`
			CandidatesTokenCount    int `json:"candidatesTokenCount"`
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
			ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return Response{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "llm:" + c.ID, Detail: "回應不是預期的 JSON", Err: err}
	}
	u := v.UsageMetadata
	// 思考的 token 與輸出同價計費，算在輸出（LLM-R10）；快取讀取包含在 promptTokenCount 中（同 openai 的對應）。
	res := Response{Usage: Usage{
		InputTokens: max(u.PromptTokenCount, 0), OutputTokens: max(u.CandidatesTokenCount, 0) + max(u.ThoughtsTokenCount, 0),
		CacheReadTokens: max(u.CachedContentTokenCount, 0),
	}}
	if br := v.PromptFeedback.BlockReason; br != "" {
		name := br
		if !geminiStatusPattern.MatchString(name) {
			name = "OTHER"
		}
		return res, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: "llm:" + c.ID, Detail: "promptFeedback.blockReason " + name,
			Message: fmt.Sprintf("Gemini 拒絕處理這部影片（%s），請改用一般流程。", name), Actions: []string{ActionGeminiFallback},
		}
	}
	if len(v.Candidates) == 0 {
		res.Problem = "the response has no candidates"
		return res, nil
	}
	cand := v.Candidates[0]
	switch {
	case geminiSafetyReasons[cand.FinishReason]:
		res.Problem = "the model stopped for safety (finishReason " + cand.FinishReason + ")"
		return res, nil
	case cand.FinishReason == "MAX_TOKENS":
		res.Problem = "the output was cut off by the token limit"
		return res, nil
	}
	var text strings.Builder
	for _, p := range cand.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		res.Problem = "the response has no content"
		return res, nil
	}
	res.Output = []byte(text.String())
	return res, nil
}
