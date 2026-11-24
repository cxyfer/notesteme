package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/worker"
)

// recLLM 記下一般 API 收到的原始請求，回答交給 inner。
type recLLM struct {
	mu    sync.Mutex
	inner llm.Client
	reqs  []llm.Request
}

func (f *recLLM) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	return f.inner.Complete(ctx, req)
}

// trnBatchConfig 是 newTrnBatchRig 的選項。
type trnBatchConfig struct {
	regular *trnLLM        // 一般 API（nil 代表 echo）
	batch   *trnLLM        // batch 的回答（nil 代表 echo）
	chunk   map[string]any // llm.chunk
	retries int            // llm.max_validation_retries
	// priority 是從 translate 重跑時的優先序（預設 subscription）。
	priority int
	texts    []string
}

// trnBatchRig 是 translate 設 batch: auto 的 rig：影音已有 corrected（en），以 priority 從 translate 重跑到 zh-Hant-TW。
// 計價同 newBatchRig：輸入 $1／MTok，一般 API 每個請求 10000 token 時 $0.01，batch 半價。
type trnBatchRig struct {
	*batchRig
	regular *recLLM
}

func newTrnBatchRig(t *testing.T, c trnBatchConfig) *trnBatchRig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := cost.New(clk, nil, cost.Options{})
	err = st.Tx(ctx, func(r *store.Repo) error {
		for unit, usd := range map[string]float64{cost.UnitInputMTok: 1, cost.UnitOutputMTok: 0} {
			if _, err := svc.PutPrice(ctx, r, map[string]any{"provider": "anthropic", "model": "m", "unit_type": unit, "usd_per_unit": usd}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.regular == nil {
		c.regular = &trnLLM{}
	}
	if c.batch == nil {
		c.batch = &trnLLM{}
	}
	regular := &recLLM{inner: usageLLM{c.regular}}
	api := &fakeBatchAPI{client: c.batch, state: llm.BatchInProgress}
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", ChannelID: "UC1", Channel: "ch"}}
	q := queue.New(st, clk, "w1")
	q.Control().SetCap(queue.ResourceLLM, 1)
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return regular }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: trnPrompts{}, LLM: reg, SponsorBlock: &fakeSponsor{}, Cost: svc})
	p.SetBatchAPI(func(context.Context, string) (BatchAPI, error) { return api, nil })
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	it := r.ingest(watch, nil, "normalize")
	r.run()
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", c.texts...)
	l := map[string]any{"max_validation_retries": c.retries}
	if c.chunk != nil {
		l["chunk"] = c.chunk
	}
	over := withMap(langOver([]string{"zh-Hant-TW"}, ""), map[string]any{
		"stages": map[string]any{"translate": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "translate-default", "batch": "auto"}},
		"llm":    l,
	})
	prio := c.priority
	if prio == 0 {
		prio = queue.PrioritySubscription
	}
	if _, err := p.Rerun(ctx, it.ID, RerunRequest{From: StageTranslate, Overrides: over, Priority: prio}); err != nil {
		t.Fatal(err)
	}
	r.run()
	return &trnBatchRig{batchRig: &batchRig{rig: r, api: api, svc: svc, it: it}, regular: regular}
}

// usageLLM 讓每個回應帶 10000 個輸入 token（計價用）。
type usageLLM struct{ inner llm.Client }

func (u usageLLM) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := u.inner.Complete(ctx, req)
	resp.Usage.InputTokens = 10000
	return resp, err
}

func (b *trnBatchRig) regularRequests() []llm.Request {
	b.regular.mu.Lock()
	defer b.regular.mu.Unlock()
	return slices.Clone(b.regular.reqs)
}

// translation 回傳 zh-Hant-TW 的譯文與來源紀錄；translate 必須是 done。
func (b *trnBatchRig) translation() ([]string, bool, []provChunk, string) {
	b.t.Helper()
	st := latestStage(b.t, b.rig, b.it.ID, StageTranslate)
	if st.Status != store.StatusDone {
		b.t.Fatalf("translate = %+v", st)
	}
	art, tr := translated(b.t, b.rig, b.it.ID, "zh-Hant-TW")
	var prov struct {
		Batch  bool        `json:"batch"`
		Chunks []provChunk `json:"chunks"`
	}
	if err := json.Unmarshal(art.Provenance, &prov); err != nil {
		b.t.Fatal(err)
	}
	return cueTexts(tr), prov.Batch, prov.Chunks, string(art.Provenance)
}

// costOutcomes 統計帳本：batch 的紀錄前面加 batch/。
func (b *trnBatchRig) costOutcomes() map[string]int {
	out := map[string]int{}
	for _, r := range b.costRows() {
		key := r.Outcome
		if strings.Contains(r.UnitPriceJSON, `"batch":true`) {
			key = "batch/" + key
		}
		out[key]++
	}
	return out
}

