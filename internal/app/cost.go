package app

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// CostEntry 是成本帳本（cost_ledger）的一筆紀錄。
type CostEntry struct {
	// ItemID 在影音永久刪除之後為空（DEL-R7）。
	ItemID string
	// ItemTitle 是永久刪除時記錄的影音標題。
	ItemTitle string
	USD       float64
	// State 是 reserved、actual 或 unknown。
	State string
}

// SeedCost 在成本帳本寫入一筆這部影音的花費。只供驗收測試的 harness 使用（ADR-0015）：
// 成本還沒有 COST 切片的寫入端，它不對應任何 HTTP 端點或 CLI 指令，production 程式不會呼叫它。
// itemID 是內部的影音 ID；state 是 reserved、actual 或 unknown，其他值回傳 store.ErrInvalidCostState。
func (s *Server) SeedCost(ctx context.Context, itemID string, usd float64, state string) error {
	return s.store.Write().InsertCostEntry(ctx, store.CostEntry{ItemID: itemID, USD: usd, State: state})
}

// CostEntries 回傳成本帳本的所有紀錄，依時間由舊到新。只供驗收測試的 harness 檢查永久刪除後帳本仍在（DEL-R7）。
func (s *Server) CostEntries(ctx context.Context) ([]CostEntry, error) {
	rows, err := s.store.Read().CostEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CostEntry, 0, len(rows))
	for _, e := range rows {
		out = append(out, CostEntry{ItemID: e.ItemID, ItemTitle: e.ItemTitle, USD: e.USD, State: e.State})
	}
	return out, nil
}

// CostRow 是成本帳本的一筆完整紀錄（COST 的驗收句型 `the cost ledger entries of … are:` 用）。
type CostRow = store.CostRow

// CostRowsOf 回傳這部影音的成本紀錄，依時間與寫入順序。只供驗收測試的 harness 使用（steps.md 4.19，不新增 API）。
func (s *Server) CostRowsOf(ctx context.Context, itemID string) ([]CostRow, error) {
	return s.store.Read().ItemCostRows(ctx, itemID)
}

// SeedCostAt 在成本帳本寫入一筆這部影音在指定時間的紀錄（state 同 SeedCost）。只供驗收測試的 harness 使用。
func (s *Server) SeedCostAt(ctx context.Context, itemID string, usd float64, state string, at time.Time) error {
	_, err := s.store.Write().InsertCostRow(ctx, store.CostRow{ItemID: itemID, USD: usd, State: state, At: at})
	return err
}
