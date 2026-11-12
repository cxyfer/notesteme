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
// 兩個陳述句必須在同一個交易中（用 Store.Tx）。
//
// 可領取：state 為 pending，執行者與資源類別符合，not_before 已到。
// 排序：priority 加上等待時間換算的 aging，再依建立順序。
func (r *Repo) ClaimTasks(ctx context.Context, o ClaimOptions) ([]Task, error) {
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
		if err := r.q.MarkStageRunning(ctx, sqlcdb.MarkStageRunningParams{StartedAt: now, ID: claimed[i].StageRunID}); err != nil {
			return nil, err
		}
	}
	return claimed, nil
}

// CompleteTask 把任務標記為 done 並清除租約。
func (r *Repo) CompleteTask(ctx context.Context, id string) error {
	return r.q.CompleteTask(ctx, id)
}

// FailTask 把任務標記為 failed 並清除租約。
func (r *Repo) FailTask(ctx context.Context, id string) error {
	return r.q.FailTask(ctx, id)
}

// RequeueTask 讓任務回到 pending，在 notBefore 之後才能再被領取。
// countAttempt 為 false 時這次領取不計入 attempts（wait 類的重新檢查、重啟時放回的租約）。
func (r *Repo) RequeueTask(ctx context.Context, id string, notBefore *time.Time, countAttempt bool) error {
	return r.q.RequeueTask(ctx, sqlcdb.RequeueTaskParams{NotBefore: msPtr(notBefore), CountAttempt: boolInt(countAttempt), ID: id})
}

// ReleaseLeases 把租約放回佇列：不是 keepOwner 持有的，或已經到期的 leased 任務回到 pending，
// 這次領取不計入 attempts，它們的步驟回到 pending（PIPE-R11）。回傳放回的任務數。
func (r *Repo) ReleaseLeases(ctx context.Context, keepOwner string) (int, error) {
	ids, err := r.q.ReleaseLeases(ctx, sqlcdb.ReleaseLeasesParams{KeepOwner: keepOwner, Now: r.nowMs()})
	if err != nil {
		return 0, err
	}
	return r.resetStages(ctx, ids)
}

// ReleaseOwned 把 owner 持有的全部租約放回佇列（正常關閉，PIPE-R12）。
func (r *Repo) ReleaseOwned(ctx context.Context, owner string) (int, error) {
	ids, err := r.q.ReleaseOwnedLeases(ctx, owner)
	if err != nil {
		return 0, err
	}
	return r.resetStages(ctx, ids)
}

// resetStages 把放回佇列的任務所屬、仍是 running 的步驟回到 pending；回傳任務數。
func (r *Repo) resetStages(ctx context.Context, stageRunIDs []string) (int, error) {
	for _, id := range stageRunIDs {
		if err := r.q.ResetRunningStage(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(stageRunIDs), nil
}

// NextTaskTime 回傳最早可以領取的 pending 任務的時間（not_before 為空或已到期時是現在）；沒有 pending 任務時 ok 為 false。
// production 的 worker 用它決定要睡到什麼時候，而不是忙碌輪詢。
func (r *Repo) NextTaskTime(ctx context.Context, executor string) (t time.Time, ok bool, err error) {
	ms, err := r.q.NextTaskTime(ctx, executor)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t = fromMs(ms)
	if now := r.clock.Now(); t.Before(now) {
		t = now
	}
	return t, true, nil
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
