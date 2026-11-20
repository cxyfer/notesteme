package store

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// inbox_entries.state：inbox 監看程式對檔案的判定。
const (
	InboxIngested   = "ingested"
	InboxAlready    = "already"
	InboxDuplicate  = "duplicate"
	InboxNotMedia   = "not_media"
	InboxTombstoned = "tombstoned"
	InboxInTrash    = "in_trash"
	InboxConflict   = "conflict"
)

// InboxEntry 是 inbox 中一個檔案的判定紀錄，以相對 inbox 的路徑（`/` 分隔）為鍵。
// 影音被永久刪除後 ItemID 變成空字串，紀錄與判定留著。
type InboxEntry struct {
	Path      string
	Size      int64
	MtimeNS   int64
	SHA256    string
	State     string
	Reason    string
	ItemID    string
	DecidedAt time.Time
}

// InboxEntries 回傳所有 inbox 紀錄，以 Path 為鍵。
func (r *Repo) InboxEntries(ctx context.Context) (map[string]InboxEntry, error) {
	rows, err := r.q.ListInboxEntries(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]InboxEntry, len(rows))
	for _, row := range rows {
		out[row.Path] = InboxEntry{
			Path: row.Path, Size: row.Size, MtimeNS: row.MtimeNs, SHA256: row.Sha256, State: row.State,
			Reason: row.Reason, ItemID: deref(row.ItemID), DecidedAt: fromMs(row.DecidedAt),
		}
	}
	return out, nil
}

// UpsertInboxEntry 新增或更新一筆 inbox 紀錄。DecidedAt 為零值時取目前的時間；ItemID 空白時存 NULL。
func (r *Repo) UpsertInboxEntry(ctx context.Context, e InboxEntry) error {
	decided := r.nowMs()
	if !e.DecidedAt.IsZero() {
		decided = e.DecidedAt.UnixMilli()
	}
	var itemID *string
	if e.ItemID != "" {
		itemID = &e.ItemID
	}
	return r.q.UpsertInboxEntry(ctx, sqlcdb.UpsertInboxEntryParams{
		Path: e.Path, Size: e.Size, MtimeNs: e.MtimeNS, Sha256: e.SHA256, State: e.State, Reason: e.Reason,
		ItemID: itemID, DecidedAt: decided,
	})
}

// DeleteInboxEntry 刪除一筆 inbox 紀錄；不存在時什麼都不做。
func (r *Repo) DeleteInboxEntry(ctx context.Context, path string) error {
	return r.q.DeleteInboxEntry(ctx, path)
}
