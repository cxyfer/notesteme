package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// CostEntry 是成本帳本（cost_ledger）的一筆紀錄；由 DEL-B2 擴充。
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
// 由 DEL-B2 實作；骨架回傳 *pipeline.NotImplementedError。
func (s *Server) SeedCost(ctx context.Context, itemID string, usd float64, state string) error {
	return &pipeline.NotImplementedError{What: "寫入成本帳本"}
}

// CostEntries 回傳成本帳本的所有紀錄，依時間由舊到新。只供驗收測試的 harness 檢查永久刪除後帳本仍在（DEL-R7）。
// 由 DEL-B2 實作；骨架回傳 *pipeline.NotImplementedError。
func (s *Server) CostEntries(ctx context.Context) ([]CostEntry, error) {
	return nil, &pipeline.NotImplementedError{What: "讀取成本帳本"}
}
