package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// outbox 項目的狀態（NOTIFY-R8）。
const (
	OutboxPending = "pending"
	OutboxSent    = "sent"
	OutboxDropped = "dropped"
)

// ErrNotifierExists 是新增已存在的 notifier id（NOTIFY-R1 的 409 notifier_exists）。
var ErrNotifierExists = errors.New("store: notifier 已經存在")

// Notifier 是 notifiers 的一列。密鑰在 Nonce＋Ciphertext（資料金鑰加密，由 internal/notify 加解密）；
// PublicJSON 是非機密欄位與末四碼（NOTIFY-R6），列表不需要解密。
type Notifier struct {
	ID         string
	Kind       string
	Nonce      []byte
	Ciphertext []byte
	PublicJSON string
	Events     []string
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// OutboxEntry 是 notification_outbox 的一列（ADR-0021 第 5 點）。PayloadJSON 不含任何密鑰。
type OutboxEntry struct {
	ID            string
	NotifierID    string
	Event         string
	DedupeKey     string
	PayloadJSON   string
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastAttemptAt *time.Time
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
}

// OutboxAttempt 是一次送出之後要寫回的結果（由 internal/notify 計算）。
type OutboxAttempt struct {
	State         string
	Attempts      int
	NextAttemptAt time.Time
	LastAttemptAt time.Time
	LastError     string
	SentAt        *time.Time
}

func notifierFromRow(row sqlcdb.Notifier) (Notifier, error) {
	n := Notifier{
		ID: row.ID, Kind: row.Kind, Nonce: row.ConfigNonce, Ciphertext: row.ConfigCiphertext,
		PublicJSON: row.ConfigPublicJson, Enabled: row.Enabled != 0,
		CreatedAt: fromMs(row.CreatedAt), UpdatedAt: fromMs(row.UpdatedAt),
	}
	if err := json.Unmarshal([]byte(row.EventsJson), &n.Events); err != nil {
		return Notifier{}, errors.New("store: notifier 的 events_json 格式不正確")
	}
	return n, nil
}

func outboxFromRow(row sqlcdb.NotificationOutbox) OutboxEntry {
	return OutboxEntry{
		ID: row.ID, NotifierID: row.NotifierID, Event: row.Event, DedupeKey: row.DedupeKey, PayloadJSON: row.PayloadJson,
		State: row.State, Attempts: int(row.Attempts), NextAttemptAt: fromMs(row.NextAttemptAt),
		LastAttemptAt: msToTime(row.LastAttemptAt), LastError: deref(row.LastError),
		CreatedAt: fromMs(row.CreatedAt), SentAt: msToTime(row.SentAt),
	}
}

func eventsJSON(events []string) (string, error) {
	if events == nil {
		events = []string{}
	}
	b, err := json.Marshal(events)
	return string(b), err
}

// InsertNotifier 新增一個 notifier；id 已存在時回傳 ErrNotifierExists。
func (r *Repo) InsertNotifier(ctx context.Context, n Notifier) error {
	ev, err := eventsJSON(n.Events)
	if err != nil {
		return err
	}
	rows, err := r.q.InsertNotifier(ctx, sqlcdb.InsertNotifierParams{
		ID: n.ID, Kind: n.Kind, ConfigNonce: n.Nonce, ConfigCiphertext: n.Ciphertext, ConfigPublicJson: n.PublicJSON,
		EventsJson: ev, Enabled: boolInt(n.Enabled), Now: r.nowMs(),
	})
	if err == nil && rows == 0 {
		return ErrNotifierExists
	}
	return err
}

// UpdateNotifier 覆寫 notifier 的設定、事件與啟用狀態（kind 不變）；不存在時回傳 ErrNotFound。
func (r *Repo) UpdateNotifier(ctx context.Context, n Notifier) error {
	ev, err := eventsJSON(n.Events)
	if err != nil {
		return err
	}
	rows, err := r.q.UpdateNotifier(ctx, sqlcdb.UpdateNotifierParams{
		ConfigNonce: n.Nonce, ConfigCiphertext: n.Ciphertext, ConfigPublicJson: n.PublicJSON,
		EventsJson: ev, Enabled: boolInt(n.Enabled), Now: r.nowMs(), ID: n.ID,
	})
	if err == nil && rows == 0 {
		return ErrNotFound
	}
	return err
}

// Notifier 取出一個 notifier；不存在時回傳 ErrNotFound。
func (r *Repo) Notifier(ctx context.Context, id string) (Notifier, error) {
	row, err := r.q.GetNotifier(ctx, id)
	if err != nil {
		return Notifier{}, notFound(err)
	}
	return notifierFromRow(row)
}

// ListNotifiers 依建立順序列出所有 notifier。
func (r *Repo) ListNotifiers(ctx context.Context) ([]Notifier, error) {
	rows, err := r.q.ListNotifiers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Notifier, 0, len(rows))
	for _, row := range rows {
		n, err := notifierFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// CountNotifiers 回傳 notifier 的數量（NOTIFY-R1 的上限）。
func (r *Repo) CountNotifiers(ctx context.Context) (int, error) {
	n, err := r.q.CountNotifiers(ctx)
	return int(n), err
}

// DeleteNotifier 刪除 notifier 與它還沒送出的 outbox 項目（NOTIFY-R1）；回傳是否真的刪除了 notifier。
// 請在交易中呼叫，兩者才會一起成功。
func (r *Repo) DeleteNotifier(ctx context.Context, id string) (bool, error) {
	if _, err := r.q.DeletePendingOutbox(ctx, id); err != nil {
		return false, err
	}
	n, err := r.q.DeleteNotifier(ctx, id)
	return n > 0, err
}

// NotifierIDsForEvent 回傳啟用中、而且訂閱了 event 的 notifier（依建立順序，NOTIFY-R3）。
func (r *Repo) NotifierIDsForEvent(ctx context.Context, event string) ([]string, error) {
	return r.q.NotifierIDsForEvent(ctx, event)
}

// OutboxDedupeExists 回報 notifier 在 since 之後是否已有同一個 dedupe_key 的項目（任何狀態，NOTIFY-R10）。
func (r *Repo) OutboxDedupeExists(ctx context.Context, notifierID, key string, since time.Time) (bool, error) {
	n, err := r.q.OutboxDedupeExists(ctx, sqlcdb.OutboxDedupeExistsParams{NotifierID: notifierID, DedupeKey: key, Since: since.UnixMilli()})
	return n != 0, err
}

// InsertOutbox 寫入一筆 pending 的 outbox 項目，下一次嘗試是現在；ID 空白時自動產生。回傳 ID。
func (r *Repo) InsertOutbox(ctx context.Context, e OutboxEntry) (string, error) {
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.PayloadJSON == "" {
		e.PayloadJSON = "{}"
	}
	err := r.q.InsertOutbox(ctx, sqlcdb.InsertOutboxParams{
		ID: e.ID, NotifierID: e.NotifierID, Event: e.Event, DedupeKey: e.DedupeKey, PayloadJson: e.PayloadJSON, Now: r.nowMs(),
	})
	return e.ID, err
}

// DueOutbox 回傳 next_attempt_at 不晚於 now 的 pending 項目（最早的在前），最多 max 筆。
func (r *Repo) DueOutbox(ctx context.Context, now time.Time, max int) ([]OutboxEntry, error) {
	rows, err := r.q.DueOutbox(ctx, sqlcdb.DueOutboxParams{Now: now.UnixMilli(), Max: int64(max)})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, outboxFromRow(row))
	}
	return out, nil
}