// BATCH-R1～R4、R6、R8：translate 的 chunk 也送成一個 batch（同一輪的兩個 chunk 在同一個 batch），等待時不送一般 API；
// 結束後分回各句完成，以 batch 單價記帳，來源紀錄記 mode: batch。
func TestBatchTranslateSubmitAndComplete(t *testing.T) {
	b := newTrnBatchRig(t, trnBatchConfig{
		retries: 1, chunk: map[string]any{"max_tokens": 1, "overlap_cues": 0, "context_cues": 1},
		texts: []string{"We store events in a log.", "Then we read them back."},
	})
	if st := latestStage(t, b.rig, b.it.ID, StageTranslate); st.Status != store.StatusWaiting || !strings.HasPrefix(st.Message, "已送出 Batch API") {
		t.Fatalf("translate = %+v", st)
	}
	if len(b.api.submitted) != 1 || len(b.api.submitted[0]) != 2 || len(b.regularRequests()) != 0 {
		t.Fatalf("batch = %d 個（%d 個請求），一般 API %d", len(b.api.submitted), b.api.requests(), len(b.regularRequests()))
	}
	if b.reserved() <= 0 {
		t.Errorf("送出前要以 batch 單價預留（BATCH-R7）：%v", b.reserved())
	}
	b.api.state = llm.BatchEnded
	if _, done := b.poll(); !done {
		t.Fatal("batch 結束後不再輪詢")
	}
	texts, viaBatch, chunks, _ := b.translation()
	if !viaBatch || reasons(chunks) != "batch:,batch:" || chunks[0].BatchID != "ext_1" || chunks[0].SubmittedAt == nil || chunks[0].EndedAt == nil {
		t.Fatalf("來源紀錄 = %v %+v", viaBatch, chunks)
	}
	if !strings.Contains(texts[0], "第1段第1句") || !strings.Contains(texts[1], "第2段第2句") {
		t.Errorf("譯文 = %q", texts)
	}
	if got := b.costOutcomes(); got["batch/ok"] != 2 || len(got) != 1 || b.reserved() != 0 {
		t.Fatalf("帳本 = %v reserved=%v", got, b.reserved())
	}
	if len(b.regularRequests()) != 0 {
		t.Errorf("一般 API 收到 %d 個請求", len(b.regularRequests()))
	}
}

// BATCH-R2：batch 中的請求與一般 API 的第一個請求完全相同（同一份 RunOptions）。
func TestBatchTranslateRequestMatchesRegular(t *testing.T) {
	texts := []string{"We store events in a log.", "Then we read them back."}
	b := newTrnBatchRig(t, trnBatchConfig{retries: 1, texts: texts})
	m := newTrnBatchRig(t, trnBatchConfig{retries: 1, texts: texts, priority: queue.PriorityManual})
	if len(m.api.submitted) != 0 {
		t.Fatal("手動重跑不用 batch")
	}
	regular := m.regularRequests()
	if len(b.api.submitted) != 1 || len(b.api.submitted[0]) != 1 || len(regular) != 1 {
		t.Fatalf("batch %d、一般 API %d", len(b.api.submitted), len(regular))
	}
	got, want := b.api.submitted[0][0].Request, regular[0]
	if got.Model != want.Model || got.MaxTokens != want.MaxTokens || got.Data != want.Data || got.Retry != want.Retry ||
		!reflect.DeepEqual(got.Fixed, want.Fixed) || got.Spec.Stage != want.Spec.Stage || !reflect.DeepEqual(got.Spec.Schema, want.Spec.Schema) ||
		got.Spec.OutputRules != want.Spec.OutputRules {
		t.Fatalf("batch 的請求與一般 API 不同：\nbatch   %+v\nregular %+v", got, want)
	}
}

// TRN-R4、BATCH-R4：batch 的結果通過驗證但句群的切分標記不符時，帶著 batch 的結果改走一般 API，
// 只補送逐 cue 的請求（不帶 retry note、不重送第一個請求）；chunk 記 mode: batch。
func TestBatchTranslateMissingMarkerSendsOnlyPerCue(t *testing.T) {
	b := newTrnBatchRig(t, trnBatchConfig{
		retries: 1,
		batch: &trnLLM{answer: func(int, trnReq) []llm.Cue {
			return []llm.Cue{{I: 1, Text: "我們之所以改用 Kafka，是因為舊的佇列跟不上。"}}
		}},
		regular: &trnLLM{answer: func(int, trnReq) []llm.Cue {
			return []llm.Cue{{I: 1, Text: "我們當初之所以決定改用 Kafka，"}, {I: 2, Text: "是因為舊的佇列已經跟不上了。"}}
		}},
		texts: []string{"The reason we moved to Kafka was that", "our old queue could not keep up."},
	})
	b.api.state = llm.BatchEnded
	b.poll()
	reqs := b.regularRequests()
	if len(reqs) != 1 || reqs[0].Retry != "" || strings.Contains(reqs[0].Data, llm.SplitMarker) {
		t.Fatalf("一般 API 只補送一次逐 cue 的請求：%+v", reqs)
	}
	texts, viaBatch, chunks, _ := b.translation()
	if !slices.Equal(texts, []string{"我們當初之所以決定改用 Kafka，", "是因為舊的佇列已經跟不上了。"}) {
		t.Errorf("譯文 = %q", texts)
	}
	if !viaBatch || reasons(chunks) != "batch:" || chunks[0].BatchID == "" {
		t.Errorf("來源紀錄 = %v %+v", viaBatch, chunks)
	}
	if got := b.costOutcomes(); got["batch/ok"] != 1 || got["ok"] != 1 || len(got) != 2 || b.reserved() != 0 {
		t.Errorf("帳本 = %v reserved=%v", got, b.reserved())
	}
}

