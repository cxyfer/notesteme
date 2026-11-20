package store

import (
	"context"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// TrashItem 把影音移到垃圾桶（設定 deleted_at），並在同一個交易中寫入 item.deleted 事件（DEL-R1）。
// 影音已經在垃圾桶中時什麼都不做，changed 為 false；不存在時回傳 ErrNotFound。
// 回傳的影音是更新後的資料。
func (r *Repo) TrashItem(ctx context.Context, id string) (it Item, changed bool, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		n, err := tx.q.TrashItem(ctx, sqlcdb.TrashItemParams{DeletedAt: ptr(tx.nowMs()), ID: id})
		if err != nil {
			return err
		}
		if it, err = tx.ItemByID(ctx, id); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		changed = true
		return tx.emit(ctx, events.Event{
			Type: events.TypeItemDeleted, Item: source.Ref(it.Platform, it.ExternalID, it.Part), ItemID: it.ID, Title: it.Title,
		})
	})
	if err != nil {
		return Item{}, false, err
	}
	return it, changed, nil
}

// RestoreItem 把影音從垃圾桶還原（deleted_at 改回 NULL），並在同一個交易中寫入 item.restored 事件（DEL-R5）。
// 影音不在垃圾桶中時什麼都不做，changed 為 false；不存在時回傳 ErrNotFound。
func (r *Repo) RestoreItem(ctx context.Context, id string) (it Item, changed bool, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		n, err := tx.q.RestoreItem(ctx, id)
		if err != nil {
			return err
		}
		if it, err = tx.ItemByID(ctx, id); err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		changed = true
		return tx.emit(ctx, events.Event{
			Type: events.TypeItemRestored, Item: source.Ref(it.Platform, it.ExternalID, it.Part), ItemID: it.ID,
		})
	})
	if err != nil {
		return Item{}, false, err
	}
	return it, changed, nil
}

// ListTrash 列出垃圾桶中的影音，刪除時間由新到舊（DEL-R4）。
func (r *Repo) ListTrash(ctx context.Context) ([]Item, error) {
	rows, err := r.q.ListTrash(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		out = append(out, itemFromRow(row))
	}
	return out, nil
}

// DeleteTombstones 移除 (platform, external_id, part) 的 tombstone（不論 sha256），回傳移除的筆數（DEL-R11）。
// 手動新增影音時，在新增影音的同一個交易中呼叫。
func (r *Repo) DeleteTombstones(ctx context.Context, platform, externalID string, part int) (int64, error) {
	return r.q.DeleteTombstones(ctx, sqlcdb.DeleteTombstonesParams{Platform: platform, ExternalID: externalID, Part: int64(part)})
}

func ptr[T any](v T) *T { return &v }
