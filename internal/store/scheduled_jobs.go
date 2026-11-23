package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ScheduledJob 是 scheduled_jobs 的一列（ADR-0021 第 1 點）。
type ScheduledJob struct {
	Kind      string
	Key       string
	NextRunAt time.Time
	LastRunAt *time.Time
	LastOKAt  *time.Time
	// LastError 是最近一次失敗的訊息；成功後清空。
	LastError string
	Failures  int
	CreatedAt time.Time
}

// ScheduledJobRun 是一次執行之後要寫回的結果（由 internal/scheduler 計算）。
type ScheduledJobRun struct {
	NextRunAt time.Time
	LastRunAt time.Time
	LastOKAt  *time.Time
	LastError string
	Failures  int
}

func scheduledJobFromRow(r sqlcdb.ScheduledJob) ScheduledJob {
	j := ScheduledJob{
		Kind: r.Kind, Key: r.Key, NextRunAt: fromMs(r.NextRunAt),
		LastRunAt: msToTime(r.LastRunAt), LastOKAt: msToTime(r.LastOkAt),
		Failures: int(r.Failures), CreatedAt: fromMs(r.CreatedAt),
	}
	if r.LastError != nil {
		j.LastError = *r.LastError
	}
	return j
}

// kindsJSON 把 kind 清單轉成查詢用的 JSON 陣列。
func kindsJSON(kinds []string) (string, error) {
	if kinds == nil {
		kinds = []string{}
	}
	b, err := json.Marshal(kinds)
	return string(b), err
}

// ScheduleJob 在 at 排定 (kind, key)；已經存在時只改下一次執行的時間，保留執行紀錄。
// 可以在呼叫端的交易中使用（例如建立訂閱時一併排定第一次檢查）。
func (r *Repo) ScheduleJob(ctx context.Context, kind, key string, at time.Time) error {
	return r.q.UpsertScheduledJob(ctx, sqlcdb.UpsertScheduledJobParams{Kind: kind, Key: key, NextRunAt: at.UnixMilli(), CreatedAt: r.nowMs()})
}

// ScheduleJobIfAbsent 只在 (kind, key) 還沒有排定時排定；回傳是否新增。
func (r *Repo) ScheduleJobIfAbsent(ctx context.Context, kind, key string, at time.Time) (bool, error) {
	n, err := r.q.InsertScheduledJobIfAbsent(ctx, sqlcdb.InsertScheduledJobIfAbsentParams{Kind: kind, Key: key, NextRunAt: at.UnixMilli(), CreatedAt: r.nowMs()})
	return n > 0, err
}

// ScheduledJob 取出一個排程工作；不存在時回傳 ErrNotFound。
func (r *Repo) ScheduledJob(ctx context.Context, kind, key string) (ScheduledJob, error) {
	row, err := r.q.GetScheduledJob(ctx, sqlcdb.GetScheduledJobParams{Kind: kind, Key: key})
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledJob{}, ErrNotFound
	}
	if err != nil {
		return ScheduledJob{}, err
	}
	return scheduledJobFromRow(row), nil
}

// DeleteScheduledJob 結束一個排程工作；回傳是否真的刪除了一列。
func (r *Repo) DeleteScheduledJob(ctx context.Context, kind, key string) (bool, error) {
	n, err := r.q.DeleteScheduledJob(ctx, sqlcdb.DeleteScheduledJobParams{Kind: kind, Key: key})
	return n > 0, err
}

// DueScheduledJobs 回傳 kinds 中 next_run_at 不晚於 now 的工作，依 next_run_at、再依建立順序，最多 max 個。
func (r *Repo) DueScheduledJobs(ctx context.Context, kinds []string, now time.Time, max int) ([]ScheduledJob, error) {
	kj, err := kindsJSON(kinds)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.DueScheduledJobs(ctx, sqlcdb.DueScheduledJobsParams{Now: now.UnixMilli(), Kinds: kj, Max: int64(max)})
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, scheduledJobFromRow(row))
	}
	return out, nil
}

// NextScheduledJobAt 回傳 kinds 中最早的 next_run_at；沒有任何工作時 ok 為 false。
func (r *Repo) NextScheduledJobAt(ctx context.Context, kinds []string) (at time.Time, ok bool, err error) {
	kj, err := kindsJSON(kinds)
	if err != nil {
		return time.Time{}, false, err
	}
	row, err := r.q.NextScheduledJobAt(ctx, kj)
	if err != nil || row.Jobs == 0 {
		return time.Time{}, false, err
	}
	return fromMs(row.NextRunAt), true, nil
}

// RecordScheduledJobRun 寫回一次執行的結果；工作在執行期間被刪除時回傳 ErrNotFound（呼叫端可以忽略）。
func (r *Repo) RecordScheduledJobRun(ctx context.Context, kind, key string, run ScheduledJobRun) error {
	var lastErr *string
	if run.LastError != "" {
		lastErr = &run.LastError
	}
	n, err := r.q.RecordScheduledJobRun(ctx, sqlcdb.RecordScheduledJobRunParams{
		NextRunAt: run.NextRunAt.UnixMilli(), LastRunAt: msPtr(&run.LastRunAt), LastOkAt: msPtr(run.LastOKAt),
		LastError: lastErr, Failures: int64(run.Failures), Kind: kind, Key: key,
	})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
