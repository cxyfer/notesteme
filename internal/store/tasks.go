package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

func taskFromRow(row sqlcdb.Task) Task {
	t := Task{
		ID:            row.ID,
		StageRunID:    row.StageRunID,
		Kind:          row.Kind,
		Executor:      row.Executor,
		ResourceClass: row.ResourceClass,
		State:         row.State,
		Priority:      int(row.Priority),
		Payload:       json.RawMessage(row.PayloadJson),
		LeaseToken:    deref(row.LeaseToken),
		LeaseOwner:    deref(row.LeaseOwner),
		LeaseUntil:    msToTime(row.LeaseUntil),
		Attempts:      int(row.Attempts),
		MaxAttempts:   int(row.MaxAttempts),
		NotBefore:     msToTime(row.NotBefore),
		DeadlineAt:    msToTime(row.DeadlineAt),
		CreatedAt:     fromMs(row.CreatedAt),
	}
	if row.ChunkIndex != nil {
		c := int(*row.ChunkIndex)
		t.ChunkIndex = &c
	}
	return t
}

// InsertTask 新增任務，狀態為 pending。
func (r *Repo) InsertTask(ctx context.Context, n NewTask) (Task, error) {
	payload := n.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	maxAttempts := n.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	id := NewID()
	err := r.q.InsertTask(ctx, sqlcdb.InsertTaskParams{
		ID:            id,
		StageRunID:    n.StageRunID,
		Kind:          n.Kind,
		Executor:      n.Executor,
		ResourceClass: n.ResourceClass,
		Priority:      int64(n.Priority),
		PayloadJson:   string(payload),
		MaxAttempts:   int64(maxAttempts),
		NotBefore:     msPtr(n.NotBefore),
		DeadlineAt:    msPtr(n.DeadlineAt),
		CreatedAt:     r.nowMs(),
	})
	if err != nil {
		return Task{}, err
	}
	return r.Task(ctx, id)
}

// Task 以 ID 查詢任務。
func (r *Repo) Task(ctx context.Context, id string) (Task, error) {
	row, err := r.q.TaskByID(ctx, id)
	if err != nil {
		return Task{}, notFound(err)
	}
	return taskFromRow(row), nil
}

// TaskByStageRun 回傳步驟最近建立的任務。
func (r *Repo) TaskByStageRun(ctx context.Context, stageRunID string) (Task, error) {
	row, err := r.q.TaskByStageRun(ctx, stageRunID)
	if err != nil {
		return Task{}, notFound(err)
	}
	return taskFromRow(row), nil
}

// ClaimTasks 以一條 SQL 原子地領取可以執行的任務（ADR-0004），並把它們的步驟標記為 running。
// 兩個陳述句在同一個交易中：呼叫端已經在 Store.Tx 裡就沿用它，否則（Write）自己開一個。
//
// 可領取：state 為 pending，執行者與資源類別符合，not_before 已到。
// 排序：priority 加上等待時間換算的 aging，再依建立順序。
func (r *Repo) ClaimTasks(ctx context.Context, o ClaimOptions) (claimed []Task, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		var cerr error
		claimed, cerr = tx.claimTasks(ctx, o)
		return cerr
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *Repo) claimTasks(ctx context.Context, o ClaimOptions) ([]Task, error) {
	now := r.nowMs()
	aging := o.AgingInterval.Milliseconds()
	if aging <= 0 {
		aging = 1 << 40 // 實際上不啟用 aging
	}
	classes, err := json.Marshal(nonNil(o.ResourceClasses)) // 空陣列代表不限資源類別
	if err != nil {
		return nil, err
	}

	// 先以同一個暫時的 token 領取，之後逐一換成各任務自己的 token。
	rows, err := r.q.ClaimTasks(ctx, sqlcdb.ClaimTasksParams{
		Owner:           o.Owner,
		Token:           NewID(),
		LeaseUntil:      now + o.Lease.Milliseconds(),
		Now:             now,
		AgingMs:         aging,
		Executor:        o.Executor,
		ResourceClasses: string(classes),
		Max:             int64(o.Max),
	})
	if err != nil {
		return nil, err
	}
	claimed := make([]Task, 0, len(rows))
	for _, row := range rows {
		claimed = append(claimed, taskFromRow(row))
	}
	if len(claimed) == 0 {
		claimed = nil
	}

	// RETURNING 不保證順序：依領取時的排序（優先度加 aging、建立時間、ID）重新排列。
	sort.SliceStable(claimed, func(i, j int) bool {
		ki := int64(claimed[i].Priority) + (now-claimed[i].CreatedAt.UnixMilli())/aging
		kj := int64(claimed[j].Priority) + (now-claimed[j].CreatedAt.UnixMilli())/aging
		if ki != kj {
			return ki > kj
		}
		if !claimed[i].CreatedAt.Equal(claimed[j].CreatedAt) {
			return claimed[i].CreatedAt.Before(claimed[j].CreatedAt)
		}
		return claimed[i].ID < claimed[j].ID // UUIDv7 依產生順序遞增
	})

	for i := range claimed {
		token := NewID()
		if err := r.q.SetTaskLeaseToken(ctx, sqlcdb.SetTaskLeaseTokenParams{LeaseToken: token, ID: claimed[i].ID}); err != nil {
			return nil, err
		}
		claimed[i].LeaseToken = token
		row, err := r.q.MarkStageRunning(ctx, sqlcdb.MarkStageRunningParams{StartedAt: now, ID: claimed[i].StageRunID})
		if errors.Is(err, sql.ErrNoRows) {
			continue // 步驟已不存在：沒有狀態可以發布
		}
		if err != nil {
			return nil, err
		}
		if err := r.emitStage(ctx, row.ItemID, row.Stage, row.Status); err != nil {
			return nil, err
		}
	}
	return claimed, nil
}

