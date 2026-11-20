package store

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// EventRecord 是一筆持久化的事件（OPS-R10）。
type EventRecord struct {
	// ID 空白時自動產生 UUIDv7。
	ID string
	// ItemID 是內部影音 ID；不屬於影音的事件留空。
	ItemID string
	Type   string
	// PayloadJSON 空白時存成 "{}"。
	PayloadJSON string
	// At 是事件時間；零值時取目前（注入的）時間。
	At time.Time
}

// InsertEvent 寫入一筆事件。
func (r *Repo) InsertEvent(ctx context.Context, ev EventRecord) error {
	if ev.ID == "" {
		ev.ID = NewID()
	}
	if ev.PayloadJSON == "" {
		ev.PayloadJSON = "{}"
	}
	at := r.nowMs()
	if !ev.At.IsZero() {
		at = ev.At.UnixMilli()
	}
	return r.q.InsertEvent(ctx, sqlcdb.InsertEventParams{
		ID: ev.ID, ItemID: ev.ItemID, Type: ev.Type, PayloadJson: ev.PayloadJSON, At: at,
	})
}

// CountEvents 回傳持久化的事件筆數。
func (r *Repo) CountEvents(ctx context.Context) (int64, error) {
	return r.q.CountEvents(ctx)
}

// DeleteEventsBefore 刪除 at 早於 cutoff 的事件（等於 cutoff 的保留），回傳刪除的筆數。
func (r *Repo) DeleteEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return r.q.DeleteEventsBefore(ctx, cutoff.UnixMilli())
}
