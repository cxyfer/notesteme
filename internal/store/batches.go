package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// batches.state（BATCH 的介面）。
const (
	BatchSubmitted  = "submitted"
	BatchCancelling = "cancelling"
	BatchEnded      = "ended"
	BatchCancelled  = "cancelled"
	BatchFailed     = "failed"
)

// TaskWaiting 是等待 batch 結果的任務狀態（BATCH-R3）：不會被領取，也不屬於租約回收的範圍（BATCH-R10）。
const TaskWaiting = "waiting"

// BatchPollKind 是 batch 輪詢的排程工作（ADR-0021 第 1 點），key 是 batches.id。
const BatchPollKind = "batch.poll"

// BatchEntry 是 batch 中的一個請求（batches.task_ids_json 的一項）：custom_id 是任務 ID。
type BatchEntry struct {
	Task        string `json:"task"`
	Run         string `json:"run"`
	Chunk       int    `json:"chunk"`
	Reservation string `json:"reservation,omitempty"`
}

// Batch 是 batches 的一列。
type Batch struct {
	ID          string
	Provider    string
	Model       string
	ExternalID  string
	State       string
	SubmittedAt time.Time
	DeadlineAt  time.Time
	EndedAt     *time.Time
	Entries     []BatchEntry
	Notice      string
}

func batchFromRow(row sqlcdb.Batch) (Batch, error) {
	b := Batch{
		ID: row.ID, Provider: row.Provider, Model: row.Model, ExternalID: row.ExternalID, State: row.State,
		SubmittedAt: fromMs(row.SubmittedAt), DeadlineAt: fromMs(row.DeadlineAt), EndedAt: msToTime(row.EndedAt),
		Notice: row.Notice,
	}
	if err := json.Unmarshal([]byte(row.TaskIdsJson), &b.Entries); err != nil {
		return Batch{}, err
	}
	return b, nil
}

// Open 回報 batch 是否還沒結束（provider 端可能還會產生費用）。
func (b Batch) Open() bool { return b.State == BatchSubmitted || b.State == BatchCancelling }

// InsertBatch 新增一個 batch（state submitted，external_id 之後才有）。
func (r *Repo) InsertBatch(ctx context.Context, b Batch) error {
	entries, err := json.Marshal(b.Entries)
	if err != nil {
		return err
	}
	return r.q.InsertBatch(ctx, sqlcdb.InsertBatchParams{
		ID: b.ID, Provider: b.Provider, Model: b.Model, SubmittedAt: toMs(b.SubmittedAt), DeadlineAt: toMs(b.DeadlineAt),
		TaskIdsJson: string(entries),
	})
}

// Batch 讀取一個 batch；不存在時回傳 ErrNotFound。
func (r *Repo) Batch(ctx context.Context, id string) (Batch, error) {
	row, err := r.q.BatchByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, err
	}
	return batchFromRow(row)
}

// SetBatchExternalID 記下 provider 的 batch ID；已經有的不改。
func (r *Repo) SetBatchExternalID(ctx context.Context, id, external string) error {
	_, err := r.q.SetBatchExternalID(ctx, sqlcdb.SetBatchExternalIDParams{ExternalID: external, ID: id})
	return err
}

// SetBatchState 改變 batch 的狀態；endedAt 與 notice 只在第一次有值時寫入。
func (r *Repo) SetBatchState(ctx context.Context, id, state string, endedAt *time.Time, notice string) error {
	return r.q.SetBatchState(ctx, sqlcdb.SetBatchStateParams{State: state, EndedAt: msPtr(endedAt), Notice: notice, ID: id})
}

// ParkLeasedTask 讓持有租約的任務放下租約、等待 batch 的結果；租約已失效時回傳 ErrLeaseLost。
func (r *Repo) ParkLeasedTask(ctx context.Context, id, leaseToken, batchID string) error {
	return leaseWrite(r.q.ParkLeasedTask(ctx, sqlcdb.ParkLeasedTaskParams{BatchID: &batchID, ID: id, LeaseToken: leaseToken}))
}

// ParkPendingTask 讓還沒被領取的任務加入 batch；任務已經不是 pending 時回傳 false。
func (r *Repo) ParkPendingTask(ctx context.Context, id, batchID string) (bool, error) {
	n, err := r.q.ParkPendingTask(ctx, sqlcdb.ParkPendingTaskParams{BatchID: &batchID, ID: id})
	return n > 0, err
}

// UnparkTask 讓等待 batch 的任務改走一般 API（pending、立即可以領取、換成新的 payload）；
// 任務已經不在這個 batch 中（被移除或已完成）時回傳 false。
func (r *Repo) UnparkTask(ctx context.Context, id, batchID string, payload json.RawMessage) (bool, error) {
	n, err := r.q.UnparkTask(ctx, sqlcdb.UnparkTaskParams{PayloadJson: string(payload), ID: id, BatchID: &batchID})
	return n > 0, err
}

// FinishBatchTask 把等待 batch 的任務標記為 done（結果已寫入 chunk）；任務已經不在這個 batch 中時回傳 false。
func (r *Repo) FinishBatchTask(ctx context.Context, id, batchID string) (bool, error) {
	n, err := r.q.FinishBatchTask(ctx, sqlcdb.FinishBatchTaskParams{ID: id, BatchID: &batchID})
	return n > 0, err
}

// BatchCandidateTasks 列出可以加入 batch 的任務（pending、到期、builtin、llm 類別、優先序不高於 maxPriority 的 chunk 任務），
// 依領取的順序；呼叫端再檢查處理方案、provider 與 model（BATCH-R1、R2）。
func (r *Repo) BatchCandidateTasks(ctx context.Context, excludeID string, maxPriority, limit int) ([]Task, error) {
	rows, err := r.q.BatchCandidateTasks(ctx, sqlcdb.BatchCandidateTasksParams{
		MaxPriority: int64(maxPriority), ExcludeID: excludeID, Now: r.nowMs(), Max: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskFromRow(row))
	}
	return out, nil
}

// WaitingBatchOfRun 回傳 stage run 正在等待的 batch；沒有時回傳 ErrNotFound。
func (r *Repo) WaitingBatchOfRun(ctx context.Context, stageRunID string) (Batch, error) {
	row, err := r.q.WaitingBatchOfRun(ctx, stageRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, err
	}
	return batchFromRow(row)
}

// SetLLMChunkBatch 記下 chunk 的 batch 紀錄（BATCH-R8 的來源紀錄）。
func (r *Repo) SetLLMChunkBatch(ctx context.Context, stageRunID string, chunk int, rec json.RawMessage) error {
	return r.q.SetLLMChunkBatch(ctx, sqlcdb.SetLLMChunkBatchParams{BatchJson: rawOr(rec, "{}"), StageRunID: stageRunID, Chunk: int64(chunk)})
}
