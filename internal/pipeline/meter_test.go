package pipeline

import (
	"context"
	"io"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/worker"
)

// costLLM 在 chunkLLM 的回答加上 usage；fail[n] 依序是 chunk n 前幾次請求的錯誤。
type costLLM struct {
	chunkLLM
	fmu  sync.Mutex
	fail map[int][]error
}

func (f *costLLM) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	m := chunkRE.FindStringSubmatch(req.Data)
	n, _ := strconv.Atoi(m[1])
	f.fmu.Lock()
	if errs := f.fail[n]; len(errs) > 0 {
		f.fail[n] = errs[1:]
		f.fmu.Unlock()
		f.mu.Lock()
		f.calls = append(f.calls, n)
		f.mu.Unlock()
		return llm.Response{}, errs[0]
	}
	f.fmu.Unlock()
	resp, err := f.chunkLLM.Complete(ctx, req)
	resp.Usage = llm.Usage{InputTokens: 10000, OutputTokens: 0}
	return resp, err
}

type costRig struct {
	*rig
	svc *cost.Service
	it  store.Item
}

// newCostRig 是 newLLMRigCap 加上成本服務：anthropic/m 每百萬輸入 token $1、輸出 $0，所以每個成功的 chunk 是 $0.01。
func newCostRig(t *testing.T, fake *costLLM, o cost.Options, budget map[string]any) *costRig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := cost.New(clk, nil, o)
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
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", ChannelID: "UC1", Channel: "ch"}}
	q := queue.New(st, clk, "w1")
	q.Control().SetCap(queue.ResourceLLM, 1)
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: staticPrompts{}, LLM: reg, SponsorBlock: &fakeSponsor{}, Cost: svc})
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	over := map[string]any{
		"stages": map[string]any{"correct": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "correct-default"}},
		"llm":    map[string]any{"chunk": map[string]any{"max_tokens": 1, "overlap_cues": 0, "context_cues": 1}, "max_validation_retries": 1},
	}
	it := r.ingest(watch, over, "normalize")
	r.run()
	seedVariant(t, r, it, subtitle.VariantSegmented, "en", "alpha", "bravo", "charlie", "delta")
	cr := &costRig{rig: r, svc: svc, it: it}
	if budget != nil {
		cr.setBudget(budget)
	}
	return cr
}

func (c *costRig) setBudget(body map[string]any) {
	c.t.Helper()
	b, errs := cost.ParseBudget(body)
	if len(errs) > 0 {
		c.t.Fatal(errs)
	}
	if err := c.st.Tx(context.Background(), func(r *store.Repo) error { return c.svc.SetBudget(context.Background(), r, b) }); err != nil {
		c.t.Fatal(err)
	}
}

