package cost

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

func asrCall(item, task string) Call {
	return Call{ItemID: item, TaskID: task, Stage: "transcript", Provider: "groq", Model: "whisper-large-v3-turbo", Unit: UnitAudioHour, Units: 0.5}
}

// ASR 以音訊長度乘以單價預留與結算（COST-R1、R5）：內建計價表、覆寫、自訂 base_url。
func TestUnitPricedReserveAndSettle(t *testing.T) {
	r := newRig(t, Options{})
	c := asrCall("", "task")
	res, err := r.reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Estimate != 20000 || !res.Priced {
		t.Fatalf("預留 = %+v，預期 0.5 小時 × $0.04 = $0.02", res)
	}
	if row := r.row(res.ID); row.UnitType != UnitAudioHour || row.State != store.CostReserved {
		t.Errorf("預留的紀錄 = %+v", row)
	}
	r.settle(res, c, Usage{Outcome: OutcomeOK, Units: 1, UnitsReported: true})
	got := r.row(res.ID)
	var up struct {
		Source     string   `json:"source"`
		CheckedAt  string   `json:"checked_at"`
		USDPerUnit *float64 `json:"usd_per_unit"`
	}
	_ = json.Unmarshal([]byte(got.UnitPriceJSON), &up)
	if got.State != store.CostActual || got.Units != 1 || math.Abs(got.USD-0.04) > 1e-12 || up.Source != SourceBuiltin ||
		up.USDPerUnit == nil || *up.USDPerUnit != 0.04 || up.CheckedAt != PricesAsOf {
		t.Errorf("結算 = %+v，%s", got, got.UnitPriceJSON)
	}

	// 覆寫優先；自訂 base_url 不套用內建計價表，但覆寫仍然適用（COST-R2）。
	r.price("groq", "whisper-large-v3-turbo", UnitAudioHour, 1)
	c.CustomBase = true
	if est, priced, err := r.svc.Estimate(r.ctx, r.st.Read(), c); err != nil || !priced || est != 500000 {
		t.Errorf("覆寫：%d %v %v", est, priced, err)
	}
	c.Model = "whisper-large-v3"
	if est, priced, err := r.svc.Estimate(r.ctx, r.st.Read(), c); err != nil || priced || est != 0 {
		t.Errorf("自訂 base_url 沒有覆寫：%d %v %v", est, priced, err)
	}
}

// 錯誤的回應金額為 0，除非 provider 回報了用量；沒有單價時 unknown；不合理的用量當作 0。
func TestUnitPricedOutcomes(t *testing.T) {
	r := newRig(t, Options{})
	r.price("supadata", "transcript", UnitCredit, 0.01)
	c := Call{TaskID: "task", Stage: "transcript", Provider: "supadata", Model: "transcript", Unit: UnitCredit, Units: 2}
	for i, cs := range []struct {
		u     Usage
		units float64
		usd   float64
	}{
		{Usage{Outcome: OutcomeHTTPError, Units: 2}, 0, 0},
		{Usage{Outcome: OutcomeHTTPError, Units: 1, UnitsReported: true}, 1, 0.01},
		{Usage{Outcome: OutcomeNetworkError}, 0, 0},
		{Usage{Outcome: OutcomeOK, Units: math.NaN()}, 0, 0},
		{Usage{Outcome: OutcomeOK, Units: 3, UnitsReported: true}, 3, 0.03},
	} {
		res, err := r.reserve(c)
		if err != nil {
			t.Fatal(err)
		}
		r.settle(res, c, cs.u)
		got := r.row(res.ID)
		if got.State != store.CostActual || got.Units != cs.units || math.Abs(got.USD-cs.usd) > 1e-12 || got.UnitType != UnitCredit {
			t.Errorf("case %d：%+v", i, got)
		}
	}

	// 沒有單價：沒有預算時照常記錄為 unknown；有預算時擋下（COST-R7）。
	u := Call{TaskID: "t2", Stage: "transcript", Provider: "supadata", Model: "other", Unit: UnitCredit, Units: 1}
	res, err := r.reserve(u)
	if err != nil || res.Priced {
		t.Fatalf("%+v %v", res, err)
	}
	r.settle(res, u, Usage{Outcome: OutcomeOK, Units: 1, UnitsReported: true})
	if got := r.row(res.ID); got.State != store.CostUnknown || got.USD != 0 || got.UnitPriceJSON != `{"source":"unknown"}` {
		t.Errorf("unknown = %+v", got)
	}
	failed := Call{TaskID: "t3", Stage: "transcript", Provider: "supadata", Model: "other", Unit: UnitCredit}
	res, _ = r.reserve(failed)
	r.settle(res, failed, Usage{Outcome: OutcomeHTTPError})
	if got := r.row(res.ID); got.State != store.CostActual || got.USD != 0 {
		t.Errorf("沒有單價的錯誤 = %+v", got)
	}
	r.budget(map[string]any{"monthly_usd": 5.0})
	if _, err := r.reserve(u); blockedMessage(t, err) != "找不到 supadata／other 的單價，無法檢查預算。" {
		t.Errorf("err = %v", err)
	}
}

// 測試控制的估算（Options.Estimates）也適用於以單一單價計價的呼叫。
func TestUnitPricedEstimateControl(t *testing.T) {
	r := newRig(t, Options{Estimates: map[string]float64{"transcript": 0.07}})
	est, priced, err := r.svc.Estimate(r.ctx, r.st.Read(), asrCall("", ""))
	if err != nil || !priced || est != 70000 {
		t.Errorf("est = %d %v %v", est, priced, err)
	}
	if got := unitMicro(math.Inf(1), 1); got != Micro(MaxUSD*1e6) {
		t.Errorf("溢位要封頂：%d", got)
	}
	if got := unitMicro(-1, 1); got != 0 {
		t.Errorf("負數當作 0：%d", got)
	}
}
