package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// CostRow 是成本帳本的一筆完整紀錄（COST-R1）。金額以 REAL 存放（00007），加總時一律換成整數 micro-USD。
type CostRow struct {
	ID         string
	ItemID     string
	ItemTitle  string
	StageRunID string
	TaskID     string
	Stage      string
	Provider   string
	Model      string
	Units      float64
	UnitType   string
	USD        float64
	State      string
	Attempt    int
	Outcome    string
	// UnitPriceJSON 是當時的計價明細（COST-R1 的 unit_price_json）；空字串寫成 {}。
	UnitPriceJSON string
	At            time.Time
}

// CostSettlement 是把預留改成實際金額（或 unknown）時寫入的欄位。
type CostSettlement struct {
	State         string
	USD           float64
	Units         float64
	UnitType      string
	Outcome       string
	UnitPriceJSON string
}

// CostListRow 是查詢範圍內的一筆紀錄，附上影音目前的標題、代號與頻道（COST-R14、R15）。
type CostListRow struct {
	CostRow
	// Ref 是影音的短代號；影音已永久刪除時是空字串。
	Ref     string
	Channel string
}

// PriceOverride 是使用者的單價覆寫（price_overrides，COST-R12）。
type PriceOverride struct {
	Provider   string
	Model      string
	UnitType   string
	USDPerUnit float64
	CheckedAt  time.Time
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// InsertCostRow 寫入一筆成本紀錄並回傳 ID（ID 空白時自動產生，At 為零值時取目前的時間）。
// 狀態必須是 reserved、actual 或 unknown；金額不可以是負數、NaN 或無限大。
func (r *Repo) InsertCostRow(ctx context.Context, e CostRow) (string, error) {
	switch e.State {
	case CostReserved, CostActual, CostUnknown:
	default:
		return "", ErrInvalidCostState
	}
	if e.USD < 0 || math.IsNaN(e.USD) || math.IsInf(e.USD, 0) {
		return "", errors.New("store: 成本金額必須是非負的有限數字")
	}
	if e.ID == "" {
		e.ID = NewID()
	}
	at := r.nowMs()
	if !e.At.IsZero() {
		at = e.At.UnixMilli()
	}
	if e.UnitPriceJSON == "" {
		e.UnitPriceJSON = "{}"
	}
	err := r.q.InsertCostRow(ctx, sqlcdb.InsertCostRowParams{
		ID: e.ID, ItemID: optStr(e.ItemID), ItemTitle: e.ItemTitle, StageRunID: optStr(e.StageRunID), TaskID: optStr(e.TaskID),
		Stage: e.Stage, Provider: e.Provider, Model: e.Model, Units: e.Units, UnitType: e.UnitType, Usd: e.USD,
		State: e.State, Attempt: int64(e.Attempt), Outcome: e.Outcome, UnitPriceJson: e.UnitPriceJSON, At: at,
	})
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// CountTaskCostRows 回傳這個任務已有的成本紀錄數（下一次呼叫的 attempt 是它加一，COST-R1）。
func (r *Repo) CountTaskCostRows(ctx context.Context, taskID string) (int, error) {
	n, err := r.q.CountTaskCostRows(ctx, &taskID)
	return int(n), err
}

// SettleCostRow 把一筆預留改成結算後的狀態與金額；這筆已經不是預留（已結算或已被清掉）時回傳 false，不改任何東西。
func (r *Repo) SettleCostRow(ctx context.Context, id string, s CostSettlement) (bool, error) {
	switch s.State {
	case CostActual, CostUnknown:
	default:
		return false, ErrInvalidCostState
	}
	if s.USD < 0 || math.IsNaN(s.USD) || math.IsInf(s.USD, 0) {
		return false, errors.New("store: 成本金額必須是非負的有限數字")
	}
	if s.UnitPriceJSON == "" {
		s.UnitPriceJSON = "{}"
	}
	n, err := r.q.SettleCostRow(ctx, sqlcdb.SettleCostRowParams{
		State: s.State, Usd: s.USD, Units: s.Units, UnitType: s.UnitType, Outcome: s.Outcome, UnitPriceJson: s.UnitPriceJSON, ID: id,
	})
	return n > 0, err
}

// DeleteCostReservation 刪除一筆預留（任務失敗、取消或放回佇列，COST-R5）；已結算的紀錄不刪。
func (r *Repo) DeleteCostReservation(ctx context.Context, id string) (bool, error) {
	n, err := r.q.DeleteCostReservation(ctx, id)
	return n > 0, err
}

// DeleteOrphanReservations 刪除沒有對應執行中任務的預留（程序中斷留下的，COST-R5、PIPE-R11）。
// 還沒有 cost_ledger 的舊 schema（只有測試以 TestControls.Migrations 模擬）沒有預留可清，回傳 0。
func (r *Repo) DeleteOrphanReservations(ctx context.Context) (int, error) {
	n, err := r.q.DeleteOrphanReservations(ctx)
	if err != nil && strings.Contains(err.Error(), "no such table: cost_ledger") {
		return 0, nil
	}
	return int(n), err
}

// SumCostMicrosBetween 回傳 [from, to) 之間 actual 與 reserved 各自的總和（整數 micro-USD）；unknown 不計入。
func (r *Repo) SumCostMicrosBetween(ctx context.Context, from, to time.Time) (actual, reserved int64, err error) {
	row, err := r.q.SumCostMicrosBetween(ctx, sqlcdb.SumCostMicrosBetweenParams{FromMs: from.UnixMilli(), ToMs: to.UnixMilli()})
	if err != nil {
		return 0, 0, err
	}
	return row.Actual, row.Reserved, nil
}

// SumItemCostMicros 回傳這部影音所有紀錄（不分月份）actual 加 reserved 的總和（整數 micro-USD，COST-R6）。
func (r *Repo) SumItemCostMicros(ctx context.Context, itemID string) (int64, error) {
	return r.q.SumItemCostMicros(ctx, &itemID)
}

func costRowFrom(row sqlcdb.CostLedger) CostRow {
	return CostRow{
		ID: row.ID, ItemID: deref(row.ItemID), ItemTitle: row.ItemTitle, StageRunID: deref(row.StageRunID), TaskID: deref(row.TaskID),
		Stage: row.Stage, Provider: row.Provider, Model: row.Model, Units: row.Units, UnitType: row.UnitType, USD: row.Usd,
		State: row.State, Attempt: int(row.Attempt), Outcome: row.Outcome, UnitPriceJSON: row.UnitPriceJson, At: fromMs(row.At),
	}
}

// ItemCostRows 回傳這部影音的成本紀錄，依時間與寫入順序。
func (r *Repo) ItemCostRows(ctx context.Context, itemID string) ([]CostRow, error) {
	rows, err := r.q.ListItemCostRows(ctx, &itemID)
	if err != nil {
		return nil, err
	}
	out := make([]CostRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, costRowFrom(row))
	}
	return out, nil
}

// CostCursor 是分頁的位置：上一頁最後一筆的時間與 ID。零值代表第一頁。
type CostCursor struct {
	At time.Time
	ID string
}

// CostRowsBetween 回傳 [from, to) 之間的紀錄，由新到舊，最多 max 筆，從 cursor 之後開始。
func (r *Repo) CostRowsBetween(ctx context.Context, from, to time.Time, cursor CostCursor, max int) ([]CostListRow, error) {
	curAt, curID := int64(math.MaxInt64), ""
	if !cursor.At.IsZero() {
		curAt, curID = cursor.At.UnixMilli(), cursor.ID
	}
	rows, err := r.q.ListCostRowsBetween(ctx, sqlcdb.ListCostRowsBetweenParams{
		FromMs: from.UnixMilli(), ToMs: to.UnixMilli(), CursorAt: curAt, CursorID: curID, Max: int64(max),
	})
	if err != nil {
		return nil, err
	}
	out := make([]CostListRow, 0, len(rows))
	for _, row := range rows {
		e := CostListRow{
			CostRow: CostRow{
				ID: row.ID, ItemID: deref(row.ItemID), ItemTitle: row.Title, Stage: row.Stage, Provider: row.Provider, Model: row.Model,
				Units: row.Units, UnitType: row.UnitType, USD: row.Usd, State: row.State, Attempt: int(row.Attempt),
				Outcome: row.Outcome, UnitPriceJSON: row.UnitPriceJson, At: fromMs(row.At),
			},
			Channel: row.Channel,
		}
		if row.Platform != "" {
			e.Ref = source.Ref(row.Platform, row.ExternalID, int(row.Part))
		}
		out = append(out, e)
	}
	return out, nil
}

// CountCostRowsBetween 回傳 [from, to) 之間的紀錄數（CSV 匯出的上限，COST-R15）。
func (r *Repo) CountCostRowsBetween(ctx context.Context, from, to time.Time) (int, error) {
	n, err := r.q.CountCostRowsBetween(ctx, sqlcdb.CountCostRowsBetweenParams{FromMs: from.UnixMilli(), ToMs: to.UnixMilli()})
	return int(n), err
}

// UpsertPriceOverride 新增或取代一筆單價覆寫。
func (r *Repo) UpsertPriceOverride(ctx context.Context, p PriceOverride) error {
	return r.q.UpsertPriceOverride(ctx, sqlcdb.UpsertPriceOverrideParams{
		Provider: p.Provider, Model: p.Model, UnitType: p.UnitType, UsdPerUnit: p.USDPerUnit, CheckedAt: p.CheckedAt.UnixMilli(),
	})
}

// DeletePriceOverride 刪除一筆單價覆寫；不存在時回傳 false。
func (r *Repo) DeletePriceOverride(ctx context.Context, provider, model, unitType string) (bool, error) {
	n, err := r.q.DeletePriceOverride(ctx, sqlcdb.DeletePriceOverrideParams{Provider: provider, Model: model, UnitType: unitType})
	return n > 0, err
}

// PriceOverride 讀取一筆單價覆寫；不存在時 ok 為 false。
func (r *Repo) PriceOverride(ctx context.Context, provider, model, unitType string) (PriceOverride, bool, error) {
	row, err := r.q.GetPriceOverride(ctx, sqlcdb.GetPriceOverrideParams{Provider: provider, Model: model, UnitType: unitType})
	if errors.Is(err, sql.ErrNoRows) {
		return PriceOverride{}, false, nil
	}
	if err != nil {
		return PriceOverride{}, false, err
	}
	return PriceOverride{Provider: row.Provider, Model: row.Model, UnitType: row.UnitType, USDPerUnit: row.UsdPerUnit, CheckedAt: fromMs(row.CheckedAt)}, true, nil
}

// PriceOverrides 列出所有單價覆寫，依 provider、model、unit_type 排序。
func (r *Repo) PriceOverrides(ctx context.Context) ([]PriceOverride, error) {
	rows, err := r.q.ListPriceOverrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PriceOverride, 0, len(rows))
	for _, row := range rows {
		out = append(out, PriceOverride{Provider: row.Provider, Model: row.Model, UnitType: row.UnitType, USDPerUnit: row.UsdPerUnit, CheckedAt: fromMs(row.CheckedAt)})
	}
	return out, nil
}

// Emit 在交易中寫入一個事件（events 表與 SSE，OPS-R10）；不屬於步驟狀態的事件（例如 budget.warning）用它。
// commit 之後才發布，rollback 時不會留下。
func (r *Repo) Emit(ctx context.Context, ev events.Event) error {
	return r.inTx(ctx, func(tx *Repo) error { return tx.emit(ctx, ev) })
}
