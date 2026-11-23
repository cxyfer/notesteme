package pipeline

import (
	"context"
	"errors"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/store"
)

// meter 為一個任務的付費 LLM 請求記帳（COST-R1、R5、R6）：每次送出之前估算、檢查預算並預留（同一個寫入交易），
// 有了結果之後把那筆預留改成實際金額；請求因為 worker 停止而中斷時刪除預留。
//
// 預留放在送出請求之前，而不是 COST-R5 寫的「領取任務的交易」：領取時還沒有組出請求（prompt、字幕資料區塊），
// 無法依 COST-R5 的方法估算。檢查與預留仍在同一個交易（ADR-0021 第 4 點），所以平行任務不會一起超支；
// 超過預算的任務會占用一次並行名額，但不送出請求（COST 待決問題 C11）。
//
// RunChunk 的驗證重試是同一個 meter 依序呼叫 before、after，所以一次只會有一筆進行中的預留。nil 的 *meter 什麼都不做。
type meter struct {
	svc  *cost.Service
	st   *store.Store
	call cost.Call
	res  *cost.Reservation
}

// newMeter 為任務 t 的 LLM 請求建立 meter；沒有成本服務（測試的最小組裝）時回傳 nil。
func (p *Pipeline) newMeter(t store.Task, run store.StageRun, it store.Item, providerID, model string) *meter {
	if p.cost == nil {
		return nil
	}
	return &meter{svc: p.cost, st: p.st, call: cost.Call{
		ItemID: it.ID, ItemTitle: it.Title, StageRunID: run.ID, TaskID: t.ID, Stage: run.Stage,
		Provider: providerID, Model: model, CustomBase: p.customBase(providerID),
	}}
}

// requestTokens 依 COST-R5 估算一個請求的 token 數：輸入是整個請求（LLM 規格 L2 的方法）；輸出是字幕資料區塊的
// 1.2 倍（無條件進位），沒有資料區塊時用請求的 max_tokens。
func requestTokens(req llm.Request) (in, out int) {
	in = llm.RequestTokens(req)
	if req.Data == "" {
		return in, max(req.MaxTokens, 0)
	}
	return in, (llm.EstimateText(req.Data)*12 + 9) / 10
}

// then 先執行 first（例如限流），通過之後才預留：限流要求等待時不會留下預留。
func (m *meter) then(first func(ctx context.Context, attempt int, req llm.Request) error) func(ctx context.Context, attempt int, req llm.Request) error {
	if m == nil {
		return first
	}
	return func(ctx context.Context, attempt int, req llm.Request) error {
		if first != nil {
			if err := first(ctx, attempt, req); err != nil {
				return err
			}
		}
		return m.before(ctx, attempt, req)
	}
}

// before 預留這次請求的估算金額；超過預算時回傳 budget_blocked，請求不會送出（COST-R8）。
func (m *meter) before(ctx context.Context, _ int, req llm.Request) error {
	if m == nil {
		return nil
	}
	c := m.call
	c.InputTokens, c.Output = requestTokens(req)
	var res cost.Reservation
	err := m.st.Tx(ctx, func(r *store.Repo) error {
		var err error
		res, err = m.svc.Reserve(ctx, r, c)
		return err
	})
	if err != nil {
		return err
	}
	m.res = &res
	return nil
}

// after 是 llm.AfterRequestFunc：把預留改成這次請求的結果。worker 停止（ctx 取消）而中斷的請求刪除預留。
// 寫入用不會被取消的 context：中斷的請求也要把預留處理掉。
func (m *meter) after(ctx context.Context, _ int, _ llm.Request, resp llm.Response, err error, valid bool) error {
	if m == nil || m.res == nil {
		return nil
	}
	res := *m.res
	m.res = nil
	wctx := context.WithoutCancel(ctx)
	if err != nil && errors.Is(err, context.Canceled) {
		return m.st.Tx(wctx, func(r *store.Repo) error { return m.svc.Release(wctx, r, res) })
	}
	u := cost.Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, ProviderUSD: resp.CostUSD}
	switch {
	case err != nil:
		u.Outcome = llm.ErrorOutcome(err)
	case valid:
		u.Outcome = cost.OutcomeOK
	default:
		u.Outcome = cost.OutcomeInvalid
	}
	return m.st.Tx(wctx, func(r *store.Repo) error { return m.svc.Settle(wctx, r, res, m.call, u) })
}

// afterFunc 回傳給 RunOptions.AfterRequest 的函式；nil 的 meter 回傳 nil。
func (m *meter) afterFunc() llm.AfterRequestFunc {
	if m == nil {
		return nil
	}
	return m.after
}
