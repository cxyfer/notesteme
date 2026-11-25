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
	// tx 是交易中的 Repo 所在的交易；其他時候是 nil（測試用來確認交易的連線設定，見 durable_test.go）。
	tx *sql.Tx
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

// inTx 讓 fn 在交易中執行：已經在交易中（Tx 傳入的 Repo）就直接執行；
// 否則（Write 的自動提交 Repo）開一個交易，fn 拿到的是交易內的 Repo。
// 會寫入事件的函式（見 emit）都經過它，狀態變更與事件才會同時成功或同時失敗。
func (r *Repo) inTx(ctx context.Context, fn func(*Repo) error) error {
	if r.queued != nil || r.s == nil {
		return fn(r)
	}
	return r.s.Tx(ctx, fn)
}

// emit 先把事件寫進 events 表（OPS-R10），再發布給 SSE 訂閱者。
// 一定要在交易中呼叫（會寫入事件的函式都經過 inTx）：事件與狀態變更在同一個交易，一起 commit 或 rollback；
// 發布先排隊，commit 之後才送出，rollback 就丟掉。
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
	return s.runTx(tx, fn)
}

// TxDurable 同 Tx，但這個交易以 synchronous=FULL commit：commit 回來時 WAL 已經 fsync，之後斷電也不會遺失它，
// 連同 WAL 中在它之前的所有交易（NFR-REL-3）。
//
// 平常的交易用 synchronous=NORMAL（sqlitedb.Open）：kill -9 不會遺失已 commit 的交易，斷電則可能遺失最後一次
// WAL fsync 之後的交易（DB 不會損毀，重新啟動後依 PIPE-R11 重跑，結果相同）。付費呼叫的結果不能這樣遺失，
// 否則重跑會再付一次錢，所以記錄付費結果的交易改用這裡。每次多一次 fsync（ref-wsl 約 7ms），只用在付費結果上。
//
// 取得唯一的寫入連線並獨占到結束：PRAGMA 是連線層級的設定，獨占才不會讓別的交易在中間把它改回 NORMAL。
func (s *Store) TxDurable(ctx context.Context, fn func(*Repo) error) (err error) {
	conn, err := s.w.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return err
	}
	// 還原失敗時連線維持 FULL：只會比較慢，不影響正確性。
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA synchronous=NORMAL") }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	return s.runTx(tx, fn)
}

// runTx 在已經開始的交易中執行 fn 並 commit；失敗、panic 時回復。
func (s *Store) runTx(tx *sql.Tx, fn func(*Repo) error) (err error) {
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // 失敗與 panic 都走這裡；panic 會在 defer 結束後繼續往上拋
		}
	}()
	var queued []events.Event
	if err := fn(&Repo{q: s.wq.WithTx(tx), clock: s.clock, s: s, queued: &queued, tx: tx}); err != nil {
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

// Now 是 store 的時鐘的現在時間（交易中需要時間、手上沒有 clock 的呼叫端用，例如 Agent 任務的領取期限）。
func (r *Repo) Now() time.Time { return r.clock.Now() }

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