// BATCH-R4、LLM-R5：batch 的譯文沒有通過驗證時改走一般 API，第一個請求就附上驗證錯誤的說明（計入驗證重試）。
func TestBatchTranslateInvalidFallsBackWithRetry(t *testing.T) {
	b := newTrnBatchRig(t, trnBatchConfig{
		retries: 1,
		batch: &trnLLM{answer: func(int, trnReq) []llm.Cue {
			return []llm.Cue{{I: 1, Text: "我們把事件儲存在日誌裡。"}, {I: 2, Text: ""}}
		}},
		texts: []string{"We store events in a log.", "Then we read them back."},
	})
	b.api.state = llm.BatchEnded
	b.poll()
	reqs := b.regularRequests()
	if len(reqs) != 1 || reqs[0].Retry == "" {
		t.Fatalf("一般 API 送一次、附上驗證錯誤：%+v", reqs)
	}
	_, viaBatch, chunks, _ := b.translation()
	if viaBatch || reasons(chunks) != "api:invalid_output" {
		t.Errorf("來源紀錄 = %v %s", viaBatch, reasons(chunks))
	}
	if got := b.costOutcomes(); got["batch/invalid_output"] != 1 || got["ok"] != 1 || len(got) != 2 {
		t.Errorf("帳本 = %v", got)
	}
}

// TRN-R7、BATCH-R4：沒有驗證重試額度時，batch 的結果就地處理：只有沒通過驗證的那一句退回原文，不送一般 API。
func TestBatchTranslateInvalidWithoutRetriesKeepsPassingCues(t *testing.T) {
	b := newTrnBatchRig(t, trnBatchConfig{
		retries: 0,
		batch: &trnLLM{answer: func(int, trnReq) []llm.Cue {
			return []llm.Cue{{I: 1, Text: "我們把事件儲存在日誌裡。"}, {I: 2, Text: ""}}
		}},
		texts: []string{"We store events in a log.", "Then we read them back."},
	})
	b.api.state = llm.BatchEnded
	b.poll()
	if n := len(b.regularRequests()); n != 0 {
		t.Fatalf("一般 API 收到 %d 個請求", n)
	}
	texts, viaBatch, chunks, prov := b.translation()
	if !slices.Equal(texts, []string{"我們把事件儲存在日誌裡。", "Then we read them back."}) {
		t.Errorf("譯文 = %q", texts)
	}
	if !viaBatch || reasons(chunks) != "batch:" || !strings.Contains(prov, `"fallbacks":[{"cue":2`) {
		t.Errorf("來源紀錄 = %v %s：%s", viaBatch, reasons(chunks), prov)
	}
}

// BATCH-R2、R8：送出失敗時 translate 的 chunk 改走一般 API（submit_failed）。
func TestBatchTranslateSubmitFailureFallsBack(t *testing.T) {
	b := newTrnBatchRig(t, trnBatchConfig{retries: 1, texts: []string{"We store events in a log."}})
	if len(b.api.submitted) != 1 {
		t.Fatal("先送出一次")
	}
	// 另一部影音的送出失敗：直接用 rig 的 batch 改成失敗，重新從 translate 以訂閱優先序重跑。
	b.api.submitErr = errors.New("HTTP 503")
	over := withMap(langOver([]string{"zh-Hant-TW"}, ""), map[string]any{
		"stages": map[string]any{"translate": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "translate-default", "batch": "auto"}},
	})
	if _, err := b.p.Rerun(context.Background(), b.it.ID, RerunRequest{From: StageTranslate, Overrides: over, Priority: queue.PriorityBackfill}); err != nil {
		t.Fatal(err)
	}
	b.run()
	_, viaBatch, chunks, _ := b.translation()
	if viaBatch || reasons(chunks) != "api:submit_failed" || len(b.regularRequests()) != 1 {
		t.Fatalf("來源紀錄 = %v %s，一般 API %d", viaBatch, reasons(chunks), len(b.regularRequests()))
	}
}
