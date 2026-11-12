package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const taskColumns = `id, stage_run_id, kind, executor, resource_class, state, priority, payload_json, chunk_index,
	COALESCE(lease_token, ''), COALESCE(lease_owner, ''), lease_until, attempts, max_attempts, not_before, deadline_at, created_at`

func scanTask(sc scanner) (Task, error) {
	var (
		t                             Task
		payload                       string
		leaseUntil, notBefore, deadln *int64
		created                       int64
	)
	err := sc.Scan(&t.ID, &t.StageRunID, &t.Kind, &t.Executor, &t.ResourceClass, &t.State, &t.Priority, &payload,
		&t.ChunkIndex, &t.LeaseToken, &t.LeaseOwner, &leaseUntil, &t.Attempts, &t.MaxAttempts, &notBefore, &deadln, &created)
	if err != nil {
		return Task{}, err
	}
	t.Payload = json.RawMessage(payload)
	t.LeaseUntil, t.NotBefore, t.DeadlineAt = msToTime(leaseUntil), msToTime(notBefore), msToTime(deadln)
	t.CreatedAt = fromMs(created)
	return t, nil
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
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO tasks (id, stage_run_id, kind, executor, resource_class, state, priority, payload_json,
		                    max_attempts, not_before, deadline_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, n.StageRunID, n.Kind, n.Executor, n.ResourceClass, TaskPending, n.Priority, string(payload),
		maxAttempts, msPtr(n.NotBefore), msPtr(n.DeadlineAt), r.nowMs())
	if err != nil {
		return Task{}, err
	}
	return r.Task(ctx, id)
}

// Task 以 ID 查詢任務。
func (r *Repo) Task(ctx context.Context, id string) (Task, error) {
	t, err := scanTask(r.q.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Task{}, ErrNotFound
	}
	return t, err
}

// TaskByStageRun 回傳步驟最近建立的任務。
func (r *Repo) TaskByStageRun(ctx context.Context, stageRunID string) (Task, error) {
	t, err := scanTask(r.q.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE stage_run_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, stageRunID))
	if err == sql.ErrNoRows {
		return Task{}, ErrNotFound
	}
	return t, err
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
	// 參數依照在 SQL 中出現的順序。
	args := []any{o.Owner, NewID(), now + o.Lease.Milliseconds(), now, o.Executor}
	classFilter := ""
	if len(o.ResourceClasses) > 0 {
		classFilter = " AND resource_class IN (" + strings.TrimSuffix(strings.Repeat("?,", len(o.ResourceClasses)), ",") + ")"
		for _, c := range o.ResourceClasses {
			args = append(args, c)
		}
	}
	args = append(args, now, aging, o.Max)

	// 先以同一個暫時的 token 領取，之後逐一換成各任務自己的 token。
	rows, err := r.q.QueryContext(ctx,
		`UPDATE tasks
		 SET state = 'leased', lease_owner = ?, lease_token = ?, lease_until = ?, attempts = attempts + 1
		 WHERE id IN (
		   SELECT id FROM tasks
		   WHERE state = 'pending' AND (not_before IS NULL OR not_before <= ?) AND executor = ?`+classFilter+`
		   ORDER BY priority + (? - created_at) / ? DESC, created_at, rowid
		   LIMIT ?
		 )
		 RETURNING `+taskColumns, args...)
	if err != nil {
		return nil, err
	}
	var claimed []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		claimed = append(claimed, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

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
		if _, err := r.q.ExecContext(ctx, `UPDATE tasks SET lease_token = ? WHERE id = ?`, token, claimed[i].ID); err != nil {
			return nil, err
		}
		claimed[i].LeaseToken = token
		if _, err := r.q.ExecContext(ctx,
			`UPDATE stage_runs SET status = 'running', block_reason = '', error_class = '', error_message = '',
			   actions_json = '[]', not_before = NULL, started_at = ?, finished_at = NULL WHERE id = ?`,
			now, claimed[i].StageRunID); err != nil {
			return nil, err
		}
	}
	return claimed, nil
}

