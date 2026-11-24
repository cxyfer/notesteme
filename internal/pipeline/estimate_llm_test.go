package pipeline

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/store"
)

// llmOverrides 讓 correct 走付費的 api（anthropic／m，輸入 $1／百萬 token），每個 chunk 最多 1000 token。
func llmOverrides() map[string]any {
	return map[string]any{
		"stages": map[string]any{"correct": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "correct-default"}},
		"llm":    map[string]any{"chunk": map[string]any{"max_tokens": 1000}},
	}
}

// ING-R8：還沒有字幕時，LLM 費用用片長推估資料區塊（每秒 6 token），依 COST-R5 估算：
// 輸入＝資料區塊＋每個 chunk 的 prompt 與標題；輸出＝資料區塊的 1.2 倍。
func TestEstimateLLMFromDuration(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	d := 600
	c.yt.info.DurationS = &d
	est, err := c.p.Estimate(ctx, IngestRequest{URL: watch, Overrides: llmOverrides()})
	if err != nil || len(est.Items) != 1 {
		t.Fatalf("Estimate = %+v, %v", est, err)
	}
	it := est.Items[0]

	cfgEff := c.effective(t)
	cfg, err := cfgEff.Stage(StageCorrect)
	if err != nil {
		t.Fatal(err)
	}
	pr, err := c.p.stagePrompt(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	data := 600 * estimateDataTokensPerSecond // 3600
	chunks := 4                               // ceil(3600 / 1000)
	in := data + chunks*(llm.EstimateText(it.Title)+llm.EstimateText(pr.Body))
	want, priced, err := c.svc.Estimate(ctx, c.st.Read(), cost.Call{Stage: StageCorrect, Provider: "anthropic", Model: "m", InputTokens: in, Output: (data*12 + 9) / 10, CustomBase: c.p.customBase("anthropic")})
	if err != nil || !priced || want <= 0 {
		t.Fatalf("預期金額 = %v, %v, %v", want, priced, err)
	}
	if !near(it.LLMUSD, want.USD()) {
		t.Errorf("llm_usd = %v，預期 %v", deref(it.LLMUSD), want.USD())
	}
	if !near(it.TotalUSD, deref(it.ASRUSD)+want.USD()) || !hasNote(it, "估算值") {
		t.Errorf("item = %+v", it)
	}

	// 片長加倍，金額跟著增加（輸入隨資料區塊與 chunk 數增加）。
	d2 := 1200
	c.yt.info.DurationS = &d2
	est2, err := c.p.Estimate(ctx, IngestRequest{URL: watch, Overrides: llmOverrides()})
	if err != nil || est2.Items[0].LLMUSD == nil || *est2.Items[0].LLMUSD <= *it.LLMUSD {
		t.Errorf("片長加倍 = %+v, %v", est2, err)
	}
}

func deref(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}

// 找不到單價時 llm_usd 是 null（不知道），不是 0；總額不含它，並註明部分金額未知。
func TestEstimateLLMUnpricedIsNull(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	d := 600
	c.yt.info.DurationS = &d
	over := llmOverrides()
	over["stages"].(map[string]any)["correct"].(map[string]any)["model"] = "no-price"
	est, err := c.p.Estimate(ctx, IngestRequest{URL: watch, Overrides: over})
	if err != nil {
		t.Fatal(err)
	}
	it := est.Items[0]
	if it.LLMUSD != nil || !hasNote(it, "無法估算 LLM 費用") || !hasTopNote(est, "部分金額未知") {
		t.Errorf("item = %+v，notes = %v", it, est.Notes)
	}
}

// 片長不知道而有付費的 LLM 步驟時，llm_usd 是 null。
func TestEstimateLLMUnknownDuration(t *testing.T) {
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	c.yt.info.DurationS = nil
	est, err := c.p.Estimate(context.Background(), IngestRequest{URL: watch, Overrides: llmOverrides()})
	if err != nil {
		t.Fatal(err)
	}
	if it := est.Items[0]; it.LLMUSD != nil || !hasNote(it, "不知道影音的長度，無法估算 LLM 費用") {
		t.Errorf("item = %+v", it)
	}
}

// 測試控制的估算（每個任務的金額）乘上 chunk 數；找不到單價也能估算。
func TestEstimateLLMUsesControlledEstimatePerChunk(t *testing.T) {
	c := newCostRig(t, &costLLM{}, cost.Options{Estimates: map[string]float64{StageCorrect: 0.01}}, nil)
	d := 600
	c.yt.info.DurationS = &d
	over := llmOverrides()
	over["stages"].(map[string]any)["correct"].(map[string]any)["model"] = "no-price"
	est, err := c.p.Estimate(context.Background(), IngestRequest{URL: watch, Overrides: over})
	if err != nil {
		t.Fatal(err)
	}
	if it := est.Items[0]; !near(it.LLMUSD, 0.04) {
		t.Errorf("llm_usd = %v，預期 4 個 chunk × 0.01", deref(it.LLMUSD))
	}
}

// translate 依需要 LLM 的目標數計費：OpenCC 轉換與相同語言不算。
func TestLLMTargets(t *testing.T) {
	eff := Effective{"language": map[string]any{"targets": []any{"en", "ja", "zh-Hant-TW", "fr"}}}
	if n := llmTargets(eff, StageContext{SourceLang: "en"}); n != 3 {
		t.Errorf("en → [en ja zh-Hant-TW fr] = %d，預期 3", n)
	}
	if n := llmTargets(eff, StageContext{SourceLang: "zh-Hans"}); n < 3 {
		t.Errorf("zh-Hans → … = %d，預期至少 3（en、ja、fr）", n)
	}
	if n := llmTargets(Effective{"language": map[string]any{"targets": []any{"en"}}}, StageContext{SourceLang: "en"}); n != 0 {
		t.Errorf("相同語言 = %d，預期 0", n)
	}
}

// SRC-R23：更好的來源的 estimate_usd 與從 transcript 重跑的估算（EstimateRerun）相同；有無法估算的付費步驟時為 null。
func TestBetterEstimate(t *testing.T) {
	ctx := context.Background()
	c := newCostRig(t, &costLLM{}, cost.Options{}, nil)
	it, err := c.st.Read().ItemByID(ctx, c.it.ID)
	if err != nil {
		t.Fatal(err)
	}
	est, err := c.p.EstimateRerun(ctx, it.ID, StageTranscript, "")
	if err != nil || est.Unknown != 0 || est.USD <= 0 {
		t.Fatalf("EstimateRerun = %+v, %v", est, err)
	}
	if got := c.p.betterEstimate(ctx, it); !near(got, est.USD.USD()) {
		t.Errorf("betterEstimate = %v，預期 %v", deref(got), est.USD.USD())
	}
	// 刪掉單價：無法估算，回 nil。
	err = c.st.Tx(ctx, func(r *store.Repo) error {
		for _, unit := range []string{cost.UnitInputMTok, cost.UnitOutputMTok} {
			if err := c.svc.DeletePrice(ctx, r, map[string]any{"provider": "anthropic", "model": "m", "unit_type": unit}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.p.betterEstimate(ctx, it); got != nil {
		t.Errorf("沒有單價時 = %v，預期 nil", *got)
	}
	// 影音不存在：nil。
	if got := c.p.betterEstimate(ctx, store.Item{ID: "nope"}); got != nil {
		t.Errorf("不存在的影音 = %v，預期 nil", *got)
	}
}

// 沒有任何付費的 LLM 任務時，更好的來源的 estimate_usd 是 null（訊息「目前無法估算金額」），不是 $0.00。
func TestBetterEstimateWithoutPaidStagesIsNull(t *testing.T) {
	r, _ := newSupadataRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	est, err := r.p.EstimateRerun(context.Background(), it.ID, StageTranscript, "")
	if err != nil || est.Priced != 0 || est.Unknown != 0 || est.USD != 0 {
		t.Fatalf("EstimateRerun = %+v, %v（這個 rig 應該沒有付費步驟）", est, err)
	}
	if got := r.p.betterEstimate(context.Background(), it); got != nil {
		t.Errorf("betterEstimate = %v，預期 nil", *got)
	}
}
