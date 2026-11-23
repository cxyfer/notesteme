package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 事件名稱（NOTIFY-R3）。
const (
	EventItemDone        = "item.done"
	EventItemFailed      = "item.failed"
	EventItemBlocked     = "item.blocked"
	EventBudgetWarning   = "budget.warning"
	EventDigest          = "digest"
	EventAlert           = "alert"
	EventBetterAvailable = "source.better_available"
)

// Events 是 notifier 可以訂閱的事件，依規格的順序。
var Events = []string{EventItemDone, EventItemFailed, EventItemBlocked, EventBudgetWarning, EventDigest, EventAlert, EventBetterAvailable}

// EventDropped 是 outbox 項目標記 dropped 時寫進 events 表的事件（總覽顯示）；它不在 Events 中，不會再以通知送出。
const EventDropped = "notify.dropped"

// DeliverKind 是送出 outbox 的排程工作（scheduled_jobs.kind），key 固定為 DeliverKey。
const (
	DeliverKind = "notify.deliver"
	DeliverKey  = "outbox"
)

// Item 是通知中的影音（webhook 的 item 欄位，NOTIFY-R4）；url 在送出時依 server.public_url 產生。
type Item struct {
	ID      string `json:"id"`
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	Channel string `json:"channel"`
}

// Alert 是 alert 事件的內容 {kind, key, message, since, resolved}（OPS-R13～R18 決定種類、對象與訊息）。
type Alert struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Message string `json:"message"`
	// Since 是條件開始成立的時間。
	Since    time.Time `json:"since"`
	Resolved bool      `json:"resolved"`
	// Summary 是文字管道標題的摘要（「Notesteme 告警：<摘要>」、「Notesteme 已恢復：<摘要>」）；空白時用 Message。
	Summary string `json:"summary,omitempty"`
}

// Event 是一個要通知的事件。DedupeKey 依 NOTIFY-R10（用 DedupeKey 系列函式產生）。
// 所有文字都必須已經過遮蔽：stage 的 Message 是 UI 的狀態訊息，不放字幕、筆記或外部工具的原始輸出（NOTIFY-R9）。
type Event struct {
	Name      string `json:"event"`
	DedupeKey string `json:"-"`
	Item      *Item  `json:"item,omitempty"`
	// Stage、ErrorClass、Message、Actions 是 item.failed、item.blocked 的步驟狀態。
	Stage      string   `json:"stage,omitempty"`
	ErrorClass string   `json:"error_class,omitempty"`
	Message    string   `json:"message,omitempty"`
	Actions    []string `json:"actions,omitempty"`
	// Summary 是 budget.warning、digest 等不屬於單一影音的事件的一行摘要（文字管道的標題）。
	Summary string `json:"summary,omitempty"`
	// Digest、Items 是 DIG 的彙整內容（形狀由 DIG 決定，原樣放進 webhook 的 body）。
	Digest json.RawMessage `json:"digest,omitempty"`
	Items  json.RawMessage `json:"items,omitempty"`
	Alert  *Alert          `json:"alert,omitempty"`
	// Budget 是 budget.warning 的數字（month、percent、spent_usd、reserved_usd、monthly_usd，COST-R10），原樣放進 webhook 的 body。
	Budget json.RawMessage `json:"budget,omitempty"`
}

// dedupe 鍵（NOTIFY-R10）。
func DedupeItemDone(itemID, runID string) string { return EventItemDone + ":" + itemID + ":" + runID }
func DedupeItemFailed(itemID, stage string) string {
	return EventItemFailed + ":" + itemID + ":" + stage
}
func DedupeItemBlocked(itemID, stage, reason string) string {
	return EventItemBlocked + ":" + itemID + ":" + stage + ":" + reason
}
func DedupeBudgetWarning(month string) string { return EventBudgetWarning + ":" + month }
func DedupeDigest(digestID string, scheduled time.Time) string {
	return EventDigest + ":" + digestID + ":" + scheduled.UTC().Format(time.RFC3339)
}
func DedupeAlert(kind, target string, resolved bool) string {
	k := EventAlert + ":" + kind + ":" + target
	if resolved {
		k += ":resolved"
	}
	return k
}
func DedupeBetterSource(itemID, src string) string {
	return EventBetterAvailable + ":" + itemID + ":" + src
}