// ErrLeaseLost 表示任務已經不在呼叫端的租約之下（租約被放回、到期後被別人領走，或任務已經結束）。
// 寫回結果的操作都要帶 lease_token：舊的持有者晚到的結果不能蓋掉現在的狀態。
var ErrLeaseLost = errors.New("任務的租約已經失效")

// leaseWrite 把「只影響持有該租約的任務」的更新結果轉成錯誤：影響 0 列代表租約已失效。
func leaseWrite(n int64, err error) error {
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// CompleteTask 把 leaseToken 持有的任務標記為 done 並清除租約。租約已失效時回傳 ErrLeaseLost。
func (r *Repo) CompleteTask(ctx context.Context, id, leaseToken string) error {
	return leaseWrite(r.q.CompleteTask(ctx, sqlcdb.CompleteTaskParams{ID: id, LeaseToken: leaseToken}))
}

// FailTask 把 leaseToken 持有的任務標記為 failed 並清除租約。租約已失效時回傳 ErrLeaseLost。
func (r *Repo) FailTask(ctx context.Context, id, leaseToken string) error {
	return leaseWrite(r.q.FailTask(ctx, sqlcdb.FailTaskParams{ID: id, LeaseToken: leaseToken}))
}

// RequeueTask 讓 leaseToken 持有的任務回到 pending，在 notBefore 之後才能再被領取。租約已失效時回傳 ErrLeaseLost。
// countAttempt 為 false 時這次領取不計入 attempts（wait 類的重新檢查）。
func (r *Repo) RequeueTask(ctx context.Context, id, leaseToken string, notBefore *time.Time, countAttempt bool) error {
	return leaseWrite(r.q.RequeueTask(ctx, sqlcdb.RequeueTaskParams{
		NotBefore: msPtr(notBefore), CountAttempt: boolInt(countAttempt), ID: id, LeaseToken: leaseToken,
	}))
}

// QueueEntry 是佇列頁的一列：還沒結束的任務與它所屬的影音。
type QueueEntry struct {
	ID            string
	ItemID        string
	Platform      string
	ExternalID    string
	Part          int
	Stage         string
	ResourceClass string
	State         string // leased 或 pending
	Attempts      int
	Priority      int
	NotBefore     *time.Time
}

// QueueTasks 列出 executor 還沒結束的任務：先是已被領取的，再是 pending 的。
// pending 的排序與 ClaimTasks 完全相同（priority 加 aging、建立順序），到期的在前、還在等 not_before 的在後，
// 所以第一筆 pending 就是下一次領取（max=1）會拿到的任務。
func (r *Repo) QueueTasks(ctx context.Context, executor string, aging time.Duration) ([]QueueEntry, error) {
	aging64 := aging.Milliseconds()
	if aging64 <= 0 {
		aging64 = 1 << 40
	}
	rows, err := r.q.ListQueueTasks(ctx, sqlcdb.ListQueueTasksParams{Now: r.nowMs(), AgingMs: aging64, Executor: executor})
	if err != nil {
		return nil, err
	}
	out := make([]QueueEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, QueueEntry{
			ID: row.ID, ItemID: row.ItemID, Platform: row.Platform, ExternalID: row.ExternalID, Part: int(row.Part),
			Stage: row.Kind, ResourceClass: row.ResourceClass, State: row.State, Attempts: int(row.Attempts),
			Priority: int(row.Priority), NotBefore: msToTime(row.NotBefore),
		})
	}
	return out, nil
}

