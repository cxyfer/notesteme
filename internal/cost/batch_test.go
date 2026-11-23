package cost

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// BATCH-R6、R7：batch 的預留與結算都是一般金額的一半；provider 回報的金額不再打折。
func TestBatchDiscount(t *testing.T) {
	r := newRig(t, Options{Estimates: map[string]float64{"correct": 0.04}})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	c := call("", "t1")
	c.Batch = true
	res, err := r.reserve(c)
	if err != nil {
		t.Fatal(err)
	}
	if v := r.view(); v.ReservedUSD != 0.02 {
		t.Fatalf("batch 預留 = %v，預期 0.02", v.ReservedUSD)
	}
	r.settle(res, c, Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000})
	row := r.row(res.ID)
	if row.USD != 0.02 || row.State != store.CostActual || !strings.Contains(row.UnitPriceJSON, `"batch":true`) || !strings.Contains(row.UnitPriceJSON, `"batch_discount":0.5`) {
		t.Fatalf("batch 結算 = %+v", row.CostRow)
	}
	// 一般請求不打折、不記 batch。
	plain := call("", "t2")
	res2, _ := r.reserve(plain)
	r.settle(res2, plain, Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000})
	if row := r.row(res2.ID); row.USD != 0.04 || !strings.Contains(row.UnitPriceJSON, `"batch":false`) || strings.Contains(row.UnitPriceJSON, "batch_discount") {
		t.Fatalf("一般結算 = %+v", row.CostRow)
	}
	// provider 回報的金額就是實際收費（已經是 batch 價）。
	c3 := call("", "t3")
	c3.Batch = true
	res3, _ := r.reserve(c3)
	usd := 0.013
	r.settle(res3, c3, Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000, ProviderUSD: &usd})
	if row := r.row(res3.ID); row.USD != 0.013 {
		t.Fatalf("provider 回報的金額 = %+v", row.CostRow)
	}
	for m, want := range map[Micro]Micro{0: 0, 1: 1, 3: 2, 40000: 20000} {
		if got := discounted(m, true); got != want {
			t.Errorf("discounted(%d) = %d，預期 %d", m, got, want)
		}
	}
	if discounted(3, false) != 3 {
		t.Error("一般請求不打折")
	}
}

// 預留已經不在（逾時後改走一般 API 時刪除）時，SettleBatch 另外寫一筆實際紀錄，attempt 接在任務既有紀錄之後。
func TestSettleBatchWithoutReservation(t *testing.T) {
	r := newRig(t, Options{})
	r.price("anthropic", "m", UnitInputMTok, 2)
	r.price("anthropic", "m", UnitOutputMTok, 10)
	c := call("", "t1")
	c.Batch = true
	plain := call("", "t1")
	res, _ := r.reserve(plain)
	r.settle(res, plain, Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000})
	for _, rs := range []Reservation{{}, {ID: "gone"}} {
		if err := r.st.Tx(r.ctx, func(tx *store.Repo) error {
			return r.svc.SettleBatch(r.ctx, tx, rs, c, Usage{Outcome: OutcomeDiscarded, InputTokens: 10000, OutputTokens: 2000})
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows := r.rows()
	if len(rows) != 3 {
		t.Fatalf("紀錄 = %+v", rows)
	}
	attempts := map[int]bool{}
	discarded := 0
	for _, row := range rows {
		attempts[row.Attempt] = true
		if row.Outcome == OutcomeDiscarded {
			discarded++
			if row.USD != 0.02 || row.State != store.CostActual {
				t.Errorf("discarded = %+v", row.CostRow)
			}
		}
	}
	if discarded != 2 || !attempts[1] || !attempts[2] || !attempts[3] {
		t.Fatalf("attempt = %v，discarded = %d", attempts, discarded)
	}
	// 有預留時照常結算（不另外新增）。
	res2, _ := r.reserve(c)
	if err := r.st.Tx(r.ctx, func(tx *store.Repo) error {
		return r.svc.SettleBatch(r.ctx, tx, res2, c, Usage{Outcome: OutcomeOK, InputTokens: 10000, OutputTokens: 2000})
	}); err != nil {
		t.Fatal(err)
	}
	if n := len(r.rows()); n != 4 || r.row(res2.ID).State != store.CostActual {
		t.Fatalf("有預留時 = %d 筆", n)
	}
}
