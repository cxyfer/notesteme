package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案驗證 ASR（Groq，audio_hour）與 Supadata（credit）的記帳與預算（COST-R1、R5～R8，COST 待決問題 C17）、
// 試跑的記帳，以及估價使用覆寫的單價（ING-R8、C23）。

// sourceCostRig 是有成本服務、Groq 與 Supadata 的 rig；影片 600 秒、沒有平台字幕。
type sourceCostRig struct {
	*rig
	svc *cost.Service
	g   *fakeGroq
	sd  *fakeSupadata
}

func newSourceCostRig(t *testing.T, o cost.Options) *sourceCostRig {
	t.Helper()
	g := &fakeGroq{lang: "en", baseURL: groq.DefaultBaseURL, model: groq.DefaultModel}
	sd := &fakeSupadata{lang: "en", cues: 3}
	fm, tool := newFakeMedia(t)
	var svc *cost.Service
	r := newRig(t, func(d *Deps) {
		svc = cost.New(d.Clock.(*clock.Fake), nil, o)
		d.Cost, d.Groq, d.Supadata, d.Media = svc, g, sd, tool
	})
	r.fm = fm
	d := 600
	r.yt.info.DurationS = &d
	return &sourceCostRig{rig: r, svc: svc, g: g, sd: sd}
}

func (c *sourceCostRig) price(provider, model, unit string, usd float64) {
	c.t.Helper()
	err := c.st.Tx(context.Background(), func(r *store.Repo) error {
		_, err := c.svc.PutPrice(context.Background(), r, map[string]any{"provider": provider, "model": model, "unit_type": unit, "usd_per_unit": usd})
		return err
	})
	if err != nil {
		c.t.Fatal(err)
	}
}

func (c *sourceCostRig) budget(body map[string]any) {
	c.t.Helper()
	b, errs := cost.ParseBudget(body)
	if len(errs) > 0 {
		c.t.Fatal(errs)
	}
	if err := c.st.Tx(context.Background(), func(r *store.Repo) error { return c.svc.SetBudget(context.Background(), r, b) }); err != nil {
		c.t.Fatal(err)
	}
}

func (c *sourceCostRig) costRows(it store.Item) []store.CostRow {
	c.t.Helper()
	rows, err := c.st.Read().ItemCostRows(context.Background(), it.ID)
	if err != nil {
		c.t.Fatal(err)
	}
	return rows
}

func priceSource(t *testing.T, row store.CostRow) (string, *float64) {
	t.Helper()
	var up struct {
		Source     string   `json:"source"`
		USDPerUnit *float64 `json:"usd_per_unit"`
	}
	if err := json.Unmarshal([]byte(row.UnitPriceJSON), &up); err != nil {
		t.Fatal(err)
	}
	return up.Source, up.USDPerUnit
}

// Groq 的轉錄記一筆 audio_hour：用量是 Groq 回報的長度，單價依 COST-R2（沒有覆寫時是內建計價表）。
func TestGroqTranscriptionIsRecordedByTheAudioHour(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.g.billedS = 1800
	it := c.ingest(watch, groqOnly(), StageTranscript)
	c.run()
	if st := c.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	rows := c.costRows(it)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.Stage != StageTranscript || row.Provider != "groq" || row.Model != groq.DefaultModel || row.UnitType != cost.UnitAudioHour ||
		row.State != store.CostActual || row.Outcome != cost.OutcomeOK || row.Attempt != 1 || math.Abs(row.Units-0.5) > 1e-9 ||
		math.Abs(row.USD-0.02) > 1e-9 || row.TaskID == "" || row.StageRunID == "" {
		t.Errorf("row = %+v", row)
	}
	if src, p := priceSource(t, row); src != cost.SourceBuiltin || p == nil || *p != 0.04 {
		t.Errorf("unit_price = %s", row.UnitPriceJSON)
	}
}

// 覆寫的單價優先；Groq 沒有回報長度時以預留的估算（片長）為準。
func TestGroqUsesTheOverrideAndTheEstimateWhenNothingIsReported(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.price("groq", groq.DefaultModel, cost.UnitAudioHour, 1.2)
	it := c.ingest(watch, groqOnly(), StageTranscript)
	c.run()
	rows := c.costRows(it)
	if len(rows) != 1 || math.Abs(rows[0].Units-600.0/3600) > 1e-9 || math.Abs(rows[0].USD-0.2) > 1e-9 {
		t.Fatalf("rows = %+v", rows)
	}
	if src, _ := priceSource(t, rows[0]); src != cost.SourceOverride {
		t.Errorf("unit_price = %s", rows[0].UnitPriceJSON)
	}
}

