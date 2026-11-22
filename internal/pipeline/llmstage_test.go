package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/worker"
)

// staticPrompts 是 PromptResolver 的替身：每個引用都解析成同一個第 1 版。
type staticPrompts struct{}

func (staticPrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	return ResolvedPrompt{ID: id, Kind: StageCorrect, Version: 1, SHA256: "sha-v1", Body: "只修正錯字。{{title}}"}, nil
}

var chunkRE = regexp.MustCompile(`"chunk":(\d+)`)

// chunkLLM 是依 chunk 編號回答的假 provider：behave[chunk] 是 "echo"（預設）、"drop"（少一句）或 "404"。
type chunkLLM struct {
	mu     sync.Mutex
	behave map[int]string
	calls  []int
	// before 在處理 chunk n 之前呼叫（例如等到其他 chunk 讓步驟 blocked）；可以是 nil。
	before func(n int)
	// deadlines 記下每個請求的 ctx 剩下的時間（沒有期限時是 0）。
	deadlines []time.Duration
	// fixed 記下每個請求的固定前綴。
	fixed []llm.Fixed
}

func (f *chunkLLM) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	m := chunkRE.FindStringSubmatch(req.Data)
	n, _ := strconv.Atoi(m[1])
	var left time.Duration
	if d, ok := ctx.Deadline(); ok {
		left = time.Until(d)
	}
	f.mu.Lock()
	f.calls = append(f.calls, n)
	f.fixed = append(f.fixed, req.Fixed)
	f.deadlines = append(f.deadlines, left)
	b, before := f.behave[n], f.before
	f.mu.Unlock()
	if before != nil {
		before(n)
	}
	if b == "429" {
		return llm.Response{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "HTTP 429", RetryAfter: time.Second}
	}
	if b == "404" {
		return llm.Response{}, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "llm", Message: "模型 m 已停用或不存在。", Actions: []string{llm.ActionChangeModel}}
	}
	var data struct {
		Cues []llm.Cue `json:"cues"`
	}
	raw := req.Data[len(llm.DataOpen) : len(req.Data)-len(llm.DataClose)]
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return llm.Response{}, err
	}
	if b == "drop" {
		data.Cues = data.Cues[:len(data.Cues)-1]
	}
	for i := range data.Cues {
		if b == "rewrite" {
			data.Cues[i].Text = "zzzz yyyy xxxx wwww vvvv" // 和原文完全不像（COR-R2）
			continue
		}
		data.Cues[i].Text += "!"
	}
	terms := []map[string]string{}
	if b == "terms" {
		for _, t := range []string{"KRaft", "Apache Kafka", "\x1b[31mred\n", "kraft"} {
			terms = append(terms, map[string]string{"term": t})
		}
	}
	out, _ := json.Marshal(map[string]any{"cues": data.Cues, "term_candidates": terms})
	return llm.Response{Output: out}, nil
}

func (f *chunkLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newLLMRig 建立 llm 並行數為 1 的 rig（請求依 chunk 順序），影音已有 4 句的 segmented，每句一個 chunk。
func newLLMRig(t *testing.T, fake *chunkLLM) (*rig, store.Item) {
	t.Helper()
	return newLLMRigCap(t, fake, 1)
}

func newLLMRigCap(t *testing.T, fake *chunkLLM, llmCap int) (*rig, store.Item) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", ChannelID: "UC1", Channel: "ch"}}
	q := queue.New(st, clk, "w1")
	q.Control().SetCap(queue.ResourceLLM, llmCap)
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: staticPrompts{}, LLM: reg})
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	over := map[string]any{
		"stages": map[string]any{"correct": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "correct-default"}},
		"llm":    map[string]any{"chunk": map[string]any{"max_tokens": 1, "overlap_cues": 0, "context_cues": 1}, "max_validation_retries": 1},
	}
	it := r.ingest(watch, over, "normalize")
	r.run()
	seedVariant(t, r, it, subtitle.VariantSegmented, "en", "alpha", "bravo", "charlie", "delta")
	return r, it
}

func rerunCorrect(t *testing.T, r *rig, it store.Item, chunks ...int) {
	t.Helper()
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageCorrect, Until: StageCorrect, Chunks: chunks}); err != nil {
		t.Fatalf("Rerun：%v", err)
	}
	r.run()
}

