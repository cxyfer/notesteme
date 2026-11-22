package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
)

// scripted 是依序回答的假 client。
type scripted struct {
	answers []Response
	errs    []error
	reqs    []Request
}

func (s *scripted) Complete(_ context.Context, req Request) (Response, error) {
	s.reqs = append(s.reqs, req)
	i := len(s.reqs) - 1
	if i < len(s.errs) && s.errs[i] != nil {
		return Response{}, s.errs[i]
	}
	return s.answers[min(i, len(s.answers)-1)], nil
}

func runOpts(c Client, retries int) RunOptions {
	return RunOptions{Client: c, Spec: CorrectSpec(), Model: "m", Fixed: Fixed{System: "S", Prefix: "P"}, Input: correctInput(2), MaxRetries: retries}
}

const okOutput = `{"cues":[{"i":1,"text":"a"},{"i":2,"text":"b"}]}`

// LLM-R4、R5：驗證失敗時附上錯誤說明立刻重試；說明放在資料區塊之後，固定前綴不變。
func TestRunChunkRetriesWithTheError(t *testing.T) {
	c := &scripted{answers: []Response{{Problem: "no tool_use", Usage: Usage{InputTokens: 1}}, {Output: []byte(okOutput), Usage: Usage{InputTokens: 2, OutputTokens: 3}}}}
	var attempts []int
	o := runOpts(c, 2)
	o.BeforeRequest = func(_ context.Context, attempt int, _ Request) error {
		attempts = append(attempts, attempt)
		return nil
	}
	res, err := RunChunk(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Output) != okOutput || res.Fallback != nil || res.Requests != 2 || res.Usage.InputTokens != 3 || res.Usage.OutputTokens != 3 {
		t.Errorf("結果 = %+v", res)
	}
	if c.reqs[0].Retry != "" || !strings.Contains(c.reqs[1].Retry, "output was not valid JSON") {
		t.Errorf("第二次請求應該附上錯誤說明：%q", c.reqs[1].Retry)
	}
	if c.reqs[0].Fixed != c.reqs[1].Fixed || c.reqs[0].Data != c.reqs[1].Data {
		t.Error("重試不應改變固定前綴與資料區塊")
	}
	if len(attempts) != 2 || attempts[0] != 0 || attempts[1] != 1 {
		t.Errorf("BeforeRequest 的 attempt = %v", attempts)
	}
}

// 重試用盡（1 + max_validation_retries 次）後回傳最後一次的驗證錯誤，由步驟退回。
func TestRunChunkFallsBackAfterTheLastRetry(t *testing.T) {
	c := &scripted{answers: []Response{{Output: []byte(`{"cues":[{"i":1,"text":"a"}]}`)}}}
	res, err := RunChunk(context.Background(), runOpts(c, 2))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != nil || res.Fallback == nil || res.Fallback.Reason != ReasonCueCount || res.Requests != 3 {
		t.Errorf("結果 = %+v", res)
	}
	c = &scripted{answers: []Response{{Problem: "x"}}}
	if res, _ := RunChunk(context.Background(), runOpts(c, 0)); res.Requests != 1 || res.Fallback == nil {
		t.Errorf("max_validation_retries 為 0 時只送一次：%+v", res)
	}
}

// provider 的錯誤（429 等）直接回傳，不算驗證重試（LLM-R6）；BeforeRequest 的錯誤（限流）不送出請求。
func TestRunChunkReturnsProviderErrors(t *testing.T) {
	rate := &errclass.Error{Class: errclass.ClassRetryable}
	c := &scripted{errs: []error{rate}, answers: []Response{{Output: []byte(okOutput)}}}
	if _, err := RunChunk(context.Background(), runOpts(c, 2)); !errors.Is(err, rate) || len(c.reqs) != 1 {
		t.Errorf("err = %v，請求數 %d", err, len(c.reqs))
	}
	wait := errors.New("wait")
	c = &scripted{answers: []Response{{Output: []byte(okOutput)}}}
	o := runOpts(c, 2)
	o.BeforeRequest = func(context.Context, int, Request) error { return wait }
	if _, err := RunChunk(context.Background(), o); !errors.Is(err, wait) || len(c.reqs) != 0 {
		t.Errorf("限流時不該送出請求：%v，請求數 %d", err, len(c.reqs))
	}
}

func TestOutputBudget(t *testing.T) {
	if got := OutputBudget(ChunkInput{}); got != 1024 {
		t.Errorf("空的 chunk = %d", got)
	}
	big := ChunkInput{Cues: make([]Cue, 10000)}
	for i := range big.Cues {
		big.Cues[i].Text = strings.Repeat("字", 10)
	}
	if got := OutputBudget(big); got != 32000 {
		t.Errorf("上限 = %d", got)
	}
}
