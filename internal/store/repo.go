package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Repo 是影音、產出、步驟與任務的資料存取。它綁定在一個連線來源上：
//   - Store.Read：唯讀連線池，只能查詢；
//   - Store.Write：唯一的寫入連線，每個陳述句各自提交；
//   - Store.Tx 傳入的 Repo：同一個交易。
//
// 在 Store.Tx 的 callback 裡只能使用傳進來的 Repo；那時呼叫 Write 會等待同一個寫入連線而卡死。
type Repo struct {
	q     *sqlcdb.Queries
	clock clock.Clock
	s     *Store
	// queued 只在交易中非 nil：步驟狀態的事件先記在這裡，commit 之後才發布；rollback 就丟掉。
	queued *[]events.Event
}

// Read 回傳唯讀的 Repo。
func (s *Store) Read() *Repo { return &Repo{q: s.rq, clock: s.clock, s: s} }

// Write 回傳寫入用的 Repo（單一陳述句，自動提交；成功後立即發布事件）。
func (s *Store) Write() *Repo { return &Repo{q: s.wq, clock: s.clock, s: s} }

// SetEventSink 設定接收步驟狀態事件的函式（PIPE-R14）。必須在開始寫入之前呼叫；
// 函式會在寫入端的 goroutine 中同步執行，所以必須不阻塞（events.Bus.Publish 符合）。
func (s *Store) SetEventSink(fn func(events.Event)) { s.sink.Store(&fn) }

// Clock 回傳 store 使用的（注入的）時鐘，讓 SSE 等元件共用同一個時間來源。
func (s *Store) Clock() clock.Clock { return s.clock }

func (s *Store) publish(ev events.Event) {
	if fn := s.sink.Load(); fn != nil {
		(*fn)(ev)
	}
}

// emitStage 記下一次步驟狀態變更：持久化到 events 表（OPS-R10），再依交易狀態發布（見 emit）。
// 事件只是通知，查不到影音（例如已被刪除）時略過，不影響寫入本身。
func (r *Repo) emitStage(ctx context.Context, itemID, stage, status string) error {
	if r.s == nil {
		return nil
	}
	it, err := r.ItemByID(ctx, itemID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.emit(ctx, events.Event{
		Type: events.TypeStageStatus, Item: source.Ref(it.Platform, it.ExternalID, it.Part),
		ItemID: itemID, Stage: stage, Status: status,
	})
}

// emitItemCreated 記下影音新增；持久化與發布的時機同 emitStage，所以 rollback 時兩者都不會留下。
func (r *Repo) emitItemCreated(ctx context.Context, it Item) error {
	if r.s == nil {
		return nil
	}
	return r.emit(ctx, events.Event{
		Type: events.TypeItemCreated, Item: source.Ref(it.Platform, it.ExternalID, it.Part), ItemID: it.ID,
	})
}

// emit 先把事件寫進 events 表（OPS-R10），再發布給 SSE 訂閱者。
//   - 交易中：寫入與狀態變更在同一個交易，一起 commit 或 rollback；發布先排隊，commit 之後才送出。
//   - 單一陳述句（Write）：沒有包住它的交易，事件是緊接在狀態變更之後的另一個陳述句。
//     兩者之間程序若中止，狀態已變更但事件沒有留下；目前只有 Tx 內的路徑保證原子，
//     Write 路徑的原子性是已知的技術債（等 SSE replay 真的依賴 events 表時，再把這些寫入改走 Tx）。
func (r *Repo) emit(ctx context.Context, ev events.Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("store: 序列化事件失敗：%w", err)
	}
	if err := r.InsertEvent(ctx, EventRecord{Type: ev.Type, ItemID: ev.ItemID, PayloadJSON: string(payload)}); err != nil {
		return fmt.Errorf("store: 寫入事件失敗：%w", err)
	}
	if r.queued != nil {
		*r.queued = append(*r.queued, ev)
		return nil
	}
	r.s.publish(ev)
	return nil
}

// Tx 在一個交易中執行 fn；fn 回傳錯誤、panic 或 ctx 取消時回復。
// panic 時也必須回復並釋放唯一的寫入連線，否則之後的寫入會永遠卡住；回復後重新 panic。
func (s *Store) Tx(ctx context.Context, fn func(*Repo) error) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // 失敗與 panic 都走這裡；panic 會在 defer 結束後繼續往上拋
		}
	}()
	var queued []events.Event
	if err := fn(&Repo{q: s.wq.WithTx(tx), clock: s.clock, s: s, queued: &queued}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	// commit 之後才發布，訂閱者讀到事件時資料一定已經落地。
	for _, ev := range queued {
		s.publish(ev)
	}
	return nil
}

// NewID 產生 UUIDv7。
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 只在系統亂數來源失敗時出錯；沒有亂數就無法安全地繼續。
		panic(fmt.Errorf("store: 產生 UUIDv7 失敗：%w", err))
	}
	return id.String()
}

func (r *Repo) nowMs() int64 { return r.clock.Now().UnixMilli() }

// msPtr 把可為空的時間轉成資料庫的值（Unix 毫秒）。
func msPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

// msToTime 是 msPtr 的反向。
func msToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := fromMs(*ms)
	return &t
}

// notFound 把「沒有資料列」轉成 ErrNotFound，其他錯誤原樣回傳。
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// deref 取出可為空欄位的值；NULL 視為空字串。
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
