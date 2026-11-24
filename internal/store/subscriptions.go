package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Subscription 是 subscriptions 的一列（SUB）。下一次檢查的時間在 scheduled_jobs（kind subscription.check）。
type Subscription struct {
	ID         string
	Kind       string
	Platform   string
	ExternalID string
	URL        string
	Title      string
	// ProfileID 空字串代表使用預設處理方案。
	ProfileID string
	Overrides json.RawMessage
	Schedule  time.Duration
	// Backfill、Filters 是 API 形狀的 JSON（由 internal/subscription 解讀）。
	Backfill json.RawMessage
	Filters  json.RawMessage
	Enabled  bool
	// CreatedAt 是「新內容」的分界（頻道，SUB-R3）。
	CreatedAt time.Time
	// BaselineAt 是第一次成功檢查的時間；nil 代表還沒有基準清單（SUB-R3）。
	BaselineAt     *time.Time
	LastCheckedAt  *time.Time
	LastError      string
	LastErrorClass string
	// Reevaluate 為 true 時，下一次檢查重新評估因回補範圍略過的條目（SUB-R3 的 PATCH）。
	Reevaluate bool
}

// SubscriptionEntry 是訂閱探索到的一個條目（SUB-R2、R5）。
type SubscriptionEntry struct {
	SubscriptionID string
	Platform       string
	ExternalID     string
	Part           int
	Title          string
	PublishedAt    *time.Time
	DurationS      *int
	LiveStatus     string
	// Position 是條目在探索清單中的位置（沒有發布時間時的排序依據）。
	Position int
	Decision string
	Reason   string
	ItemID   string
	SeenAt   time.Time
}

// 條目的決定（subscription_entries.decision）。
const (
	EntryCreated  = "created"
	EntrySkipped  = "skipped"
	EntryDeferred = "deferred"
)

// SubscriptionCheck 是一次檢查要寫回訂閱的結果。
type SubscriptionCheck struct {
	Title          string
	ExternalID     string
	Enabled        bool
	BaselineAt     *time.Time
	CheckedAt      time.Time
	LastError      string
	LastErrorClass string
	Reevaluate     bool
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func subscriptionFromRow(r sqlcdb.Subscription) Subscription {
	return Subscription{
		ID: r.ID, Kind: r.Kind, Platform: r.Platform, ExternalID: r.ExternalID, URL: r.Url, Title: r.Title,
		ProfileID: deref(r.ProfileID), Overrides: json.RawMessage(r.OverridesJson),
		Schedule: time.Duration(r.ScheduleS) * time.Second,
		Backfill: json.RawMessage(r.BackfillJson), Filters: json.RawMessage(r.FiltersJson),
		Enabled: r.Enabled != 0, CreatedAt: fromMs(r.CreatedAt), BaselineAt: msToTime(r.BaselineAt),
		LastCheckedAt: msToTime(r.LastCheckedAt), LastError: deref(r.LastError), LastErrorClass: deref(r.LastErrorClass),
		Reevaluate: r.Reevaluate != 0,
	}
}

// InsertSubscription 建立訂閱；CreatedAt 為零值時取目前（注入的）時間。回傳寫入的內容。
func (r *Repo) InsertSubscription(ctx context.Context, s Subscription) (Subscription, error) {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = fromMs(r.nowMs())
	}
	err := r.q.InsertSubscription(ctx, sqlcdb.InsertSubscriptionParams{
		ID: s.ID, Kind: s.Kind, Platform: s.Platform, ExternalID: s.ExternalID, Url: s.URL, Title: s.Title,
		ProfileID: strPtr(s.ProfileID), OverridesJson: rawOr(s.Overrides, "{}"), ScheduleS: int64(s.Schedule / time.Second),
		BackfillJson: rawOr(s.Backfill, "{}"), FiltersJson: rawOr(s.Filters, "{}"), Enabled: boolInt(s.Enabled),
		CreatedAt: s.CreatedAt.UnixMilli(),
	})
	if err != nil {
		return Subscription{}, err
	}
	return r.Subscription(ctx, s.ID)
}

// Subscription 取出一個訂閱；不存在時回傳 ErrNotFound。
func (r *Repo) Subscription(ctx context.Context, id string) (Subscription, error) {
	row, err := r.q.GetSubscription(ctx, id)
	if err != nil {
		return Subscription{}, notFound(err)
	}
	return subscriptionFromRow(row), nil
}

// Subscriptions 依建立順序列出全部訂閱。
func (r *Repo) Subscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := r.q.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, subscriptionFromRow(row))
	}
	return out, nil
}

// CountSubscriptions 回傳訂閱數。
func (r *Repo) CountSubscriptions(ctx context.Context) (int64, error) {
	return r.q.CountSubscriptions(ctx)
}

// SubscriptionsBySource 回傳同一個來源（kind、platform、external_id）的訂閱，依建立順序。
func (r *Repo) SubscriptionsBySource(ctx context.Context, kind, platform, externalID string) ([]Subscription, error) {
	rows, err := r.q.SubscriptionsBySource(ctx, sqlcdb.SubscriptionsBySourceParams{Kind: kind, Platform: platform, ExternalID: externalID})
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(rows))
	for _, row := range rows {
		out = append(out, subscriptionFromRow(row))
	}
	return out, nil
}

