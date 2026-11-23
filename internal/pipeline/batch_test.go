package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"database/sql"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/worker"
)

// fakeBatchAPI 是假的 Batch API：每個請求由 answers（另一個 chunkLLM，不計入一般 API 的請求）回答。
type fakeBatchAPI struct {
	mu        sync.Mutex
	answers   *chunkLLM
	submitErr error
	statusErr error
	// state 是 Status 回報的狀態；cancelState 是 Cancel 之後的狀態（空字串代表同 state）。
	state, cancelState string
	// kinds 依 chunk 編號指定結果：succeeded（預設）、errored、expired、canceled、missing（沒有結果）、garbage（不能解碼）。
	kinds      map[int]string
	resultsErr error
	submitted  [][]llm.BatchRequest
	cancels    int
}

func (f *fakeBatchAPI) Submit(_ context.Context, reqs []llm.BatchRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return "", f.submitErr
	}
	f.submitted = append(f.submitted, reqs)
	return "ext_" + strconv.Itoa(len(f.submitted)), nil
}

func (f *fakeBatchAPI) Status(_ context.Context, id string) (llm.BatchStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return llm.BatchStatus{}, f.statusErr
	}
	return llm.BatchStatus{ID: id, State: f.state}, nil
}

func (f *fakeBatchAPI) Cancel(_ context.Context, id string) (llm.BatchStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	st := f.state
	if f.cancelState != "" {
		st = f.cancelState
	}
	return llm.BatchStatus{ID: id, State: st}, nil
}