// ReleaseLeases 把租約放回佇列：不是 keepOwner 持有的，或已經到期的 leased 任務回到 pending，
// 這次領取不計入 attempts，它們的步驟回到 pending（PIPE-R11）。回傳放回的任務數。
func (r *Repo) ReleaseLeases(ctx context.Context, keepOwner string) (n int, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		var rerr error
		n, rerr = tx.releaseLeases(ctx, keepOwner)
		return rerr
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (r *Repo) releaseLeases(ctx context.Context, keepOwner string) (int, error) {
	ids, err := r.q.ReleaseLeases(ctx, sqlcdb.ReleaseLeasesParams{KeepOwner: keepOwner, Now: r.nowMs()})
	if err != nil {
		return 0, err
	}
	return r.resetStages(ctx, ids)
}

// ReleaseOwned 把 owner 持有的全部租約放回佇列（正常關閉，PIPE-R12）。
func (r *Repo) ReleaseOwned(ctx context.Context, owner string) (n int, err error) {
	err = r.inTx(ctx, func(tx *Repo) error {
		var rerr error
		n, rerr = tx.releaseOwned(ctx, owner)
		return rerr
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (r *Repo) releaseOwned(ctx context.Context, owner string) (int, error) {
	ids, err := r.q.ReleaseOwnedLeases(ctx, owner)
	if err != nil {
		return 0, err
	}
	return r.resetStages(ctx, ids)
}

// resetStages 把放回佇列的任務所屬、仍是 running 的步驟回到 pending；回傳任務數。
func (r *Repo) resetStages(ctx context.Context, stageRunIDs []string) (int, error) {
	for _, id := range stageRunIDs {
		row, err := r.q.ResetRunningStage(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue // 步驟不是 running：沒有改變，不發布
		}
		if err != nil {
			return 0, err
		}
		if err := r.emitStage(ctx, row.ItemID, row.Stage, row.Status); err != nil {
			return 0, err
		}
	}
	return len(stageRunIDs), nil
}

// NextTaskTime 回傳 pending 任務中，not_before 晚於 after 的最早時間；沒有這樣的任務時 ok 為 false。
// 已到期的任務不算：領不到的到期任務（類別暫停或已滿）不該遮蔽之後才到期的任務，
// 它們由「通知」喚醒 worker。production 的 worker 用它決定要睡到什麼時候，而不是忙碌輪詢。
func (r *Repo) NextTaskTime(ctx context.Context, executor string, after time.Time) (t time.Time, ok bool, err error) {
	ms, err := r.q.NextTaskTime(ctx, sqlcdb.NextTaskTimeParams{Executor: executor, AfterMs: toMs(after)})
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return fromMs(ms), true, nil
}

// CountTasks 回傳指定狀態的任務數；用於測試與診斷。
func (r *Repo) CountTasks(ctx context.Context, state string) (n int, err error) {
	c, err := r.q.CountTasks(ctx, state)
	return int(c), err
}

// CancelPendingTasks 把指定步驟中還沒被領取的任務標記為 failed，讓它們不再執行。
// 重跑取代了這些步驟時使用；已經被領取的任務不受影響。回傳被取消的任務數。
func (r *Repo) CancelPendingTasks(ctx context.Context, stageRunIDs []string) (int, error) {
	n := 0
	for _, id := range stageRunIDs {
		c, err := r.q.CancelPendingTasks(ctx, id)
		if err != nil {
			return n, err
		}
		n += int(c)
	}
	return n, nil
}

// TaskCancelled 是被取消的任務的狀態（PIPE-R15）：不再被領取，租約也已清除，晚到的寫回不會有任何作用。
const TaskCancelled = "cancelled"

// CancelItemTasks 把影音所有還沒結束（pending 或 leased）的任務改為 cancelled，回傳它們的 ID，
// 讓呼叫端通知 worker 結束其中正在執行的。
func (r *Repo) CancelItemTasks(ctx context.Context, itemID string) ([]string, error) {
	return r.q.CancelItemTasks(ctx, itemID)
}
