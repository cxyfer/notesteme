package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// AGT：Agent 領取的任務（executor 為 agent）。領取沿用 ClaimTasks（同一條原子的 SQL，AGT-R3）；
// 這裡是租約的延長、放回、提交失敗的計數，以及 sweeper 的查詢。寫入都要帶 lease_token。

// ExecutorAgent 是 Agent 執行的任務（tasks.executor）。
const ExecutorAgent = "agent"

// CountAgentHeld 回傳 owner 目前持有、資源類別為 class 的 Agent 任務數。
func (r *Repo) CountAgentHeld(ctx context.Context, owner, class string) (int, error) {
	n, err := r.q.CountAgentHeld(ctx, sqlcdb.CountAgentHeldParams{Owner: owner, ResourceClass: class})
	return int(n), err
}

// AgentTask 以 ID 讀取 Agent 任務（任何狀態）；不存在或不是 Agent 任務時回傳 ErrNotFound。
func (r *Repo) AgentTask(ctx context.Context, id string) (Task, error) {
	row, err := r.q.AgentTaskForLease(ctx, id)
	if err != nil {
		return Task{}, notFound(err)
	}
	return taskFromRow(row), nil
}

// ExtendAgentLease 把 owner 持有、還沒到期的租約延長到 until。租約已失效或已到期時回傳 ErrLeaseLost。
func (r *Repo) ExtendAgentLease(ctx context.Context, id, token, owner string, until time.Time) error {
	return leaseWrite(r.q.ExtendAgentLease(ctx, sqlcdb.ExtendAgentLeaseParams{
		LeaseUntil: toMs(until), ID: id, LeaseToken: token, Owner: owner, Now: r.nowMs(),
	}))
}

// AddAgentFailure 記一次驗證失敗的提交，回傳這份租約下累計的次數。租約已失效時回傳 ErrLeaseLost。
func (r *Repo) AddAgentFailure(ctx context.Context, id, token string) (int, error) {
	n, err := r.q.AddAgentFailure(ctx, sqlcdb.AddAgentFailureParams{ID: id, LeaseToken: token})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrLeaseLost
	}
	return int(n), err
}

// RequeueAgentTask 把 Agent 持有的任務放回佇列。countAttempt 為 false 時這次領取不計入 attempts（AGT-R8）；
// deadline 是新的領取期限（AGT-R9），nil 代表沒有。租約已失效時回傳 ErrLeaseLost。
func (r *Repo) RequeueAgentTask(ctx context.Context, id, token, reason string, countAttempt bool, deadline *time.Time) error {
	return leaseWrite(r.q.RequeueAgentTask(ctx, sqlcdb.RequeueAgentTaskParams{
		ReleaseReason: reason, DeadlineAt: msPtr(deadline), CountAttempt: boolInt(countAttempt), ID: id, LeaseToken: token,
	}))
}

// ExpiredAgentLeases 列出租約已到期、仍是 leased 的 Agent 任務（AGT-R4）。
func (r *Repo) ExpiredAgentLeases(ctx context.Context) ([]Task, error) {
	return tasksFrom(r.q.ExpiredAgentLeases(ctx, r.nowMs()))
}

// TimedOutAgentTasks 列出超過領取期限仍沒有被領取的 Agent 任務（AGT-R9）。
func (r *Repo) TimedOutAgentTasks(ctx context.Context) ([]Task, error) {
	return tasksFrom(r.q.TimedOutAgentTasks(ctx, r.nowMs()))
}

func tasksFrom(rows []sqlcdb.Task, err error) ([]Task, error) {
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskFromRow(row))
	}
	return out, nil
}

// NextAgentTime 回傳 sweeper 下一次有事要做的時間：最早到期的租約或領取期限；都沒有時 ok 為 false。
func (r *Repo) NextAgentTime(ctx context.Context) (time.Time, bool, error) {
	ms, err := r.q.NextAgentTime(ctx)
	if err != nil || ms == 0 {
		return time.Time{}, false, err
	}
	return fromMs(ms), true, nil
}

// ClearAgentDeadlines 清掉 stage run 中等待領取的 Agent 任務的期限（on_timeout: notify 只通知一次）。
func (r *Repo) ClearAgentDeadlines(ctx context.Context, stageRunID string) error {
	return r.q.ClearAgentDeadlines(ctx, stageRunID)
}

// SwitchAgentTasksToBuiltin 把 stage run 中還沒完成的 Agent 任務交給內建 worker（改用 API，AGT-R9、R11）：
// 被領取中的租約立即失效。回傳改變的任務 ID。
func (r *Repo) SwitchAgentTasksToBuiltin(ctx context.Context, stageRunID string) ([]string, error) {
	return r.q.SwitchAgentTasksToBuiltin(ctx, stageRunID)
}

// CountRunTasks 回傳 stage run 中 executor、state 相符的任務數。
func (r *Repo) CountRunTasks(ctx context.Context, stageRunID, executor, state string) (int, error) {
	n, err := r.q.CountRunTasks(ctx, sqlcdb.CountRunTasksParams{StageRunID: stageRunID, Executor: executor, State: state})
	return int(n), err
}

// AwaitAgentStage 把 running 或 pending 的步驟改為 awaiting_agent（AGT-R1）並發布事件；其他狀態不變。
// 回傳是否改變了。
func (r *Repo) AwaitAgentStage(ctx context.Context, id string) (bool, error) {
	row, err := r.q.AwaitAgentStage(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, r.emitStage(ctx, row.ItemID, row.Stage, row.Status)
}

// SwitchAgentTaskToBuiltin 把一個還沒完成的 Agent 任務交給內建 worker；回傳是否改變了。
func (r *Repo) SwitchAgentTaskToBuiltin(ctx context.Context, id string) (bool, error) {
	ids, err := r.q.SwitchAgentTaskToBuiltin(ctx, id)
	return len(ids) > 0, err
}