// NextOutboxAttempt 回傳最早的 pending 項目的下一次嘗試時間；沒有 pending 的項目時 ok 為 false。
func (r *Repo) NextOutboxAttempt(ctx context.Context) (at time.Time, ok bool, err error) {
	row, err := r.q.NextOutboxAttempt(ctx)
	if err != nil || row.Pending == 0 {
		return time.Time{}, false, err
	}
	return fromMs(row.NextAttemptAt), true, nil
}

// OutboxAttemptsSince 回傳 notifier 在 since 之後（不含）的嘗試時間（最早的在前），速率上限用（NOTIFY-R11）。
func (r *Repo) OutboxAttemptsSince(ctx context.Context, notifierID string, since time.Time) ([]time.Time, error) {
	ms, err := r.q.OutboxAttemptsSince(ctx, sqlcdb.OutboxAttemptsSinceParams{NotifierID: notifierID, Since: since.UnixMilli()})
	if err != nil {
		return nil, err
	}
	out := make([]time.Time, 0, len(ms))
	for _, m := range ms {
		out = append(out, fromMs(m))
	}
	return out, nil
}

// RecordOutboxAttempt 寫回一次嘗試的結果；項目已不是 pending（例如 notifier 已刪除）時回傳 ErrNotFound。
func (r *Repo) RecordOutboxAttempt(ctx context.Context, id string, a OutboxAttempt) error {
	var lastErr *string
	if a.LastError != "" {
		lastErr = &a.LastError
	}
	n, err := r.q.RecordOutboxAttempt(ctx, sqlcdb.RecordOutboxAttemptParams{
		State: a.State, Attempts: int64(a.Attempts), NextAttemptAt: a.NextAttemptAt.UnixMilli(),
		LastAttemptAt: msPtr(&a.LastAttemptAt), LastError: lastErr, SentAt: msPtr(a.SentAt), ID: id,
	})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// DeferOutbox 把 pending 項目延到 at，不計入嘗試次數（NOTIFY-R11）。
func (r *Repo) DeferOutbox(ctx context.Context, id string, at time.Time) error {
	_, err := r.q.DeferOutbox(ctx, sqlcdb.DeferOutboxParams{NextAttemptAt: at.UnixMilli(), ID: id})
	return err
}

// ListOutbox 回傳最近的 outbox 項目（最新的在前），state 空白代表全部狀態。
func (r *Repo) ListOutbox(ctx context.Context, state string, max int) ([]OutboxEntry, error) {
	rows, err := r.q.ListOutbox(ctx, sqlcdb.ListOutboxParams{State: state, Max: int64(max)})
	if err != nil {
		return nil, err
	}
	out := make([]OutboxEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, outboxFromRow(row))
	}
	return out, nil
}

// DeleteFinishedOutboxBefore 刪除 cutoff 之前建立、已經 sent 或 dropped 的項目（保留 30 天，NOTIFY-R8）。
func (r *Repo) DeleteFinishedOutboxBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	return r.q.DeleteFinishedOutboxBefore(ctx, cutoff.UnixMilli())
}
