// Package queue 是租約制的任務佇列（ADR-0004）。預設實作建立在 SQLite 上：
// 任務存在 tasks 表，以一條 SQL 原子地領取；事件喚醒與並行計數在記憶體中。
//
// 步驟的狀態與任務的完成是同一個交易，所以完成任務的邏輯在 pipeline；這裡負責領取、
// 放回租約、重試的退避時間，以及喚醒 worker。
package queue

import (
	"context"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// 優先順序（PIPE-R9）：手動新增 > 訂閱的新內容 > 回補。數字越大越優先。
const (
	PriorityBackfill     = 1
	PrioritySubscription = 2
	PriorityManual       = 3
)

// PriorityByName 把優先級的名稱（manual、subscription、backfill）換成數值。
func PriorityByName(name string) (int, bool) {
	switch name {
	case "manual":
		return PriorityManual, true
	case "subscription":
		return PrioritySubscription, true
	case "backfill":
		return PriorityBackfill, true
	}
	return 0, false
}

// 內建 worker 執行的任務，executor 為 builtin。
const ExecutorBuiltin = "builtin"

// Agent 領取的任務（AGT），executor 為 agent。
const ExecutorAgent = "agent"

// 資源類別（ADR-0004）。
const (
	ResourceDownload = "download"
	ResourceLocal    = "local"
)

// 預設值。
const (
	// AgingInterval 是等待多久優先度提高一級（PIPE-R9）。
	AgingInterval = 10 * time.Minute
	// DefaultLease 是租約長度（ADR-0004）。
	DefaultLease = 10 * time.Minute
	// DefaultMaxAttempts 是 retryable 錯誤的最多嘗試次數（PIPE-R3）。
	DefaultMaxAttempts = 3
)

// backoff 是 retryable 錯誤重試前的等待時間（PIPE-R3）。
var backoff = []time.Duration{30 * time.Second, 2 * time.Minute}

// RetryDelay 回傳第 attempts 次嘗試失敗後，到下一次嘗試的等待時間。
// attempts 已達 maxAttempts 時 ok 為 false，代表重試次數用盡。
func RetryDelay(attempts, maxAttempts int) (d time.Duration, ok bool) {
	if attempts >= maxAttempts {
		return 0, false
	}
	return backoff[min(max(attempts, 1), len(backoff))-1], true
}

// Queue 是領取任務的入口。每次啟動使用新的 owner（worker ID）。
type Queue struct {
	st    *store.Store
	clk   clock.Clock
	owner string
	lease time.Duration
	wake  chan struct{}
	ctl   *Control
}

// New 建立 Queue；owner 是這次啟動的 worker ID。
func New(st *store.Store, clk clock.Clock, owner string) *Queue {
	q := &Queue{st: st, clk: clk, owner: owner, lease: DefaultLease, wake: make(chan struct{}, 1)}
	q.ctl = newControl(st, q.Wake)
	return q
}

// Control 回傳資源類別的並行上限與暫停設定。啟動時要先 Load。
func (q *Queue) Control() *Control { return q.ctl }

// Owner 回傳 worker ID。
func (q *Queue) Owner() string { return q.owner }

// Wake 通知 worker 有新任務；不會阻塞，重複通知會合併。
func (q *Queue) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Wakeups 是 Wake 的接收端。
func (q *Queue) Wakeups() <-chan struct{} { return q.wake }

// Claim 領取最多 max 個可以執行的任務，並把它們的步驟標記為 running。
// 不看資源類別的上限與暫停設定；worker 用 ClaimFree。
func (q *Queue) Claim(ctx context.Context, max int) ([]store.Task, error) {
	var out []store.Task
	err := q.st.Tx(ctx, func(r *store.Repo) error {
		var err error
		out, err = r.ClaimTasks(ctx, store.ClaimOptions{
			Owner: q.owner, Max: max, Lease: q.lease, Executor: ExecutorBuiltin, AgingInterval: AgingInterval,
		})
		return err
	})
	return out, err
}

// ClaimFree 依各資源類別的空位領取任務（PIPE-R8、PIPE-R10）。running 是各類別正在執行的任務數：
// 每個類別最多領取「上限減去執行中的數量」個，暫停的類別（或整個佇列暫停時）不領取。
// 各類別在同一個交易中領取，類別內仍依優先度加 aging 排序。
func (q *Queue) ClaimFree(ctx context.Context, running map[string]int) ([]store.Task, error) {
	free := q.ctl.Free(running)
	if len(free) == 0 {
		return nil, nil
	}
	classes := make([]string, 0, len(free))
	for class := range free {
		classes = append(classes, class)
	}
	slices.Sort(classes)
	var out []store.Task
	err := q.st.Tx(ctx, func(r *store.Repo) error {
		out = nil
		for _, class := range classes {
			got, err := r.ClaimTasks(ctx, store.ClaimOptions{
				Owner: q.owner, Max: free[class], Lease: q.lease, Executor: ExecutorBuiltin,
				ResourceClasses: []string{class}, AgingInterval: AgingInterval,
			})
			if err != nil {
				return err
			}
			out = append(out, got...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Entries 列出還沒結束的任務：先是內建 worker 的，再是 Agent 的（AGT）；各自先列已被領取的，
// 再列 pending 的，pending 的排序與各自的領取相同（見 store.Repo.QueueTasks）。
func (q *Queue) Entries(ctx context.Context) ([]store.QueueEntry, error) {
	r := q.st.Read()
	out, err := r.QueueTasks(ctx, ExecutorBuiltin, AgingInterval)
	if err != nil {
		return nil, err
	}
	agent, err := r.QueueTasks(ctx, ExecutorAgent, AgingInterval)
	if err != nil {
		return nil, err
	}
	return append(out, agent...), nil
}

// MaxRetryable 是佇列頁「失敗」清單最多列出的筆數。
const MaxRetryable = 200

// Retryable 列出可以手動重試的步驟與它們最新的任務（failed、blocked、被取消），由新到舊，最多 MaxRetryable 筆。
func (q *Queue) Retryable(ctx context.Context) ([]store.RetryableTask, error) {
	return q.st.Read().RetryableTasks(ctx, MaxRetryable)
}

// RecoverStale 把別的 worker 留下的租約，以及已到期的租約放回 pending（PIPE-R11）。
// 這次中斷不計入重試次數。啟動時呼叫一次。
// 同一個交易刪除沒有對應執行中任務的成本預留（程序中斷留下的，COST-R5）：放回佇列的任務之後會重新預留。
func (q *Queue) RecoverStale(ctx context.Context) (int, error) {
	return q.release(ctx, func(r *store.Repo) (int, error) {
		n, err := r.ReleaseLeases(ctx, q.owner)
		if err != nil {
			return 0, err
		}
		if _, err := r.DeleteOrphanReservations(ctx); err != nil {
			return 0, err
		}
		return n, nil
	})
}

// ReleaseOwned 把自己持有的租約放回 pending（正常關閉，PIPE-R12）。
func (q *Queue) ReleaseOwned(ctx context.Context) (int, error) {
	return q.release(ctx, func(r *store.Repo) (int, error) { return r.ReleaseOwned(ctx, q.owner) })
}

func (q *Queue) release(ctx context.Context, f func(*store.Repo) (int, error)) (int, error) {
	var n int
	err := q.st.Tx(ctx, func(r *store.Repo) error {
		var err error
		n, err = f(r)
		return err
	})
	return n, err
}

// NextDue 回傳 not_before 晚於 after 的 pending 任務中最早的時間；沒有這樣的任務時 ok 為 false。
// 已到期的任務不算（領不到的到期任務不能遮蔽之後才到期的重試），它們靠 Wake 喚醒 worker。
// after 應該是這一輪領取「開始前」的時間，這樣領取期間才到期的任務不會漏掉。
func (q *Queue) NextDue(ctx context.Context, after time.Time) (time.Time, bool, error) {
	return q.st.Read().NextTaskTime(ctx, ExecutorBuiltin, after)
}

// WaitRecheckDefault 是 wait 類的結果沒有指定時間時，下一次檢查的間隔。
const WaitRecheckDefault = 15 * time.Minute
