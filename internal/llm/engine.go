package llm

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// RunOptions 是執行一個 chunk 的輸入。
type RunOptions struct {
	Client Client
	Spec   *Spec
	Model  string
	Fixed  Fixed
	Input  ChunkInput
	// MaxRetries 是驗證失敗後最多再試的次數（llm.max_validation_retries，LLM-R5）。
	MaxRetries int
	// BeforeRequest 在每次送出請求之前呼叫（限流用）；attempt 從 0 起算，0 是第一次。回傳錯誤時不送出，RunChunk 回傳該錯誤。
	BeforeRequest func(ctx context.Context, attempt int, req Request) error
	// Timeout 是整個 chunk（含驗證重試）的時間上限，0 代表沒有。呼叫端設成「租約剩下的時間減去安全邊際」：
	// 到期時回傳 retryable，任務放回佇列，而不是讓租約失效、被重新領取而重複送出（重複計費）。
	Timeout time.Duration
}

// ChunkResult 是一個 chunk 的結果。Output 非 nil 代表通過驗證；
// 重試用盡仍失敗時 Output 是 nil，Fallback 是最後一次的驗證錯誤，由步驟依自己的退回規則處理（LLM-R5）。
type ChunkResult struct {
	Output   json.RawMessage
	Fallback *ValidationError
	Usage    Usage
	Requests int
}

// RunChunk 送出一個 chunk：驗證失敗時把錯誤說明附在下一次請求中，立刻重試，最多 MaxRetries 次（LLM-R4、R5）。
// provider 的錯誤（429、5xx、404…）不算驗證失敗，直接回傳，由呼叫端依錯誤類別處理（LLM-R6）。
func RunChunk(ctx context.Context, o RunOptions) (ChunkResult, error) {
	if o.Client == nil || o.Spec == nil {
		return ChunkResult{}, errors.New("llm: RunChunk 需要 Client 與 Spec")
	}
	data, err := DataBlock(o.Input.Chunk.Index, o.Input.Chunks, o.Input.Context, o.Input.Cues)
	if err != nil {
		return ChunkResult{}, err
	}
	req := Request{Model: o.Model, MaxTokens: OutputBudget(o.Input), Spec: o.Spec, Fixed: o.Fixed, Data: data}
	var res ChunkResult
	parent := ctx
	if o.Timeout != 0 {
		if o.Timeout < 0 {
			return res, errChunkTimeout()
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	for attempt := 0; attempt <= max(o.MaxRetries, 0); attempt++ {
		if ctx.Err() != nil && parent.Err() == nil {
			return res, errChunkTimeout()
		}
		if o.BeforeRequest != nil {
			if err := o.BeforeRequest(ctx, attempt, req); err != nil {
				return res, err
			}
		}
		resp, err := o.Client.Complete(ctx, req)
		res.Requests++
		if err != nil {
			if ctx.Err() != nil && parent.Err() == nil {
				// 是這個 chunk 自己的時間上限到了，不是 worker 停止。
				return res, errChunkTimeout()
			}
			return res, err
		}
		res.Usage.Add(resp.Usage)
		var verr *ValidationError
		if resp.Output == nil {
			verr = &ValidationError{Reason: ReasonInvalidJSON, Message: "output was not valid JSON: " + resp.Problem}
		} else {
			var out json.RawMessage
			out, verr = o.Spec.Validate(o.Input, resp.Output)
			if verr == nil {
				res.Output, res.Fallback = out, nil
				return res, nil
			}
		}
		res.Fallback = verr
		req.Retry = RetryNote(verr)
	}
	return res, nil
}

// errChunkTimeout 是 chunk 超過 RunOptions.Timeout：retryable，任務放回佇列。
func errChunkTimeout() error {
	return &errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "chunk 超過租約內的時間上限"}
}

// OutputBudget 是請求的 max_tokens：輸出約與輸入的字幕同長，加上 JSON 的額外負擔，留兩倍再加 1024，介於 1024 與 32000 之間。
func OutputBudget(in ChunkInput) int {
	n := 0
	for _, c := range in.Cues {
		n += EstimateTokens(c.Text) + 4
	}
	return min(max(2*n+1024, 1024), 32000)
}

// RequestTokens 估算一個請求的輸入 token 數，給 TPM 限流預扣用。
func RequestTokens(req Request) int {
	return EstimateText(req.Fixed.System) + EstimateText(req.Fixed.Prefix) + EstimateText(req.Data) + EstimateText(req.Retry)
}