// UpdateSubscriptionSettings 寫回可修改的設定（PATCH）；不存在時回傳 ErrNotFound。
func (r *Repo) UpdateSubscriptionSettings(ctx context.Context, s Subscription) error {
	n, err := r.q.UpdateSubscriptionSettings(ctx, sqlcdb.UpdateSubscriptionSettingsParams{
		ProfileID: strPtr(s.ProfileID), OverridesJson: rawOr(s.Overrides, "{}"), ScheduleS: int64(s.Schedule / time.Second),
		BackfillJson: rawOr(s.Backfill, "{}"), FiltersJson: rawOr(s.Filters, "{}"), Enabled: boolInt(s.Enabled),
		Reevaluate: boolInt(s.Reevaluate), ID: s.ID,
	})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// RecordSubscriptionCheck 寫回一次檢查的結果；訂閱在檢查期間被刪除時回傳 ErrNotFound。
func (r *Repo) RecordSubscriptionCheck(ctx context.Context, id string, c SubscriptionCheck) error {
	n, err := r.q.RecordSubscriptionCheck(ctx, sqlcdb.RecordSubscriptionCheckParams{
		Title: c.Title, ExternalID: c.ExternalID, Enabled: boolInt(c.Enabled), BaselineAt: msPtr(c.BaselineAt),
		LastCheckedAt: msPtr(&c.CheckedAt), LastError: strPtr(c.LastError), LastErrorClass: strPtr(c.LastErrorClass),
		Reevaluate: boolInt(c.Reevaluate), ID: id,
	})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteSubscription 刪除訂閱與它的條目（ON DELETE CASCADE），影音保留但 subscription_id 改為空（SUB-R13）。
// 排程由呼叫端在同一個交易中刪除。回傳是否真的刪除了一列。
func (r *Repo) DeleteSubscription(ctx context.Context, id string) (bool, error) {
	if err := r.q.ClearItemsSubscription(ctx, &id); err != nil {
		return false, err
	}
	n, err := r.q.DeleteSubscription(ctx, id)
	return n > 0, err
}

// SetItemSubscription 記下建立影音的訂閱。
func (r *Repo) SetItemSubscription(ctx context.Context, itemID, subscriptionID string) error {
	return r.q.SetItemSubscription(ctx, sqlcdb.SetItemSubscriptionParams{SubscriptionID: strPtr(subscriptionID), ID: itemID})
}

// UpsertSubscriptionEntry 寫入或更新一個條目；已有的發布時間、片長與直播狀態在新值為空時保留。
func (r *Repo) UpsertSubscriptionEntry(ctx context.Context, e SubscriptionEntry) error {
	if e.SeenAt.IsZero() {
		e.SeenAt = fromMs(r.nowMs())
	}
	var dur *int64
	if e.DurationS != nil {
		d := int64(*e.DurationS)
		dur = &d
	}
	return r.q.UpsertSubscriptionEntry(ctx, sqlcdb.UpsertSubscriptionEntryParams{
		SubscriptionID: e.SubscriptionID, Platform: e.Platform, ExternalID: e.ExternalID, Part: int64(e.Part),
		Title: e.Title, PublishedAt: msPtr(e.PublishedAt), DurationS: dur, LiveStatus: strPtr(e.LiveStatus),
		Position: int64(e.Position), Decision: e.Decision, Reason: e.Reason, ItemID: strPtr(e.ItemID),
		SeenAt: e.SeenAt.UnixMilli(),
	})
}

// SubscriptionEntries 列出訂閱的條目（新的在前），最多 max 筆。
func (r *Repo) SubscriptionEntries(ctx context.Context, subscriptionID string, max int) ([]SubscriptionEntry, error) {
	rows, err := r.q.ListSubscriptionEntries(ctx, sqlcdb.ListSubscriptionEntriesParams{SubscriptionID: subscriptionID, Max: int64(max)})
	if err != nil {
		return nil, err
	}
	out := make([]SubscriptionEntry, 0, len(rows))
	for _, row := range rows {
		e := SubscriptionEntry{
			SubscriptionID: row.SubscriptionID, Platform: row.Platform, ExternalID: row.ExternalID, Part: int(row.Part),
			Title: row.Title, PublishedAt: msToTime(row.PublishedAt), LiveStatus: deref(row.LiveStatus),
			Position: int(row.Position), Decision: row.Decision, Reason: row.Reason, ItemID: deref(row.ItemID),
			SeenAt: fromMs(row.SeenAt),
		}
		if row.DurationS != nil {
			d := int(*row.DurationS)
			e.DurationS = &d
		}
		out = append(out, e)
	}
	return out, nil
}

// HasTombstone 回報 (platform, external_id, part) 是否有永久刪除留下的 tombstone（DEL-R11）。
func (r *Repo) HasTombstone(ctx context.Context, platform, externalID string, part int) (bool, error) {
	n, err := r.q.TombstoneExists(ctx, sqlcdb.TombstoneExistsParams{Platform: platform, ExternalID: externalID, Part: int64(part)})
	return n > 0, err
}

// EmitSubscriptionEvent 寫入並發布一個訂閱事件（subscription.*）；和狀態變更在同一個交易。
func (r *Repo) EmitSubscriptionEvent(ctx context.Context, ev events.Event) error {
	return r.inTx(ctx, func(tx *Repo) error {
		if tx.s == nil {
			return nil
		}
		return tx.emit(ctx, ev)
	})
}

// SubscriptionNewItems 回傳每個訂閱的「新內容」數：這個訂閱建立的（items.subscription_id）、
// 還沒有標為已讀（LIB-R19）、不在垃圾桶中的影音數（SUB-R14）。沒有新內容的訂閱不在結果中。
// 一次 GROUP BY 算出全部，列表不必每列各查一次。
func (r *Repo) SubscriptionNewItems(ctx context.Context) (map[string]int, error) {
	rows, err := r.s.r.QueryContext(ctx, `SELECT subscription_id, COUNT(*) FROM items
WHERE subscription_id IS NOT NULL AND read_state = 'unread' AND deleted_at IS NULL
GROUP BY subscription_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