// 預估超過月預算時不送出轉錄請求，步驟 budget_blocked（COST-R8），沒有留下紀錄。
func TestGroqOverTheBudgetIsBlockedBeforeTheRequest(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.price("groq", groq.DefaultModel, cost.UnitAudioHour, 1.2)
	c.budget(map[string]any{"monthly_usd": 0.1})
	it := c.ingest(watch, groqOnly(), StageTranscript)
	c.run()
	st := c.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "budget_blocked" || st.ResumeOn != cost.ResumeToken ||
		st.Message != "本月預算 $0.10 已用 $0.00（含預留），這個步驟預估 $0.20，超過預算。" {
		t.Fatalf("transcript = %+v", st)
	}
	if c.g.calls() != 0 {
		t.Errorf("超過預算不該送出請求：%d", c.g.calls())
	}
	if rows := c.costRows(it); len(rows) != 0 {
		t.Errorf("rows = %+v", rows)
	}
}

// 切塊時每一塊各自記帳，預留依這一塊的長度。
func TestGroqChunksAreRecordedOneByOne(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.g.maxBytes = 1 << 20
	c.fm.prepSize = 3 << 20 // 前處理後 3 MiB：切成多塊
	c.fm.duration = 600
	it := c.ingest(watch, groqOnly(), StageTranscript)
	c.run()
	if st := c.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	rows := c.costRows(it)
	if len(rows) < 2 || len(rows) != c.g.calls() {
		t.Fatalf("rows = %d，轉錄請求 = %d", len(rows), c.g.calls())
	}
	var hours float64
	for i, row := range rows {
		if row.Attempt != i+1 || row.State != store.CostActual || row.UnitType != cost.UnitAudioHour {
			t.Errorf("row %d = %+v", i, row)
		}
		hours += row.Units
	}
	// 每塊前後多取的重疊也計入，所以總長至少是整段音訊。
	if hours < 600.0/3600-1e-9 {
		t.Errorf("總時數 = %v", hours)
	}
}

// 轉錄失敗（HTTP 錯誤）記 0 元的 http_error；請求沒有結果（沒有語音）仍計費。
func TestGroqFailuresAndEmptyResults(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.g.err = &errclass.Error{Class: errclass.ClassPermanent, Source: "groq", Detail: "HTTP 400"}
	it := c.ingest(watch, groqOnly(), StageTranscript)
	c.run()
	rows := c.costRows(it)
	if len(rows) != 1 || rows[0].Outcome != cost.OutcomeHTTPError || rows[0].USD != 0 || rows[0].State != store.CostActual {
		t.Fatalf("rows = %+v", rows)
	}

	c2 := newSourceCostRig(t, cost.Options{})
	c2.g.err = groq.ErrNoTranscript
	c2.g.billedS = 3600
	it2 := c2.ingest(watch, groqOnly(), StageTranscript)
	c2.run()
	rows = c2.costRows(it2)
	if len(rows) != 1 || rows[0].Outcome != cost.OutcomeOK || math.Abs(rows[0].USD-0.04) > 1e-9 {
		t.Fatalf("rows = %+v", rows)
	}
}

// Supadata 記一筆 credit：用量是回報的 credit 數；沒有單價、沒有預算時是 unknown（COST-R2 的 (4)、R7）。
func TestSupadataIsRecordedByTheCredit(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.sd.credits = 3
	// 只用 Supadata：替身的字幕太短，沒通過涵蓋率檢查，但已付費的結果在沒有其他來源時仍採用（SRC-R4 的例外）。
	it := c.ingest(watch, fallbackOverride("supadata"), StageTranscript)
	c.run()
	if st := c.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	rows := c.costRows(it)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	row := rows[0]
	if row.Provider != supadata.ID || row.Model != supadata.PriceModel || row.UnitType != cost.UnitCredit || row.Units != 3 ||
		row.State != store.CostUnknown || row.USD != 0 || row.Outcome != cost.OutcomeOK {
		t.Errorf("row = %+v", row)
	}
	if src, p := priceSource(t, row); src != cost.SourceUnknown || p != nil {
		t.Errorf("unit_price = %s", row.UnitPriceJSON)
	}

	// 設定了 credit 的單價：金額是 credit 數乘以單價；沒有回報 credit 時用估算（auto 模式 600 秒 = 20 credit）。
	c2 := newSourceCostRig(t, cost.Options{})
	c2.price(supadata.ID, supadata.PriceModel, cost.UnitCredit, 0.001)
	it2 := c2.ingest(watch, fallbackOverride("supadata"), StageTranscript)
	c2.run()
	rows = c2.costRows(it2)
	if len(rows) != 1 || rows[0].Units != 20 || math.Abs(rows[0].USD-0.02) > 1e-9 || rows[0].State != store.CostActual {
		t.Fatalf("rows = %+v", rows)
	}
}