func (f *fakeBatchAPI) Results(ctx context.Context, _ llm.BatchStatus, fn func(llm.BatchResult) error) error {
	f.mu.Lock()
	all := append([][]llm.BatchRequest(nil), f.submitted...)
	kinds, rerr := f.kinds, f.resultsErr
	f.mu.Unlock()
	if rerr != nil {
		return rerr
	}
	for _, reqs := range all {
		for _, q := range reqs {
			n, _ := strconv.Atoi(chunkRE.FindStringSubmatch(q.Request.Data)[1])
			kind := kinds[n]
			switch kind {
			case "missing":
				continue
			case "garbage":
				if err := fn(llm.BatchResult{CustomID: q.CustomID, Kind: llm.ResultSucceeded, Body: json.RawMessage(`"garbage"`)}); err != nil {
					return err
				}
				continue
			case "", llm.ResultSucceeded:
				resp, err := f.answers.Complete(ctx, q.Request)
				if err != nil {
					return err
				}
				body, _ := json.Marshal(map[string]any{"output": json.RawMessage(resp.Output), "in": 10000})
				if err := fn(llm.BatchResult{CustomID: q.CustomID, Kind: llm.ResultSucceeded, Body: body}); err != nil {
					return err
				}
			default:
				if err := fn(llm.BatchResult{CustomID: q.CustomID, Kind: kind}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (f *fakeBatchAPI) DecodeBatchBody(raw []byte, _ *llm.Spec) (llm.Response, error) {
	var v struct {
		Output json.RawMessage `json:"output"`
		In     int             `json:"in"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return llm.Response{}, &errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "回應不是預期的 JSON"}
	}
	return llm.Response{Output: v.Output, Usage: llm.Usage{InputTokens: v.In}}, nil
}

func (f *fakeBatchAPI) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.submitted {
		n += len(r)
	}
	return n
}

type batchRig struct {
	*rig
	api  *fakeBatchAPI
	fake *costLLM
	svc  *cost.Service
	it   store.Item
}

// batchRigConfig 是 newBatchRig 的選項。
type batchRigConfig struct {
	opts    cost.Options
	budget  map[string]any
	correct map[string]any // 併進 stages.correct
	retries *int           // llm.max_validation_retries（預設 1）
	api     func(*fakeBatchAPI)
	// priority 是新增時的優先序（預設 subscription）。
	priority int
}

// newBatchRig 建立 correct 設定 batch: auto 的影音（4 句、每句一個 chunk），執行到 correct 送出 batch。
// 計價同 newCostRig：一般 API 每個 chunk $0.01，batch $0.005。
func newBatchRig(t *testing.T, c batchRigConfig) *batchRig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := cost.New(clk, nil, c.opts)
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
	fake := &costLLM{}
	api := &fakeBatchAPI{answers: &chunkLLM{}, state: llm.BatchInProgress}
	if c.api != nil {
		c.api(api)
	}
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", ChannelID: "UC1", Channel: "ch", Tracks: []youtube.Track{{Lang: "en", Kind: "manual"}}}}
	q := queue.New(st, clk, "w1")
	q.Control().SetCap(queue.ResourceLLM, 1)
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: staticPrompts{}, LLM: reg, SponsorBlock: &fakeSponsor{}, Cost: svc})
	p.SetBatchAPI(func(context.Context, string) (BatchAPI, error) { return api, nil })
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	br := &batchRig{rig: r, api: api, fake: fake, svc: svc}
	if c.budget != nil {
		b, errs := cost.ParseBudget(c.budget)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		if err := st.Tx(ctx, func(rp *store.Repo) error { return svc.SetBudget(ctx, rp, b) }); err != nil {
			t.Fatal(err)
		}
	}
	correct := map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "correct-default", "batch": "auto"}
	for k, v := range c.correct {
		correct[k] = v
	}
	retries := 1
	if c.retries != nil {
		retries = *c.retries
	}
	over := map[string]any{
		"stages": map[string]any{"segment": map[string]any{"executor": "off"}, "correct": correct},
		"llm":    map[string]any{"chunk": map[string]any{"max_tokens": 1, "overlap_cues": 0, "context_cues": 1}, "max_validation_retries": retries},
	}
	prio := c.priority
	if prio == 0 {
		prio = queue.PrioritySubscription
	}
	res, err := p.Ingest(ctx, IngestRequest{URL: watch, Overrides: over, Until: StageCorrect, Priority: prio})
	if err != nil {
		t.Fatal(err)
	}
	br.it = res[0].Item
	r.run()
	return br
}

// batchID 回傳最近建立的 batch（直接讀資料庫：沒有對應的公開查詢）。
func (b *batchRig) batchID() string {
	b.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(b.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)&mode=ro")
	if err != nil {
		b.t.Fatal(err)
	}
	defer db.Close()
	var id string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM batches ORDER BY rowid DESC LIMIT 1`).Scan(&id); err != nil {
		b.t.Fatal(err)
	}
	return id
}

func (b *batchRig) batch() store.Batch {
	b.t.Helper()
	bt, err := b.st.Read().Batch(context.Background(), b.batchID())
	if err != nil {
		b.t.Fatal(err)
	}
	return bt
}

func (b *batchRig) poll() (time.Time, bool) {
	b.t.Helper()
	next, done, err := b.p.PollBatch(context.Background(), b.batchID(), b.clk.Now())
	if err != nil {
		b.t.Fatal(err)
	}
	b.run()
	return next, done
}

func (b *batchRig) costRows() []store.CostRow {
	b.t.Helper()
	rows, err := b.st.Read().ItemCostRows(context.Background(), b.it.ID)
	if err != nil {
		b.t.Fatal(err)
	}
	return rows
}

func (b *batchRig) reserved() float64 {
	b.t.Helper()
	v, err := b.svc.View(context.Background(), b.st.Read())
	if err != nil {
		b.t.Fatal(err)
	}
	return v.ReservedUSD
}

type provChunk struct {
	Mode           string     `json:"mode"`
	BatchID        string     `json:"batch_id"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	EndedAt        *time.Time `json:"ended_at"`
	FallbackReason string     `json:"fallback_reason"`
}

// provenance 回傳 correct 現行產出的 batch、chunks 與步驟的訊息；correct 必須是 done。
func (b *batchRig) provenance() (bool, []provChunk, string) {
	b.t.Helper()
	st := latestStage(b.t, b.rig, b.it.ID, StageCorrect)
	if st.Status != store.StatusDone {
		b.t.Fatalf("correct = %+v", st)
	}
	art, err := b.st.Read().Artifact(context.Background(), st.ArtifactID)
	if err != nil {
		b.t.Fatal(err)
	}
	var prov struct {
		Batch  bool        `json:"batch"`
		Chunks []provChunk `json:"chunks"`
	}
	if err := json.Unmarshal(art.Provenance, &prov); err != nil {
		b.t.Fatal(err)
	}
	return prov.Batch, prov.Chunks, st.Message
}

func reasons(chunks []provChunk) string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Mode + ":" + c.FallbackReason
	}
	return strings.Join(out, ",")
}

// BATCH-R2、R3、R4、R6、R8：同一輪的 4 個 chunk 送成一個 batch，等待時不送一般 API；結束後照一般 API 的驗證完成，
// 以半價記帳，來源紀錄記下 mode batch。
func TestBatchSubmitWaitAndComplete(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{opts: cost.Options{Estimates: map[string]float64{StageCorrect: 0.01}}})
	st := latestStage(t, b.rig, b.it.ID, StageCorrect)
	if st.Status != store.StatusWaiting || st.Message != "已送出 Batch API，等待結果，最晚 2026-11-24 10:00 UTC。" {
		t.Fatalf("送出之後 = %+v", st)
	}
	if len(b.api.submitted) != 1 || b.api.requests() != 4 || b.fake.callCount() != 0 {
		t.Fatalf("batch = %d 個、請求 %d、一般 API %d", len(b.api.submitted), b.api.requests(), b.fake.callCount())
	}
	bt := b.batch()
	if bt.ExternalID != "ext_1" || bt.State != store.BatchSubmitted || len(bt.Entries) != 4 || !bt.DeadlineAt.Equal(bt.SubmittedAt.Add(24*time.Hour)) || bt.Provider != "anthropic" || bt.Model != "m" {
		t.Fatalf("batches = %+v", bt)
	}
	for _, e := range bt.Entries {
		task, _ := b.st.Read().Task(context.Background(), e.Task)
		if task.State != store.TaskWaiting || task.BatchID != bt.ID || e.Reservation == "" {
			t.Fatalf("任務 = %+v, entry = %+v", task, e)
		}
	}
	// BATCH-R2：custom_id 是任務 ID。
	for i, q := range b.api.submitted[0] {
		if q.CustomID != bt.Entries[i].Task {
			t.Errorf("custom_id = %q，預期 %q", q.CustomID, bt.Entries[i].Task)
		}
	}
	// GET /api/items/:id 的 batch 欄位。
	d, err := b.p.Item(context.Background(), b.it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bv := d.Batches[st.ID]; bv == nil || bv.Provider != "anthropic" || !bv.DeadlineAt.Equal(bt.DeadlineAt) || !bv.SubmittedAt.Equal(bt.SubmittedAt) {
		t.Fatalf("Batches = %+v", d.Batches)
	}
	if got := b.reserved(); got != 0.02 {
		t.Fatalf("以 batch 單價預留 4 × $0.005，實際 %v", got)
	}
	// 還沒結束：5 分鐘後再查。
	b.clk.Advance(5 * time.Minute)
	if next, done := b.poll(); done || !next.Equal(b.clk.Now().Add(batchPollInterval)) {
		t.Fatalf("處理中 = %v, %v", next, done)
	}
	b.api.state = llm.BatchEnded
	if _, done := b.poll(); !done {
		t.Fatal("結束之後輪詢工作要結束")
	}
	viaBatch, chunks, _ := b.provenance()
	if !viaBatch || len(chunks) != 4 {
		t.Fatalf("來源紀錄 = %v %+v", viaBatch, chunks)
	}
	for _, c := range chunks {
		if c.Mode != "batch" || c.BatchID != "ext_1" || c.SubmittedAt == nil || c.EndedAt == nil || c.FallbackReason != "" {
			t.Errorf("chunk = %+v", c)
		}
	}
	if b.fake.callCount() != 0 {
		t.Errorf("一般 API 收到 %d 個請求", b.fake.callCount())
	}
	rows := b.costRows()
	if len(rows) != 4 || b.reserved() != 0 {
		t.Fatalf("成本紀錄 = %+v reserved=%v", rows, b.reserved())
	}
	for _, r := range rows {
		if r.State != store.CostActual || r.Outcome != cost.OutcomeOK || r.USD != 0.005 || !strings.Contains(r.UnitPriceJSON, `"batch":true`) || !strings.Contains(r.UnitPriceJSON, `"batch_discount":0.5`) {
			t.Errorf("紀錄 = %+v", r)
		}
	}
	if bt := b.batch(); bt.State != store.BatchEnded || bt.EndedAt == nil {
		t.Errorf("batch 結束 = %+v", bt)
	}
	// 結束之後再輪詢沒有作用。
	if _, done, err := b.p.PollBatch(context.Background(), b.batchID(), b.clk.Now()); !done || err != nil {
		t.Errorf("結束後 = %v %v", done, err)
	}
	if _, done, err := b.p.PollBatch(context.Background(), "nope", b.clk.Now()); !done || err != nil {
		t.Errorf("不存在的 batch = %v %v", done, err)
	}
}

// BATCH-R2：送出失敗時這些任務改走一般 API（submit_failed），預留刪除，一般 API 以一般單價記帳，不再輪詢。
func TestBatchSubmitFailureFallsBack(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) {
		f.submitErr = &errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "HTTP 529"}
	}})
	viaBatch, chunks, _ := b.provenance()
	if viaBatch || reasons(chunks) != "api:submit_failed,api:submit_failed,api:submit_failed,api:submit_failed" {
		t.Fatalf("來源紀錄 = %v %+v", viaBatch, chunks)
	}
	if b.fake.callCount() != 4 {
		t.Errorf("一般 API 收到 %d 個請求", b.fake.callCount())
	}
	rows := b.costRows()
	if len(rows) != 4 || b.reserved() != 0 {
		t.Fatalf("紀錄 = %+v reserved=%v", rows, b.reserved())
	}
	for _, r := range rows {
		if r.State != store.CostActual || r.USD != 0.01 || strings.Contains(r.UnitPriceJSON, `"batch":true`) {
			t.Errorf("紀錄 = %+v", r)
		}
	}
	if bt := b.batch(); bt.State != store.BatchFailed {
		t.Errorf("batch = %+v", bt)
	}
	if _, err := b.st.Read().ScheduledJob(context.Background(), store.BatchPollKind, b.batchID()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("送出失敗之後不輪詢：%v", err)
	}
}

// BATCH-R1：手動新增（manual）、batch: off、自訂 base_url、agent 與 translate 都不用 batch。
func TestBatchEligibility(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{priority: queue.PriorityManual})
	if len(b.api.submitted) != 0 || latestStage(t, b.rig, b.it.ID, StageCorrect).Status != store.StatusDone {
		t.Fatalf("手動新增不用 batch：%d 個 batch", len(b.api.submitted))
	}
	b = newBatchRig(t, batchRigConfig{correct: map[string]any{"batch": "off"}, priority: queue.PriorityBackfill})
	if len(b.api.submitted) != 0 || latestStage(t, b.rig, b.it.ID, StageCorrect).Status != store.StatusDone {
		t.Fatalf("batch: off 不用 batch：%d 個 batch", len(b.api.submitted))
	}

	p := b.p
	task := store.Task{Executor: queue.ExecutorBuiltin, Priority: queue.PriorityBackfill, ChunkIndex: new(int)}
	run := store.StageRun{Stage: StageCorrect, Executor: ExecutorAPI}
	cfg := StageConfig{Provider: "anthropic"}
	auto := Effective{"stages": map[string]any{StageCorrect: map[string]any{"batch": "auto"}, StageTranslate: map[string]any{"batch": "auto"}}}
	if !p.batchEligible(task, run, cfg, auto, llmPayload{}) {
		t.Fatal("符合條件的任務要用 batch")
	}
	for name, ok := range map[string]bool{
		"已經改走一般 API":     p.batchEligible(task, run, cfg, auto, llmPayload{Batch: &batchFallback{Reason: BatchFallbackTimeout}}),
		"manual":         p.batchEligible(store.Task{Executor: queue.ExecutorBuiltin, Priority: queue.PriorityManual, ChunkIndex: new(int)}, run, cfg, auto, llmPayload{}),
		"agent 任務":       p.batchEligible(store.Task{Executor: ExecutorAgent, Priority: queue.PriorityBackfill, ChunkIndex: new(int)}, run, cfg, auto, llmPayload{}),
		"不是 chunk":       p.batchEligible(store.Task{Executor: queue.ExecutorBuiltin, Priority: queue.PriorityBackfill}, run, cfg, auto, llmPayload{}),
		"executor agent": p.batchEligible(task, store.StageRun{Stage: StageCorrect, Executor: ExecutorAgent}, cfg, auto, llmPayload{}),
		"translate":      p.batchEligible(task, store.StageRun{Stage: StageTranslate, Executor: ExecutorAPI}, cfg, auto, llmPayload{}),
		"frames":         p.batchEligible(task, store.StageRun{Stage: StageFrames, Executor: ExecutorAPI}, cfg, auto, llmPayload{}),
		"batch 沒設定":      p.batchEligible(task, run, cfg, Effective{}, llmPayload{}),
		"gemini":         p.batchEligible(task, run, StageConfig{Provider: "gemini"}, auto, llmPayload{}),
		"不存在的 provider":  p.batchEligible(task, run, StageConfig{Provider: "nope"}, auto, llmPayload{}),
	} {
		if ok {
			t.Errorf("%s 不應該用 batch", name)
		}
	}
	custom := New(Deps{Store: b.st, Clock: b.clk, LLM: llm.NewRegistry(map[string]llm.ProviderConfig{"anthropic": {Type: llm.TypeAnthropic, BaseURL: "https://proxy.example"}}, b.clk)})
	if custom.batchEligible(task, run, cfg, auto, llmPayload{}) {
		t.Error("自訂 base_url 的 provider 不應該用 batch")
	}
}

// BATCH-R4、R8：個別請求失敗只有那個 chunk 改走一般 API；驗證失敗的結果改走一般 API 並計入驗證重試；
// 不能解碼的結果視同請求失敗；expired 不計費。
func TestBatchResultKinds(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) {
		f.kinds = map[int]string{1: llm.ResultErrored, 2: "garbage", 3: llm.ResultExpired}
		f.answers.behave = map[int]string{4: "drop"} // chunk 4 少一句：驗證失敗
	}})
	b.api.state = llm.BatchEnded
	b.poll()
	viaBatch, chunks, _ := b.provenance()
	if got := reasons(chunks); got != "api:request_failed,api:request_failed,api:request_failed,api:invalid_output" {
		t.Fatalf("來源紀錄 = %s", got)
	}
	if viaBatch {
		t.Error("沒有 chunk 經 batch 完成時頂層 batch 不是 true")
	}
	// 一般 API：4 個 chunk 各一次。chunk 4 的第一次就附上驗證錯誤的說明，而且不再重試（max_validation_retries 1 已用掉）。
	if b.fake.callCount() != 4 {
		t.Fatalf("一般 API 收到 %d 個請求（%v）", b.fake.callCount(), b.fake.calls)
	}
	outcomes := map[string]int{}
	for _, r := range b.costRows() {
		key := r.Outcome
		if strings.Contains(r.UnitPriceJSON, `"batch":true`) {
			key = "batch/" + key
		}
		outcomes[key]++
	}
	// errored 與不能解碼的記 http_error（$0）、expired 不記；驗證失敗的照樣計費（batch 單價）。
	if outcomes["batch/http_error"] != 2 || outcomes["batch/invalid_output"] != 1 || outcomes["ok"] != 4 || len(outcomes) != 3 || b.reserved() != 0 {
		t.Fatalf("outcome = %v reserved=%v", outcomes, b.reserved())
	}
}

// 驗證重試沒有額度時（max_validation_retries 0），batch 驗證失敗的 chunk 直接照步驟的退回規則處理（LLM-R5）。
func TestBatchInvalidWithoutRetriesFallsBackInPlace(t *testing.T) {
	zero := 0
	b := newBatchRig(t, batchRigConfig{retries: &zero, api: func(f *fakeBatchAPI) {
		f.answers.behave = map[int]string{2: "drop"}
	}})
	b.api.state = llm.BatchEnded
	b.poll()
	viaBatch, chunks, _ := b.provenance()
	if !viaBatch || reasons(chunks) != "batch:,batch:,batch:,batch:" || b.fake.callCount() != 0 {
		t.Fatalf("來源紀錄 = %v %s，一般 API %d", viaBatch, reasons(chunks), b.fake.callCount())
	}
	st := latestStage(t, b.rig, b.it.ID, StageCorrect)
	art, _ := b.st.Read().Artifact(context.Background(), st.ArtifactID)
	if !strings.Contains(string(art.Provenance), `"fallbacks":[{"chunk":2`) {
		t.Errorf("chunk 2 要記在 fallbacks：%s", art.Provenance)
	}
}

// BATCH-R5、R7：逾時就取消，還沒有結果的任務改走一般 API（timeout），batch 的預留刪除、以一般單價重新預留。
func TestBatchTimeoutCancelsAndFallsBack(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) {
		f.cancelState = llm.BatchEnded
		f.kinds = map[int]string{1: llm.ResultCanceled, 2: llm.ResultCanceled, 3: llm.ResultCanceled, 4: llm.ResultCanceled}
	}})
	b.clk.Advance(25 * time.Hour)
	if _, done := b.poll(); !done || b.api.cancels != 1 {
		t.Fatalf("逾時：done=%v cancels=%d", done, b.api.cancels)
	}
	_, chunks, _ := b.provenance()
	if got := reasons(chunks); got != "api:timeout,api:timeout,api:timeout,api:timeout" {
		t.Fatalf("來源紀錄 = %s", got)
	}
	for _, r := range b.costRows() {
		if strings.Contains(r.UnitPriceJSON, `"batch":true`) || r.USD != 0.01 {
			t.Errorf("取消的請求不計費，一般 API 以一般單價：%+v", r)
		}
	}
	if bt := b.batch(); bt.State != store.BatchCancelled {
		t.Errorf("batch = %+v", bt)
	}
}

// BATCH-R5：取消之後 provider 仍在處理時繼續輪詢；之後回來的成功結果記為 discarded（BATCH-R9）。
func TestBatchLateResultsAfterTimeoutAreDiscarded(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{budget: map[string]any{"monthly_usd": 0.01}})
	// 預算只夠一般 API 的 1 個 chunk：逾時改走一般 API 時重新檢查預算（BATCH-R7）。
	b.clk.Advance(25 * time.Hour)
	if next, done := b.poll(); done || next.IsZero() {
		t.Fatalf("取消之後還在處理：next=%v done=%v", next, done)
	}
	if bt := b.batch(); bt.State != store.BatchCancelling {
		t.Fatalf("batch = %+v", bt)
	}
	st := latestStage(t, b.rig, b.it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || st.BlockReason != "budget_blocked" {
		t.Fatalf("超過預算時 blocked：%+v", st)
	}
	// 再一次輪詢（cancelling、仍在處理）不重送取消。
	b.clk.Advance(5 * time.Minute)
	if _, done := b.poll(); done || b.api.cancels != 1 {
		t.Fatalf("cancelling：done=%v cancels=%d", done, b.api.cancels)
	}
	b.api.state = llm.BatchEnded
	if _, done := b.poll(); !done {
		t.Fatal("結束後輪詢工作要結束")
	}
	discarded := 0
	for _, r := range b.costRows() {
		if r.Outcome == cost.OutcomeDiscarded {
			discarded++
			if r.State != store.CostActual || r.USD != 0.005 {
				t.Errorf("discarded = %+v", r)
			}
		}
	}
	if discarded != 4 || b.reserved() != 0 {
		t.Fatalf("discarded = %d reserved=%v：%+v", discarded, b.reserved(), b.costRows())
	}
}

// BATCH-R9：重跑時原本的任務離開 batch、輪詢提前；所有任務都被移除時取消 batch，之後的成功結果記為 discarded。
func TestBatchRerunRemovesTasks(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{})
	if _, err := b.p.Rerun(context.Background(), b.it.ID, RerunRequest{From: StageCorrect, Until: StageCorrect}); err != nil {
		t.Fatal(err)
	}
	job, err := b.st.Read().ScheduledJob(context.Background(), store.BatchPollKind, b.batchID())
	if err != nil || !job.NextRunAt.Equal(b.clk.Now()) {
		t.Fatalf("輪詢要提前到現在：%+v %v", job, err)
	}
	// 輪詢在任務之前（ADR-0021 第 1 點）：還在處理、沒有任務了 → 取消。
	if _, done, err := b.p.PollBatch(context.Background(), b.batchID(), b.clk.Now()); err != nil || done || b.api.cancels != 1 {
		t.Fatalf("取消：done=%v err=%v cancels=%d", done, err, b.api.cancels)
	}
	b.run()
	if st := latestStage(t, b.rig, b.it.ID, StageCorrect); st.Status != store.StatusDone || b.fake.callCount() != 4 {
		t.Fatalf("重跑走一般 API：%+v，%d 個請求", st, b.fake.callCount())
	}
	b.api.state = llm.BatchEnded
	b.poll()
	discarded := 0
	for _, r := range b.costRows() {
		if r.Outcome == cost.OutcomeDiscarded {
			discarded++
		}
	}
	if discarded != 4 || b.reserved() != 0 {
		t.Fatalf("discarded = %d reserved=%v", discarded, b.reserved())
	}
}

// BATCH-R9：刪除影音（移到垃圾桶）時任務離開 batch；provider 取消的請求不記帳。
func TestBatchTrashCancelsBatch(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) {
		f.cancelState = llm.BatchEnded
		f.kinds = map[int]string{1: llm.ResultCanceled, 2: llm.ResultCanceled, 3: llm.ResultCanceled, 4: llm.ResultCanceled}
	}})
	if _, err := b.p.Trash(context.Background(), b.it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, done := b.poll(); !done || b.api.cancels != 1 {
		t.Fatalf("done=%v cancels=%d", done, b.api.cancels)
	}
	if rows := b.costRows(); len(rows) != 0 || b.reserved() != 0 {
		t.Fatalf("取消的請求不記帳：%+v reserved=%v", rows, b.reserved())
	}
	if bt := b.batch(); bt.State != store.BatchCancelled {
		t.Errorf("batch = %+v", bt)
	}
}

// BATCH-R4、R11：provider 終止 batch、結果不能取用時所有任務改走一般 API（batch_failed），其他主機的結果網址附上提醒。
func TestBatchFailedAndForeignResults(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) { f.resultsErr = llm.ErrForeignResults }})
	b.api.state = llm.BatchEnded
	b.poll()
	_, chunks, msg := b.provenance()
	if reasons(chunks) != "api:batch_failed,api:batch_failed,api:batch_failed,api:batch_failed" || msg != noticeForeignResults {
		t.Fatalf("來源紀錄 = %s，訊息 %q", reasons(chunks), msg)
	}

	b = newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) { f.kinds = map[int]string{2: "missing"} }})
	b.api.state = llm.BatchTerminated
	b.poll()
	viaBatch, chunks, _ := b.provenance()
	if !viaBatch || reasons(chunks) != "batch:,api:batch_failed,batch:,batch:" {
		t.Fatalf("來源紀錄 = %v %s", viaBatch, reasons(chunks))
	}
	if bt := b.batch(); bt.State != store.BatchFailed {
		t.Errorf("batch = %+v", bt)
	}

	b = newBatchRig(t, batchRigConfig{api: func(f *fakeBatchAPI) { f.resultsErr = llm.ErrBatchResultsTooLarge }})
	b.api.state = llm.BatchEnded
	b.poll()
	if _, chunks, msg := b.provenance(); reasons(chunks) != "api:batch_failed,api:batch_failed,api:batch_failed,api:batch_failed" || msg != "" {
		t.Fatalf("來源紀錄 = %s，訊息 %q", reasons(chunks), msg)
	}
}

// 查詢失敗時回傳錯誤（排程器依退避重試）；結果的其他錯誤同樣。
func TestBatchPollErrors(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{})
	b.api.statusErr = errors.New("boom")
	if _, _, err := b.p.PollBatch(context.Background(), b.batchID(), b.clk.Now()); err == nil {
		t.Error("查詢失敗要回傳錯誤")
	}
	b.api.statusErr, b.api.state, b.api.resultsErr = nil, llm.BatchEnded, errors.New("read failed")
	if _, _, err := b.p.PollBatch(context.Background(), b.batchID(), b.clk.Now()); err == nil {
		t.Error("讀取結果失敗要回傳錯誤")
	}
	if st := latestStage(t, b.rig, b.it.ID, StageCorrect); st.Status != store.StatusWaiting {
		t.Errorf("錯誤時不改變步驟：%+v", st)
	}
	b.p.SetBatchAPI(func(context.Context, string) (BatchAPI, error) { return nil, errors.New("no key") })
	if _, _, err := b.p.PollBatch(context.Background(), b.batchID(), b.clk.Now()); err == nil {
		t.Error("沒有 client 時要回傳錯誤")
	}
}

// BATCH-R7：超過預算的請求不放進 batch，依 COST-R8 blocked。
func TestBatchBudgetBlocksBeforeSubmit(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{opts: cost.Options{Estimates: map[string]float64{StageCorrect: 0.01}}, budget: map[string]any{"monthly_usd": 0.012}})
	// batch 單價 $0.005：前兩個放得進去，第三個超過預算。
	if b.api.requests() != 2 {
		t.Fatalf("batch 的請求數 = %d，預期 2", b.api.requests())
	}
	if got := b.reserved(); got != 0.01 {
		t.Fatalf("預留 = %v", got)
	}
	st := latestStage(t, b.rig, b.it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || st.BlockReason != "budget_blocked" {
		t.Fatalf("超過預算的任務 blocked：%+v", st)
	}
}

// 送出之後程序中斷、沒有記下 provider 的 batch ID 時，第一次輪詢把任務改走一般 API（submit_failed）。
func TestBatchWithoutExternalIDFallsBack(t *testing.T) {
	b := newBatchRig(t, batchRigConfig{})
	db, err := sql.Open("sqlite", "file:"+filepath.Join(b.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE batches SET external_id = ''`); err != nil {
		t.Fatal(err)
	}
	if _, done := b.poll(); !done {
		t.Fatal("沒有 batch ID 時結束輪詢")
	}
	if _, chunks, _ := b.provenance(); reasons(chunks) != "api:submit_failed,api:submit_failed,api:submit_failed,api:submit_failed" {
		t.Fatalf("來源紀錄 = %s", reasons(chunks))
	}
}