// LLM-R5、R8：退回的 chunk 記在 fallbacks；產出不寫入步驟層的快取鍵，所以不指定 chunks 的重跑也只重新送出退回的 chunk，
// 之後全部通過時步驟層的快取命中，不再呼叫 provider。
func TestLLMFallbackIsRetriedOnPlainRerun(t *testing.T) {
	fake := &chunkLLM{behave: map[int]string{2: "drop"}}
	r, it := newLLMRig(t, fake)
	rerunCorrect(t, r, it)
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusDone || st.CacheKey != "" {
		t.Fatalf("有退回時步驟 done、沒有快取鍵：%+v", st)
	}
	// chunk 1、3、4 各 1 次，chunk 2 是 1 + 1 次重試。
	if n := fake.callCount(); n != 5 {
		t.Fatalf("請求數 = %d，預期 5（%v）", n, fake.calls)
	}
	// 每個請求都在租約內的期限之下（租約減去安全邊際）：不讓租約失效後被重新領取而重複計費。
	for i, d := range fake.deadlines {
		if d <= 0 || d > queue.DefaultLease-chunkLeaseMargin {
			t.Errorf("第 %d 個請求的期限剩 %v，預期在 0 與 %v 之間", i+1, d, queue.DefaultLease-chunkLeaseMargin)
		}
	}
	art, _ := r.st.Read().Artifact(context.Background(), st.ArtifactID)
	var prov struct {
		Fallbacks []struct {
			Chunk  int
			Reason string
		}
	}
	_ = json.Unmarshal(art.Provenance, &prov)
	if len(prov.Fallbacks) != 1 || prov.Fallbacks[0].Chunk != 2 || prov.Fallbacks[0].Reason != llm.ReasonCueCount {
		t.Errorf("fallbacks = %+v", prov.Fallbacks)
	}
	tr, _ := readTranscript(r.dir, art.Path, art.SHA256)
	if got := []string{tr.Cues[0].Text, tr.Cues[1].Text, tr.Cues[2].Text}; got[0] != "alpha!" || got[1] != "bravo" || got[2] != "charlie!" {
		t.Errorf("退回的 chunk 用原文、其他用輸出：%v", got)
	}

	fake.mu.Lock()
	fake.behave = map[int]string{}
	fake.mu.Unlock()
	rerunCorrect(t, r, it)
	if n := fake.callCount(); n != 6 {
		t.Fatalf("一般重跑只重新送出退回的 chunk：請求數 = %d，預期 6（%v）", n, fake.calls)
	}
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusDone || st.CacheKey == "" {
		t.Fatalf("全部通過時寫入步驟層的快取鍵：%+v", st)
	}
	rerunCorrect(t, r, it)
	if n := fake.callCount(); n != 6 {
		t.Errorf("步驟層的快取命中時不呼叫 provider：請求數 = %d", n)
	}
	// 指定的 chunk 兩層快取都繞過。
	rerunCorrect(t, r, it, 3)
	if n := fake.callCount(); n != 7 || fake.calls[6] != 3 {
		t.Errorf("chunks: [3] 只重新送出 chunk 3：%v", fake.calls)
	}
	// 超過現行切分的 chunk 總數（4）：422，什麼都不送出（L12）。
	var inv *InvalidError
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageCorrect, Until: StageCorrect, Chunks: []int{2, 5}}); !errors.As(err, &inv) ||
		len(inv.Fields) != 1 || inv.Fields[0].Path != "chunks[1]" || inv.Fields[0].Message != "chunk 5 超過現行切分的 chunk 總數 4。" {
		t.Errorf("chunks: [2, 5]：%v", err)
	}
}

// 一個 chunk 讓步驟 blocked 時，其他還沒領取的 chunk 取消，不會把步驟改回 running 而蓋掉訊息與動作。
func TestLLMBlockedChunkCancelsSiblings(t *testing.T) {
	fake := &chunkLLM{behave: map[int]string{1: "404"}}
	r, it := newLLMRig(t, fake)
	rerunCorrect(t, r, it)
	if n := fake.callCount(); n != 1 {
		t.Errorf("blocked 之後不再送出其他 chunk：%v", fake.calls)
	}
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.Message != "模型 m 已停用或不存在。" ||
		len(st.Actions) != 1 || st.Actions[0] != llm.ActionChangeModel {
		t.Errorf("步驟 = %+v", st)
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskPending); n != 0 {
		t.Errorf("還有 %d 個 pending 任務", n)
	}
}

// 晚到的 chunk 錯誤：chunk 1 讓步驟 blocked 時 chunk 2 已經在執行，之後它的 429（retryable）不能把步驟改回 pending、重新排任務，
// 蓋掉 blocked 的訊息與動作。
func TestLLMLateChunkErrorKeepsBlockedStage(t *testing.T) {
	fake := &chunkLLM{behave: map[int]string{1: "404", 2: "429"}}
	r, it := newLLMRigCap(t, fake, 2)
	fake.before = func(n int) {
		if n != 2 {
			return
		}
		// 等 chunk 1 讓步驟 blocked 之後才回應。
		for range 500 {
			if st := latestStage(t, r, it.ID, StageCorrect); st.Status == store.StatusBlocked {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("chunk 1 沒有讓步驟 blocked")
	}
	rerunCorrect(t, r, it)
	if n := fake.callCount(); n != 2 {
		t.Errorf("請求 = %v，預期 chunk 1、2 各一次", fake.calls)
	}
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || st.Message != "模型 m 已停用或不存在。" || len(st.Actions) != 1 || st.Actions[0] != llm.ActionChangeModel {
		t.Errorf("步驟 = %+v", st)
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskPending); n != 0 {
		t.Errorf("晚到的錯誤不重新排任務：還有 %d 個 pending 任務", n)
	}
}
