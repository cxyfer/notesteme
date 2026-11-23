package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// PurgedItem 是被永久刪除的影音；呼叫端用 ID 刪除它在資料目錄中的檔案。
type PurgedItem struct {
	ID    string
	Ref   string
	Title string
}

// PurgeItem 在一個交易中永久刪除垃圾桶中的影音（DEL-R6、R7、R11）：
//  1. cost_ledger 的 item_id 改為空，標題另存在 item_title（金額照常計入預算）。
//  2. 留下 tombstone，鍵是 (platform, external_id, part, content_sha256)；平台影音的 content_sha256 為空。
//  3. 刪除這部影音的 events，另外寫一筆 item.purged：事件的 item 是代號，帶標題，events 表的 item_id 留空。
//  4. 刪除 items；artifacts、stage_runs、source_probes、tasks 依 ON DELETE CASCADE 一起刪除。
//  5. 刪除沒有任何步驟引用的 config_snapshots。
//
// 影音不存在，或不在垃圾桶中時回傳 ErrNotFound。notAfter 不為 nil 時，影音必須在那個時間或之前刪除，
// 否則同樣回傳 ErrNotFound：到期清理列出到期的影音之後，影音可能先被還原又重新刪除，不能照舊的名單刪掉它。
// 事件在 commit 之後才發布；rollback 時什麼都不會留下。
func (r *Repo) PurgeItem(ctx context.Context, id string, notAfter *time.Time) (PurgedItem, error) {
	var out PurgedItem
	err := r.inTx(ctx, func(tx *Repo) error {
		it, err := tx.ItemByID(ctx, id)
		if err != nil {
			return err
		}
		if it.DeletedAt == nil || (notAfter != nil && it.DeletedAt.After(*notAfter)) {
			return ErrNotFound
		}
		ref := source.Ref(it.Platform, it.ExternalID, it.Part)
		out = PurgedItem{ID: it.ID, Ref: ref, Title: it.Title}

		if _, err := tx.q.PurgeDetachCost(ctx, sqlcdb.PurgeDetachCostParams{ItemTitle: it.Title, ItemID: &it.ID}); err != nil {
			return fmt.Errorf("改寫成本帳本：%w", err)
		}
		if err := tx.q.PurgeUpsertTombstone(ctx, sqlcdb.PurgeUpsertTombstoneParams{
			Platform: it.Platform, ExternalID: it.ExternalID, Part: int64(it.Part), Sha256: it.ContentSHA256, PurgedAt: tx.nowMs(),
		}); err != nil {
			return fmt.Errorf("寫入 tombstone：%w", err)
		}
		if _, err := tx.q.PurgeDeleteEvents(ctx, it.ID); err != nil {
			return fmt.Errorf("刪除事件：%w", err)
		}
		// SRCH-R8：索引列在同一個交易中刪除（外鍵的 cascade 也會刪，這裡明確寫出）。
		if err := tx.q.DeleteSearchCuesForItem(ctx, it.ID); err != nil {
			return fmt.Errorf("刪除全文搜尋索引：%w", err)
		}
		if n, err := tx.q.PurgeDeleteItem(ctx, it.ID); err != nil {
			return fmt.Errorf("刪除影音：%w", err)
		} else if n == 0 {
			return ErrNotFound
		}
		if _, err := tx.q.PurgeDeleteOrphanSnapshots(ctx); err != nil {
			return fmt.Errorf("刪除沒有引用的設定快照：%w", err)
		}
		// item_id 留空：影音已經不存在，事件只記代號與標題。
		return tx.emit(ctx, events.Event{Type: events.TypeItemPurged, Item: ref, Title: it.Title})
	})
	return out, err
}

// TrashIDs 回傳垃圾桶中所有影音的 ID，刪除時間早的在前。
func (r *Repo) TrashIDs(ctx context.Context) ([]string, error) {
	return r.q.PurgeTrashedIDs(ctx)
}

// ExpiredTrashIDs 回傳 deleted_at 不晚於 cutoff 的影音 ID，刪除時間早的在前。
func (r *Repo) ExpiredTrashIDs(ctx context.Context, cutoff time.Time) ([]string, error) {
	ms := cutoff.UnixMilli()
	return r.q.PurgeExpiredIDs(ctx, &ms)
}

// DeletionStats 是刪除預覽用的統計（DEL-R3）。
type DeletionStats struct {
	// Artifacts 是所有版本的產出數，Bytes 是它們的 size 總和。
	Artifacts int
	Bytes     int64
	// SpentUSD 是這部影音狀態為 actual 的花費總和。
	SpentUSD float64
}

// DeletionStats 查詢影音的刪除預覽統計；只讀取。
func (r *Repo) DeletionStats(ctx context.Context, itemID string) (DeletionStats, error) {
	a, err := r.q.PreviewArtifactStats(ctx, itemID)
	if err != nil {
		return DeletionStats{}, err
	}
	spent, err := r.q.PreviewSpentUSD(ctx, &itemID)
	if err != nil {
		return DeletionStats{}, err
	}
	return DeletionStats{Artifacts: int(a.Artifacts), Bytes: a.Bytes, SpentUSD: spent}, nil
}

// 成本帳本的狀態（DEL-R7）。
const (
	CostReserved = "reserved"
	CostActual   = "actual"
	CostUnknown  = "unknown"
)

// CostEntry 是成本帳本的一筆紀錄。
type CostEntry struct {
	ID string
	// ItemID 在影音永久刪除之後為空。
	ItemID    string
	ItemTitle string
	USD       float64
	State     string
	At        time.Time
}

// ErrInvalidCostState 表示成本帳本的狀態不是 reserved、actual 或 unknown。
var ErrInvalidCostState = errors.New("成本帳本的狀態必須是 reserved、actual 或 unknown。")

// InsertCostEntry 寫入一筆成本紀錄；ItemID 空白時 item_id 為 NULL。ID 空白時自動產生，At 為零值時取目前的時間。
// 成本的寫入端屬於 COST 切片；目前只有驗收測試的 harness 用它預先放入花費。
func (r *Repo) InsertCostEntry(ctx context.Context, e CostEntry) error {
	switch e.State {
	case CostReserved, CostActual, CostUnknown:
	default:
		return ErrInvalidCostState
	}
	if e.ID == "" {
		e.ID = NewID()
	}
	at := r.nowMs()
	if !e.At.IsZero() {
		at = e.At.UnixMilli()
	}
	var itemID *string
	if e.ItemID != "" {
		itemID = &e.ItemID
	}
	return r.q.InsertCostEntry(ctx, sqlcdb.InsertCostEntryParams{
		ID: e.ID, ItemID: itemID, ItemTitle: e.ItemTitle, Usd: e.USD, State: e.State, At: at,
	})
}

// CostEntries 回傳成本帳本的所有紀錄，由舊到新。
func (r *Repo) CostEntries(ctx context.Context) ([]CostEntry, error) {
	rows, err := r.q.ListCostEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CostEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, CostEntry{
			ID: row.ID, ItemID: deref(row.ItemID), ItemTitle: row.ItemTitle, USD: row.Usd, State: row.State, At: fromMs(row.At),
		})
	}
	return out, nil
}