// Outbox 把事件寫進 notification_outbox（NOTIFY-R3、R10，ADR-0021 第 5 點）。零值不可用；nil 的 *Outbox 什麼都不做。
type Outbox struct {
	Clock clock.Clock
	// DedupeWindow 是 notify.dedupe_window；0 代表 24 小時。
	DedupeWindow time.Duration
}

// Emit 在呼叫端的交易（r 是 Store.Tx 傳入的 Repo）中，為每個啟用中、訂閱了這個事件的 notifier 寫入一筆 outbox；
// 去重視窗內已有同一個 dedupe_key 的不再寫入。有寫入時把 notify.deliver 提前到現在。回傳寫入的筆數。
// 沒有符合的 notifier 時不寫入任何東西（既有的流程不受影響）。
func (o *Outbox) Emit(ctx context.Context, r *store.Repo, ev Event) (int, error) {
	return o.emit(ctx, r, ev, nil)
}

// EmitTo 同 Emit，但只寫給 only 之中、啟用中而且訂閱了這個事件的 notifier（DIG-R8：彙整只送到它設定的 notifier）。
// only 是空的時候不寫入任何東西。
func (o *Outbox) EmitTo(ctx context.Context, r *store.Repo, ev Event, only []string) (int, error) {
	if len(only) == 0 {
		return 0, nil
	}
	return o.emit(ctx, r, ev, only)
}

func (o *Outbox) emit(ctx context.Context, r *store.Repo, ev Event, only []string) (int, error) {
	if o == nil {
		return 0, nil
	}
	if ev.Name == "" || ev.DedupeKey == "" {
		return 0, errors.New("notify: 事件缺少名稱或 dedupe 鍵")
	}
	ids, err := r.NotifierIDsForEvent(ctx, ev.Name)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if only != nil {
		kept := ids[:0:0]
		for _, id := range ids {
			if contains(only, id) {
				kept = append(kept, id)
			}
		}
		if ids = kept; len(ids) == 0 {
			return 0, nil
		}
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return 0, fmt.Errorf("notify: 序列化事件失敗：%w", err)
	}
	now := o.Clock.Now()
	window := o.DedupeWindow
	if window <= 0 {
		window = 24 * time.Hour
	}
	n := 0
	for _, id := range ids {
		dup, err := r.OutboxDedupeExists(ctx, id, ev.DedupeKey, now.Add(-window))
		if err != nil {
			return n, err
		}
		if dup {
			continue
		}
		if _, err := r.InsertOutbox(ctx, store.OutboxEntry{NotifierID: id, Event: ev.Name, DedupeKey: ev.DedupeKey, PayloadJSON: string(payload)}); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		if err := r.ScheduleJob(ctx, DeliverKind, DeliverKey, now); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Wants 回報是否有啟用中的 notifier 訂閱了這個事件；呼叫端可以先問，再組事件的內容（避免沒有 notifier 時多讀資料）。
func (o *Outbox) Wants(ctx context.Context, r *store.Repo, event string) (bool, error) {
	if o == nil {
		return false, nil
	}
	ids, err := r.NotifierIDsForEvent(ctx, event)
	return len(ids) > 0, err
}

// ItemOf 讀出影音與頻道，組成通知用的 Item（在呼叫端的交易中）。
func ItemOf(ctx context.Context, r *store.Repo, itemID string) (*Item, error) {
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := &Item{ID: it.ID, Ref: source.Ref(it.Platform, it.ExternalID, it.Part), Title: it.Title}
	if it.ChannelID != "" {
		if ch, err := r.ChannelByID(ctx, it.ChannelID); err == nil {
			out.Channel = ch.Title
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return out, nil
}