func (c *costRig) rows() []store.CostRow {
	c.t.Helper()
	rows, err := c.st.Read().ItemCostRows(context.Background(), c.it.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	return rows
}

func (c *costRig) reserved() float64 {
	c.t.Helper()
	v, err := c.svc.View(context.Background(), c.st.Read())
	if err != nil {
		c.t.Fatal(err)
	}
	return v.ReservedUSD
}

func sumUSD(rows []store.CostRow) float64 {
	var s float64
	for _, r := range rows {
		s += r.USD
	}
	return math.Round(s*1e6) / 1e6
}

// COST-R1、R5：每個請求一筆紀錄，送出前預留、結果出來後改成實際金額；驗證失敗的請求照樣計費。
func TestMeterRecordsEveryRequest(t *testing.T) {
	fake := &costLLM{chunkLLM: chunkLLM{behave: map[int]string{2: "drop"}}}
	c := newCostRig(t, fake, cost.Options{}, nil)
	rerunCorrect(t, c.rig, c.it)
	rows := c.rows()
	// chunk 1、3、4 各一次，chunk 2 是一次加一次驗證重試。
	if len(rows) != 5 || c.reserved() != 0 {
		t.Fatalf("紀錄 = %+v，reserved = %v", rows, c.reserved())
	}
	outcomes := map[string]int{}
	for _, r := range rows {
		if r.State != store.CostActual || r.USD != 0.01 || r.Stage != StageCorrect || r.Provider != "anthropic" || r.Model != "m" || r.StageRunID == "" || r.TaskID == "" {
			t.Errorf("紀錄 = %+v", r)
		}
		outcomes[r.Outcome]++
	}
	if outcomes[cost.OutcomeOK] != 3 || outcomes[cost.OutcomeInvalid] != 2 {
		t.Errorf("outcome = %v", outcomes)
	}
}

// COST-R8：超過預算時不送出請求；調整預算後恢復，已完成的 chunk 不重送（沿用這次的 chunk 紀錄）。
func TestMeterBlocksThenResumesWithoutResending(t *testing.T) {
	fake := &costLLM{}
	c := newCostRig(t, fake, cost.Options{Estimates: map[string]float64{StageCorrect: 0.01}}, map[string]any{"monthly_usd": 0.025})
	rerunCorrect(t, c.rig, c.it)
	st := latestStage(t, c.rig, c.it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || st.ResumeOn != cost.ResumeToken {
		t.Fatalf("超過預算的步驟 = %+v", st)
	}
	if n := fake.callCount(); n != 2 {
		t.Fatalf("擋下之前送出 %d 個請求，預期 2（%v）", n, fake.calls)
	}
	if rows := c.rows(); len(rows) != 2 || sumUSD(rows) != 0.02 || c.reserved() != 0 {
		t.Fatalf("擋下時的紀錄 = %+v reserved=%v", rows, c.reserved())
	}
	c.setBudget(map[string]any{"monthly_usd": 1.0})
	if n, err := c.p.ResumeBlocked(context.Background(), cost.ResumeToken); err != nil || n != 1 {
		t.Fatalf("ResumeBlocked = %d, %v", n, err)
	}
	c.run()
	if st := latestStage(t, c.rig, c.it.ID, StageCorrect); st.Status != store.StatusDone {
		t.Fatalf("恢復後 = %+v", st)
	}
	fake.mu.Lock()
	calls := append([]int(nil), fake.calls...)
	fake.mu.Unlock()
	if len(calls) != 4 || calls[2] != 3 || calls[3] != 4 {
		t.Errorf("恢復後只送出還沒完成的 chunk：%v", calls)
	}
	if rows := c.rows(); len(rows) != 4 || sumUSD(rows) != 0.04 {
		t.Errorf("恢復後的紀錄 = %+v", rows)
	}
}

// 有預算而找不到單價時擋下，什麼都不送出（COST-R7）。
func TestMeterBlocksUnpricedModel(t *testing.T) {
	fake := &costLLM{}
	c := newCostRig(t, fake, cost.Options{}, map[string]any{"per_item_usd": 10.0})
	if err := c.st.Tx(context.Background(), func(r *store.Repo) error {
		return c.svc.DeletePrice(context.Background(), r, map[string]any{"provider": "anthropic", "model": "m", "unit_type": cost.UnitOutputMTok})
	}); err != nil {
		t.Fatal(err)
	}
	rerunCorrect(t, c.rig, c.it)
	st := latestStage(t, c.rig, c.it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || fake.callCount() != 0 || len(c.rows()) != 0 {
		t.Fatalf("步驟 = %+v，請求 %d，紀錄 %d", st, fake.callCount(), len(c.rows()))
	}
}

// 錯誤的請求記為 http_error 或 network_error、金額 0；退避之後重試的請求是下一個 attempt（COST-R1）。
func TestMeterRecordsFailedRequestsAtNoCost(t *testing.T) {
	fake := &costLLM{fail: map[int][]error{
		1: {&errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "HTTP 503"}},
		2: {&errclass.Error{Class: errclass.ClassRetryable, Source: "llm", Detail: "dial tcp: connection refused"}},
	}}
	c := newCostRig(t, fake, cost.Options{}, nil)
	rerunCorrect(t, c.rig, c.it)
	c.clk.Advance(time.Minute)
	c.run()
	if st := latestStage(t, c.rig, c.it.ID, StageCorrect); st.Status != store.StatusDone {
		t.Fatalf("重試之後 = %+v", st)
	}
	byTask := map[string][]store.CostRow{}
	for _, r := range c.rows() {
		byTask[r.TaskID] = append(byTask[r.TaskID], r)
	}
	var failed []store.CostRow
	for _, rows := range byTask {
		if len(rows) == 2 {
			failed = append(failed, rows...)
		}
	}
	if len(failed) != 4 || len(c.rows()) != 6 || c.reserved() != 0 {
		t.Fatalf("紀錄 = %+v", c.rows())
	}
	seen := map[string]bool{}
	for i := 0; i < len(failed); i += 2 {
		first, second := failed[i], failed[i+1]
		if first.Attempt != 1 || first.USD != 0 || second.Attempt != 2 || second.Outcome != cost.OutcomeOK || second.USD != 0.01 {
			t.Errorf("重試的紀錄 = %+v / %+v", first, second)
		}
		seen[first.Outcome] = true
	}
	if !seen[cost.OutcomeHTTPError] || !seen[cost.OutcomeNetworkError] {
		t.Errorf("outcome = %v", seen)
	}
}

// worker 停止（ctx 取消）而中斷的請求刪除預留，不留下紀錄；nil 的 meter 什麼都不做。
func TestMeterReleasesCancelledRequest(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := cost.New(clk, nil, cost.Options{Estimates: map[string]float64{StageCorrect: 0.5}})
	m := &meter{svc: svc, st: st, call: cost.Call{TaskID: "t1", Stage: StageCorrect, Provider: "anthropic", Model: "m", CustomBase: true}}
	req := llm.Request{Data: llm.DataOpen + `{"chunk":1}` + llm.DataClose, MaxTokens: 50}
	if err := m.before(ctx, 1, req); err != nil {
		t.Fatal(err)
	}
	v, _ := svc.View(ctx, st.Read())
	if v.ReservedUSD != 0.5 {
		t.Fatalf("預留 = %v", v.ReservedUSD)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.after(cctx, 1, req, llm.Response{}, context.Canceled, false); err != nil {
		t.Fatal(err)
	}
	if v, _ := svc.View(ctx, st.Read()); v.ReservedUSD != 0 {
		t.Errorf("中斷後仍有預留 %v", v.ReservedUSD)
	}
	// 沒有進行中的預留時 after 什麼都不做（例如 before 被擋下）。
	if err := m.after(ctx, 1, req, llm.Response{}, nil, true); err != nil {
		t.Error(err)
	}
	var nilMeter *meter
	if err := nilMeter.before(ctx, 1, req); err != nil || nilMeter.afterFunc() != nil || nilMeter.then(nil) != nil {
		t.Error("nil 的 meter 應該什麼都不做")
	}
	// then 先執行限流，失敗時不預留。
	limited := m.then(func(context.Context, int, llm.Request) error { return context.DeadlineExceeded })
	if err := limited(ctx, 1, req); err != context.DeadlineExceeded || m.res != nil {
		t.Errorf("限流失敗時不應預留：%v %+v", err, m.res)
	}
}

func TestRequestTokens(t *testing.T) {
	in, out := requestTokens(llm.Request{MaxTokens: 321})
	if in != llm.RequestTokens(llm.Request{MaxTokens: 321}) || out != 321 {
		t.Errorf("沒有資料區塊：%d %d", in, out)
	}
	data := llm.DataOpen + `{"cues":[{"text":"hello world"}]}` + llm.DataClose
	_, out = requestTokens(llm.Request{Data: data, MaxTokens: 1})
	est := llm.EstimateText(data)
	if want := int(math.Ceil(float64(est) * 1.2)); out != want {
		t.Errorf("輸出估算 = %d，預期 %d（資料區塊 %d 的 1.2 倍無條件進位）", out, want, est)
	}
}
