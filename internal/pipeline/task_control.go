package pipeline

import (
	"context"
	"errors"
	"slices"

	"github.com/cxyfer/notesteme/internal/store"
)

// ErrTaskNotFound 是任務不存在（POST /api/tasks/:id/retry、cancel 回 404）。
var ErrTaskNotFound = errors.New("找不到這個任務。")

// 單一任務的重試與取消被拒絕時的代碼（409）。
const (
	CodeTaskNotRetryable   = "task_not_retryable"
	CodeTaskNotCancellable = "task_not_cancellable"
)

// TaskStateError 是任務（它所屬的步驟）目前的狀態不能重試或取消（409）。
type TaskStateError struct {
	Code string
	Msg  string
}

func (e *TaskStateError) Error() string { return e.Msg }

// 可以手動重試的步驟（PIPE-R4）：failed、blocked，以及被取消的 skipped。
// 其他原因的 skipped（off、not_configured、no_outputs、same_language）是設定決定的，重試也一樣會略過。
func retryableRun(run store.StageRun) bool {
	switch run.Status {
	case store.StatusFailed, store.StatusBlocked:
		return true
	case store.StatusSkipped:
		return run.BlockReason == SkipCancelled
	}
	return false
}

// taskRun 讀出任務、它的步驟與影音，並確認這個步驟是該步驟最新的一筆（重跑取代的舊步驟不能操作）。
// 影音在垃圾桶中時回傳 *InTrashError（DEL-R1）。stale 為 true 代表步驟已被較新的重跑取代。
func taskRun(ctx context.Context, r *store.Repo, taskID string) (t store.Task, run store.StageRun, it store.Item, stale bool, err error) {
	if t, err = r.Task(ctx, taskID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = ErrTaskNotFound
		}
		return
	}
	if run, err = r.StageRun(ctx, t.StageRunID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = ErrTaskNotFound
		}
		return
	}
	if it, err = r.ItemByID(ctx, run.ItemID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			err = ErrTaskNotFound
		}
		return
	}
	if it.DeletedAt != nil {
		err = &InTrashError{ItemID: it.ID}
		return
	}
	runs, err := r.StageRunsByItem(ctx, it.ID)
	if err != nil {
		return
	}
	stale = latestRuns(runs)[run.Stage].ID != run.ID
	return
}

// CancelTask 只取消一個任務所屬的步驟（POST /api/tasks/:id/cancel，語意比照 PIPE-R15）：
// 在一個交易中把步驟改為 skipped（原因 cancelled），同一個步驟所有未結束的任務（LLM 的 plan、chunk、assemble）改為 cancelled。
// 其他步驟不動：下游還沒有任務，之後可以重試這個步驟或從它重新處理。
// 回傳影音 ID 與被取消的任務 ID；呼叫端要在交易完成之後通知 worker 結束其中正在執行的（worker.Cancel）。
//
// 步驟已經是被取消的 skipped 時什麼都不做（冪等，回傳空的清單）；已經結束（done、failed、其他 skipped）或被重跑取代時回傳 *TaskStateError。
func (p *Pipeline) CancelTask(ctx context.Context, taskID string) (itemID string, cancelled []string, err error) {
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		cancelled = nil // 交易可能重試
		_, run, it, stale, err := taskRun(ctx, r, taskID)
		if err != nil {
			return err
		}
		itemID = it.ID
		if stale {
			return &TaskStateError{Code: CodeTaskNotCancellable, Msg: "這個任務所屬的步驟已被重新處理取代，不能取消。"}
		}
		if run.Status == store.StatusSkipped && run.BlockReason == SkipCancelled {
			return nil
		}
		if !slices.Contains(unfinishedStatuses, run.Status) {
			return &TaskStateError{Code: CodeTaskNotCancellable, Msg: "這個任務已經結束，不能取消。"}
		}
		if err := r.SetStageState(ctx, run.ID, cancelledState()); err != nil {
			return err
		}
		cancelled, err = r.CancelStageRunTasks(ctx, run.ID)
		return err
	})
	if err != nil {
		return "", nil, err
	}
	return itemID, cancelled, nil
}

// RetryTask 手動重試一個任務所屬的步驟（POST /api/tasks/:id/retry，PIPE-R4 的「也可以手動重試」）。
// 可以重試的是該步驟最新一筆、狀態為 failed、blocked 或被取消的 skipped，而且沒有未結束的任務；其他情況回傳 *TaskStateError。
//
// 在一個交易中：步驟改回 pending（清掉說明、動作與 resume_on），沿用同一筆 stage run 與設定快照，
// 以原任務的優先順序與資源類別建立新任務，嘗試次數從 0 開始（同 ResumeBlocked）。
// 同一部影音之後被取消的步驟（skipped／cancelled，例如影音層級的取消）改回 pending，完成後照常前進。
// LLM 步驟沿用這筆 stage run 已有的 chunk：已有結果的不再送出（不重複付費）。
func (p *Pipeline) RetryTask(ctx context.Context, taskID string) (itemID string, err error) {
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		t, run, it, stale, err := taskRun(ctx, r, taskID)
		if err != nil {
			return err
		}
		itemID = it.ID
		if stale {
			return &TaskStateError{Code: CodeTaskNotRetryable, Msg: "這個任務所屬的步驟已被重新處理取代，不能重試。"}
		}
		if !retryableRun(run) {
			return &TaskStateError{Code: CodeTaskNotRetryable, Msg: "只有失敗、停住或已取消的步驟可以重試。"}
		}
		n, err := r.CountUnfinishedStageRunTasks(ctx, run.ID)
		if err != nil {
			return err
		}
		if n > 0 {
			return &TaskStateError{Code: CodeTaskNotRetryable, Msg: "這個步驟已經在處理中。"}
		}
		if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
			return err
		}
		if err := reopenCancelledAfter(ctx, r, it.ID, run.Stage); err != nil {
			return err
		}
		// 沿用原任務的優先順序與資源類別：transcript 要不要歸為 asr 是建立任務時的推定，不重新判斷。
		if run.Stage == StageFrames {
			return insertFramesTask(ctx, r, it.ID, run, t.Priority, false)
		}
		_, err = insertStageTaskLike(ctx, r, it.ID, run, t.Priority, &t)
		return err
	})
	if err != nil {
		return "", err
	}
	p.wake()
	return itemID, nil
}

// reopenCancelledAfter 把影音在 stage 之後、最新一筆是被取消的 skipped 的步驟改回 pending（不建任務，等上游完成再前進）。
// 移到垃圾桶的步驟不會走到這裡（垃圾桶中的影音不能重試）。
func reopenCancelledAfter(ctx context.Context, r *store.Repo, itemID, stage string) error {
	i := slices.Index(Stages, stage)
	if i < 0 {
		return nil
	}
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return err
	}
	latest := latestRuns(runs)
	for _, s := range Stages[i+1:] {
		if run, ok := latest[s]; ok && run.Status == store.StatusSkipped && run.BlockReason == SkipCancelled {
			if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
				return err
			}
		}
	}
	return nil
}