// CompleteTask 把任務標記為 done 並清除租約。
func (r *Repo) CompleteTask(ctx context.Context, id string) error {
	_, err := r.q.ExecContext(ctx,
		`UPDATE tasks SET state = 'done', lease_token = NULL, lease_owner = NULL, lease_until = NULL WHERE id = ?`, id)
	return err
}

// FailTask 把任務標記為 failed 並清除租約。
func (r *Repo) FailTask(ctx context.Context, id string) error {
	_, err := r.q.ExecContext(ctx,
		`UPDATE tasks SET state = 'failed', lease_token = NULL, lease_owner = NULL, lease_until = NULL WHERE id = ?`, id)
	return err
}

// RequeueTask 讓任務回到 pending，在 notBefore 之後才能再被領取。
// countAttempt 為 false 時這次領取不計入 attempts（wait 類的重新檢查、重啟時放回的租約）。
func (r *Repo) RequeueTask(ctx context.Context, id string, notBefore *time.Time, countAttempt bool) error {
	_, err := r.q.ExecContext(ctx,
		`UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
		   not_before = ?, attempts = CASE WHEN ? THEN attempts ELSE MAX(attempts - 1, 0) END
		 WHERE id = ?`, msPtr(notBefore), countAttempt, id)
	return err
}

// ReleaseLeases 把租約放回佇列：不是 keepOwner 持有的，或已經到期的 leased 任務回到 pending，
// 這次領取不計入 attempts，它們的步驟回到 pending（PIPE-R11）。回傳放回的任務數。
func (r *Repo) ReleaseLeases(ctx context.Context, keepOwner string) (int, error) {
	return r.releaseLeases(ctx, `lease_owner <> ? OR lease_until <= ?`, keepOwner, r.nowMs())
}

// ReleaseOwned 把 owner 持有的全部租約放回佇列（正常關閉，PIPE-R12）。
func (r *Repo) ReleaseOwned(ctx context.Context, owner string) (int, error) {
	return r.releaseLeases(ctx, `lease_owner = ?`, owner)
}

func (r *Repo) releaseLeases(ctx context.Context, cond string, args ...any) (int, error) {
	rows, err := r.q.QueryContext(ctx,
		`UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
		   attempts = MAX(attempts - 1, 0)
		 WHERE state = 'leased' AND (`+cond+`) RETURNING stage_run_id`, args...)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := r.q.ExecContext(ctx, `UPDATE stage_runs SET status = 'pending' WHERE id = ? AND status = 'running'`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// NextTaskTime 回傳最早可以領取的 pending 任務的時間（not_before 為空或已到期時是現在）；沒有 pending 任務時 ok 為 false。
// production 的 worker 用它決定要睡到什麼時候，而不是忙碌輪詢。
func (r *Repo) NextTaskTime(ctx context.Context, executor string) (t time.Time, ok bool, err error) {
	var ms *int64
	err = r.q.QueryRowContext(ctx,
		`SELECT MIN(COALESCE(not_before, 0)) FROM tasks WHERE state = 'pending' AND executor = ?`, executor).Scan(&ms)
	if err != nil || ms == nil {
		return time.Time{}, false, err
	}
	t = fromMs(*ms)
	if now := r.clock.Now(); t.Before(now) {
		t = now
	}
	return t, true, nil
}

// CountTasks 回傳指定狀態的任務數；用於測試與診斷。
func (r *Repo) CountTasks(ctx context.Context, state string) (n int, err error) {
	err = r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE state = ?`, state).Scan(&n)
	return n, err
}

// CancelPendingTasks 把指定步驟中還沒被領取的任務標記為 failed，讓它們不再執行。
// 重跑取代了這些步驟時使用；已經被領取的任務不受影響。回傳被取消的任務數。
func (r *Repo) CancelPendingTasks(ctx context.Context, stageRunIDs []string) (int, error) {
	n := 0
	for _, id := range stageRunIDs {
		res, err := r.q.ExecContext(ctx, `UPDATE tasks SET state = 'failed' WHERE stage_run_id = ? AND state = 'pending'`, id)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += int(c)
	}
	return n, nil
}