// 有預算而 Supadata 沒有單價時不送出請求，也不改用下一個來源（COST-R7、R8）。
func TestSupadataWithoutAPriceIsBlockedOnceABudgetIsSet(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.budget(map[string]any{"monthly_usd": 10.0})
	it := c.ingest(watch, fallbackOverride("supadata", "groq"), StageTranscript)
	c.run()
	st := c.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "budget_blocked" || st.Message != "找不到 supadata／transcript 的單價，無法檢查預算。" {
		t.Fatalf("transcript = %+v", st)
	}
	if c.sd.calls() != 0 || c.g.calls() != 0 {
		t.Errorf("supadata = %d，groq = %d，預期都沒有呼叫", c.sd.calls(), c.g.calls())
	}
}

// 沒有 API key 的來源不預留、不記帳，也不會因為預算被擋下：照常略過，改用下一個來源。
func TestUnconfiguredSourceIsNotMetered(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.budget(map[string]any{"monthly_usd": 10.0})
	c.g.noKey = true
	c.price(supadata.ID, supadata.PriceModel, cost.UnitCredit, 0.001)
	it := c.ingest(watch, fallbackOverride("groq", "supadata"), StageTranscript)
	c.run()
	if st := c.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	rows := c.costRows(it)
	if len(rows) != 1 || rows[0].Provider != supadata.ID {
		t.Fatalf("rows = %+v", rows)
	}
}

// 估價（ING-R8）用 COST-R2 的單價順序：覆寫的 Groq 單價與 Supadata 的 credit 單價。
func TestEstimateUsesPriceOverrides(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.price("groq", groq.DefaultModel, cost.UnitAudioHour, 1.2)
	est, err := c.p.Estimate(context.Background(), IngestRequest{URL: watch, Overrides: groqOnly()})
	if err != nil {
		t.Fatal(err)
	}
	if it := est.Items[0]; !near(it.ASRUSD, 0.2) {
		t.Errorf("asr_usd = %+v", it)
	}

	// 自訂的 base_url 不套用內建計價表，但覆寫仍然適用。
	c.g.baseURL = "https://whisper.example.com/v1"
	est, _ = c.p.Estimate(context.Background(), IngestRequest{URL: watch, Overrides: groqOnly()})
	if it := est.Items[0]; !near(it.ASRUSD, 0.2) {
		t.Errorf("自訂 base_url 的 asr_usd = %+v", it)
	}
	c.g.model = "whisper-large-v3"
	est, _ = c.p.Estimate(context.Background(), IngestRequest{URL: watch, Overrides: groqOnly()})
	if it := est.Items[0]; it.ASRUSD != nil || !hasNote(it, "自訂的 base_url") {
		t.Errorf("沒有覆寫的自訂 base_url：%+v", it)
	}

	// Supadata：auto 模式 600 秒估 20 credit。
	c.price(supadata.ID, supadata.PriceModel, cost.UnitCredit, 0.001)
	est, _ = c.p.Estimate(context.Background(), IngestRequest{URL: watch})
	if it := est.Items[0]; srcOf(it) != "supadata" || !near(it.SourceUSD, 0.02) || !near(it.TotalUSD, 0.02) || !hasNote(it, "推估") {
		t.Errorf("supadata = %+v", it)
	}
	if len(est.Notes) != 0 {
		t.Errorf("notes = %v", est.Notes)
	}
	est, _ = c.p.Estimate(context.Background(), IngestRequest{URL: watch, Overrides: map[string]any{"supadata": map[string]any{"mode": "native"}}})
	if it := est.Items[0]; !near(it.SourceUSD, 0.001) || hasNote(it, "推估") {
		t.Errorf("native = %+v", it)
	}
}

