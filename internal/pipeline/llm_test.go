package pipeline

import (
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func TestEffectiveLLM(t *testing.T) {
	got, err := Effective{}.LLM()
	if err != nil || got.Chunk != llm.DefaultChunkParams() || got.MaxValidationRetries != llm.DefaultMaxValidationRetries {
		t.Errorf("預設值 = %+v %v", got, err)
	}
	got, err = Effective{"llm": map[string]any{"chunk": map[string]any{"max_tokens": 100.0, "overlap_cues": 0.0}, "max_validation_retries": 0.0}}.LLM()
	if err != nil || got.Chunk != (llm.ChunkParams{MaxTokens: 100, OverlapCues: 0, ContextCues: llm.DefaultContextCues}) || got.MaxValidationRetries != 0 {
		t.Errorf("覆寫 = %+v %v", got, err)
	}
}

// LLM-R6：retryable 錯誤帶 Retry-After 時依它排下一次；LLM-R7：限流的 wait 用錯誤指定的時間與訊息，不計入次數。
func TestClassifyUsesRetryAfter(t *testing.T) {
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	p := &Pipeline{clock: clock.NewFake(t0)}
	res := p.classify(store.Task{Attempts: 1, MaxAttempts: 3}, &errclass.Error{Class: errclass.ClassRetryable, RetryAfter: 45 * time.Second})
	if res.task != taskRequeue || !res.countAttempt || !res.notBefore.Equal(t0.Add(45*time.Second)) {
		t.Errorf("retryable = %+v", res)
	}
	res = p.classify(store.Task{Attempts: 1, MaxAttempts: 3}, &errclass.Error{Class: errclass.ClassRetryable})
	if !res.notBefore.Equal(t0.Add(30 * time.Second)) {
		t.Errorf("沒有 Retry-After 時用退避：%v", res.notBefore)
	}
	res = p.classify(store.Task{Attempts: 1, MaxAttempts: 3}, &errclass.Error{Class: errclass.ClassWait, RetryAfter: time.Minute, Message: "等待"})
	if res.task != taskRequeue || res.countAttempt || !res.notBefore.Equal(t0.Add(time.Minute)) || res.state.Message != "等待" || res.state.Status != store.StatusWaiting {
		t.Errorf("wait = %+v", res)
	}
}

func TestReplaceCueTexts(t *testing.T) {
	in := subtitle.Transcript{Lang: "en", Cues: []subtitle.Cue{{Index: 1, StartMS: 1000, EndMS: 2000, Text: "a"}, {Index: 2, StartMS: 2000, EndMS: 3000, Text: "b"}}}
	out, err := replaceCueTexts(in, []llm.Cue{{I: 1, Text: "A"}, {I: 2, Text: "B"}})
	if err != nil || out.Cues[0].Text != "A" || out.Cues[1].Text != "B" || out.Cues[1].StartMS != 2000 || in.Cues[0].Text != "a" {
		t.Errorf("out = %+v（輸入 %+v） %v", out, in, err)
	}
	if _, err := replaceCueTexts(in, []llm.Cue{{I: 1, Text: "A"}}); err == nil {
		t.Error("句數不同時應該失敗")
	}
	if _, err := replaceCueTexts(in, []llm.Cue{{I: 2, Text: "A"}, {I: 1, Text: "B"}}); err == nil {
		t.Error("句號不依序時應該失敗")
	}
}

// chunk 的時間上限在租約之內：單次請求的逾時 < 租約減去安全邊際 < 租約；租約快到期時不送出請求。
func TestChunkTimeoutFitsInLease(t *testing.T) {
	if !(llm.RequestTimeout < queue.DefaultLease-chunkLeaseMargin) {
		t.Fatalf("RequestTimeout %v 必須小於租約 %v 減去 %v", llm.RequestTimeout, queue.DefaultLease, chunkLeaseMargin)
	}
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	p := &Pipeline{clock: clk}
	until := clk.Now().Add(queue.DefaultLease)
	if got := p.chunkTimeout(store.Task{LeaseUntil: &until}); got != queue.DefaultLease-chunkLeaseMargin {
		t.Errorf("剛領取：%v", got)
	}
	soon := clk.Now().Add(30 * time.Second)
	if got := p.chunkTimeout(store.Task{LeaseUntil: &soon}); got >= 0 {
		t.Errorf("租約剩不到安全邊際時回傳負數：%v", got)
	}
}

// 登記的 Spec 在啟動時檢查：schema 不符合 strict 模式、步驟名稱不一致時是錯誤（init 會 panic）。
func TestCheckLLMStages(t *testing.T) {
	if err := checkLLMStages(llmStages); err != nil {
		t.Fatalf("目前的登記：%v", err)
	}
	loose := func() *llm.Spec {
		return &llm.Spec{Stage: StageCorrect, Schema: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}}
	}
	if err := checkLLMStages(map[string]llmStage{StageCorrect: {spec: loose}}); err == nil {
		t.Error("schema 沒有 additionalProperties: false、required 不完整時應該拒絕")
	}
	if err := checkLLMStages(map[string]llmStage{StageSegment: {spec: llm.CorrectSpec}}); err == nil {
		t.Error("步驟名稱不一致時應該拒絕")
	}
}