// 試跑也記帳（COST-R1）：記在影音名下、沒有任務；超過預算時不送出請求。
func TestPlaygroundIsMeteredAndBudgeted(t *testing.T) {
	fake := &costLLM{chunkLLM: chunkLLM{behave: map[int]string{}}}
	c := newCostRig(t, fake, cost.Options{}, nil)
	before := fake.callCount()
	if _, err := c.p.Playground(context.Background(), PlaygroundRequest{
		Prompt: "correct-default@1", Item: c.it.ID, FromMS: 0, ToMS: 60000, Provider: "anthropic", Model: "m",
	}); err != nil {
		t.Fatal(err)
	}
	sent := fake.callCount() - before
	var play []store.CostRow
	for _, row := range c.rows() {
		if row.TaskID == "" {
			play = append(play, row)
		}
	}
	if sent == 0 || len(play) != sent {
		t.Fatalf("請求 = %d，試跑的紀錄 = %+v", sent, play)
	}
	for _, row := range play {
		if row.Stage != StageCorrect || row.StageRunID != "" || row.State != store.CostActual || row.Attempt != 1 || math.Abs(row.USD-0.01) > 1e-9 {
			t.Errorf("row = %+v", row)
		}
	}

	c.setBudget(map[string]any{"monthly_usd": 0.0})
	before = fake.callCount()
	_, err := c.p.Playground(context.Background(), PlaygroundRequest{
		Prompt: "correct-default@1", Item: c.it.ID, FromMS: 0, ToMS: 60000, Provider: "anthropic", Model: "m",
	})
	if !cost.IsBudgetBlocked(err) || fake.callCount() != before {
		t.Errorf("err = %v，請求 = %d", err, fake.callCount()-before)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// 前一個付費來源的結果被暫留（沒通過涵蓋率），下一個來源超過預算時採用暫留的結果，不讓已付的費用白費。
func TestBudgetBlockedNextSourceKeepsThePaidResult(t *testing.T) {
	c := newSourceCostRig(t, cost.Options{})
	c.price(supadata.ID, supadata.PriceModel, cost.UnitCredit, 0.001)
	c.price("groq", groq.DefaultModel, cost.UnitAudioHour, 100)
	c.budget(map[string]any{"monthly_usd": 1.0})
	it := c.ingest(watch, fallbackOverride("supadata", "groq"), StageTranscript)
	c.run()
	st := c.stage(it, StageTranscript)
	if st.Status != store.StatusDone || c.sd.calls() != 1 || c.g.calls() != 0 {
		t.Fatalf("transcript = %+v，supadata = %d，groq = %d", st, c.sd.calls(), c.g.calls())
	}
	if !strings.Contains(st.Message, "沿用已付費的結果") {
		t.Errorf("message = %q", st.Message)
	}
	probes, err := c.st.Read().LatestProbes(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range probes {
		got = append(got, p.SourceID+":"+p.State+":"+p.Reason)
	}
	if !slices.Contains(got, "groq:unavailable:budget_blocked") || !slices.Contains(got, "supadata:used:low_coverage") {
		t.Errorf("probes = %v", got)
	}
}

// 批次重跑的 dry_run（LIB-R14）從 transcript 起也估算 ASR 與 Supadata。
func TestEstimateRerunIncludesPaidTranscripts(t *testing.T) {
	ctx := context.Background()
	c := newSourceCostRig(t, cost.Options{})
	it := c.ingest(watch, groqOnly(), StageTranscript)
	c.run()
	est, err := c.p.EstimateRerun(ctx, it.ID, StageTranscript, "")
	if err != nil {
		t.Fatal(err)
	}
	// 600 秒 × $0.04／小時 = $0.006667。
	if est.Priced < 1 || est.USD < 6667 || est.Unknown != 0 {
		t.Errorf("groq = %+v", est)
	}
	tr, err := c.p.EstimateRerun(ctx, it.ID, StageTranscript, "")
	if err != nil || tr != est {
		t.Errorf("估算要穩定：%+v %v", tr, err)
	}

	// Supadata 沒有單價：無法估算。
	c2 := newSourceCostRig(t, cost.Options{})
	it2 := c2.ingest(watch, fallbackOverride("supadata"), StageTranscript)
	c2.run()
	if est, err := c2.p.EstimateRerun(ctx, it2.ID, StageTranscript, ""); err != nil || est.Unknown < 1 {
		t.Errorf("supadata = %+v, %v", est, err)
	}

	// 平台字幕：transcript 不用錢。
	c3 := newSourceCostRig(t, cost.Options{})
	c3.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it3 := c3.ingest(watch, nil, StageTranscript)
	c3.run()
	if est, err := c3.p.EstimateRerun(ctx, it3.ID, StageTranscript, ""); err != nil || est != (RerunEstimate{}) {
		t.Errorf("平台字幕 = %+v, %v", est, err)
	}
}
