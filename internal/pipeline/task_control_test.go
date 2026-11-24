package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// 單一任務的重試與取消（POST /api/tasks/:id/retry、cancel）。

func latestTask(t *testing.T, r *rig, run store.StageRun) store.Task {
	t.Helper()
	tk, err := r.st.Read().TaskByStageRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("步驟 %s 的任務：%v", run.Stage, err)
	}
	return tk
}

func setProbeErr(r *rig, err error) {
	r.yt.mu.Lock()
	r.yt.probeErr = err
	r.yt.mu.Unlock()
}

func wantTaskState(t *testing.T, err error, code string) {
	t.Helper()
	var se *TaskStateError
	if !errors.As(err, &se) || se.Code != code || se.Msg == "" {
		t.Fatalf("err = %v，預期 %s", err, code)
	}
}

// 沒有 resume_on 的 blocked（例如缺 API key）不會自動恢復，只能手動重試（PIPE-R4）：
// 步驟回到 pending、說明清空，新任務沿用優先順序與資源類別、嘗試次數從 0 開始，修正後能跑完整條 pipeline。
func TestRetryTaskBlockedRunsToTheEnd(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	setProbeErr(r, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Message: "需要設定。", Actions: []string{"開啟設定"}})
	res, err := r.p.Ingest(ctx, IngestRequest{URL: watch, Priority: queue.PriorityBackfill})
	if err != nil {
		t.Fatal(err)
	}
	it := res[0].Item
	r.run()
	meta := r.stage(it, StageMetadata)
	if meta.Status != store.StatusBlocked || meta.ResumeOn != "" {
		t.Fatalf("前置條件：metadata = %+v", meta)
	}
	old := latestTask(t, r, meta)

	setProbeErr(r, nil)
	itemID, err := r.p.RetryTask(ctx, old.ID)
	if err != nil || itemID != it.ID {
		t.Fatalf("RetryTask = %q, %v", itemID, err)
	}
	st := r.stage(it, StageMetadata)
	if st.Status != store.StatusPending || st.BlockReason != "" || st.Message != "" || len(st.Actions) != 0 || st.ID != meta.ID {
		t.Errorf("重試後的步驟 = %+v，預期同一筆 stage run 回到 pending", st)
	}
	tk := latestTask(t, r, st)
	if tk.ID == old.ID || tk.State != store.TaskPending || tk.Attempts != 0 || tk.Priority != queue.PriorityBackfill || tk.ResourceClass != old.ResourceClass {
		t.Errorf("新任務 = %+v，原任務 = %+v", tk, old)
	}
	// 已經有任務在等待：再按一次重試不會建立第二個任務。
	_, err = r.p.RetryTask(ctx, old.ID)
	wantTaskState(t, err, CodeTaskNotRetryable)

	r.run()
	for _, s := range Stages[:3] {
		if got := r.stage(it, s); got.Status != store.StatusDone {
			t.Errorf("重試後 %s = %+v，預期 done", s, got)
		}
	}
	// 已經完成的步驟不能重試。
	_, err = r.p.RetryTask(ctx, old.ID)
	wantTaskState(t, err, CodeTaskNotRetryable)
}

// permanent 失敗的步驟也可以手動重試。
func TestRetryTaskFailedPermanent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	setProbeErr(r, &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Detail: "gone"})
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	meta := r.stage(it, StageMetadata)
	if meta.Status != store.StatusFailed {
		t.Fatalf("前置條件：metadata = %+v", meta)
	}
	setProbeErr(r, nil)
	if _, err := r.p.RetryTask(ctx, latestTask(t, r, meta).ID); err != nil {
		t.Fatal(err)
	}
	r.run()
	if got := r.stage(it, StageMetadata); got.Status != store.StatusDone || got.ErrorClass != "" {
		t.Errorf("重試後 metadata = %+v", got)
	}
}

// 取消單一任務：只有它的步驟變成 skipped（cancelled），任務變成 cancelled；其他步驟不動，
// 之後也不會被執行。重複取消是冪等的；重試被取消的步驟會接著跑完後面的步驟。
func TestCancelTaskThenRetry(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	meta := r.stage(it, StageMetadata)
	tk := latestTask(t, r, meta)
	evs := recordEvents(r.st)

	itemID, ids, err := r.p.CancelTask(ctx, tk.ID)
	if err != nil || itemID != it.ID || len(ids) != 1 || ids[0] != tk.ID {
		t.Fatalf("CancelTask = %q %v, %v", itemID, ids, err)
	}
	if st := r.stage(it, StageMetadata); st.Status != store.StatusSkipped || st.BlockReason != SkipCancelled || st.FinishedAt == nil {
		t.Errorf("取消後 metadata = %+v", st)
	}
	if got, _ := r.st.Read().Task(ctx, tk.ID); got.State != store.TaskCancelled || got.LeaseToken != "" {
		t.Errorf("任務 = %+v，預期 cancelled", got)
	}
	for _, s := range Stages[1:] {
		if st := r.stage(it, s); st.Status != store.StatusPending {
			t.Errorf("其他步驟 %s 不應改動：%+v", s, st)
		}
	}
	r.run()
	if r.yt.probes != 0 {
		t.Errorf("取消後不應執行，Probe 被呼叫 %d 次", r.yt.probes)
	}
	if _, ids, err := r.p.CancelTask(ctx, tk.ID); err != nil || len(ids) != 0 {
		t.Errorf("再取消一次 = %v, %v，預期冪等", ids, err)
	}

	// 取消與重試都發布 stage.status（PIPE-R14），畫面才會更新。
	if got := stageEvents(evs()); got["metadata/skipped"] != 1 || got["metadata/pending"] != 0 {
		t.Errorf("取消的事件 = %v", got)
	}
	if _, err := r.p.RetryTask(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if got := stageEvents(evs()); got["metadata/pending"] != 1 {
		t.Errorf("重試的事件 = %v", got)
	}
	r.run()
	for _, s := range Stages[:3] {
		if st := r.stage(it, s); st.Status != store.StatusDone {
			t.Errorf("重試後 %s = %+v，預期 done", s, st)
		}
	}
	// 已經完成的步驟不能取消。
	_, _, err = r.p.CancelTask(ctx, tk.ID)
	wantTaskState(t, err, CodeTaskNotCancellable)
}

// 影音層級的取消（PIPE-R15）把後面的步驟也改成 skipped（cancelled）；重試第一個步驟後，它們回到 pending 並照常前進。
func TestRetryAfterItemCancelReopensLaterStages(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	tk := latestTask(t, r, r.stage(it, StageMetadata))
	if _, err := r.p.CancelItem(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.RetryTask(ctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	for _, s := range Stages[1:] {
		if st := r.stage(it, s); st.Status != store.StatusPending || st.BlockReason != "" {
			t.Errorf("重試後 %s = %+v，預期回到 pending", s, st)
		}
	}
	r.run()
	for _, s := range Stages[:3] {
		if st := r.stage(it, s); st.Status != store.StatusDone {
			t.Errorf("%s = %+v，預期 done", s, st)
		}
	}
}

// 被重跑取代的步驟、不存在的任務、垃圾桶中的影音都不能操作。
func TestTaskControlRejects(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	old := latestTask(t, r, r.stage(it, StageNormalize))
	r.rerun(it.ID, StageTranscript, nil)
	_, err := r.p.RetryTask(ctx, old.ID)
	wantTaskState(t, err, CodeTaskNotRetryable)
	_, _, err = r.p.CancelTask(ctx, old.ID)
	wantTaskState(t, err, CodeTaskNotCancellable)

	if _, err := r.p.RetryTask(ctx, "no-such-task"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("不存在的任務 = %v", err)
	}
	if _, _, err := r.p.CancelTask(ctx, "no-such-task"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("不存在的任務 = %v", err)
	}

	cur := latestTask(t, r, latestStage(t, r, it.ID, StageNormalize))
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var tr *InTrashError
	if _, err := r.p.RetryTask(ctx, cur.ID); !errors.As(err, &tr) || tr.ItemID != it.ID {
		t.Errorf("垃圾桶中的影音重試 = %v", err)
	}
	if _, _, err := r.p.CancelTask(ctx, cur.ID); !errors.As(err, &tr) {
		t.Errorf("垃圾桶中的影音取消 = %v", err)
	}
}

func TestRetryableRun(t *testing.T) {
	for _, c := range []struct {
		status, reason string
		want           bool
	}{
		{store.StatusFailed, "", true},
		{store.StatusBlocked, "needs_action", true},
		{store.StatusBlocked, "budget_blocked", true},
		{store.StatusSkipped, SkipCancelled, true},
		{store.StatusSkipped, "off", false},
		{store.StatusSkipped, "not_configured", false},
		{store.StatusSkipped, "same_language", false},
		{store.StatusSkipped, "", false},
		{store.StatusDone, "", false},
		{store.StatusPending, "", false},
		{store.StatusRunning, "", false},
		{store.StatusWaiting, "", false},
		{store.StatusAwaitingAgent, "", false},
	} {
		if got := retryableRun(store.StageRun{Status: c.status, BlockReason: c.reason}); got != c.want {
			t.Errorf("%s/%s = %v，預期 %v", c.status, c.reason, got, c.want)
		}
	}
}

// LLM 步驟停住後重試：沿用同一筆 stage run 已完成的 chunk，只重新送出沒有結果的（不重複付費）。
func TestRetryTaskLLMReusesDoneChunks(t *testing.T) {
	fake := &chunkLLM{behave: map[int]string{3: "404"}}
	r, it := newLLMRig(t, fake)
	rerunCorrect(t, r, it)
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusBlocked || fake.callCount() != 3 {
		t.Fatalf("前置條件：correct = %+v，請求 %v", st, fake.calls)
	}
	fake.mu.Lock()
	fake.behave = map[int]string{}
	fake.mu.Unlock()
	if _, err := r.p.RetryTask(context.Background(), latestTask(t, r, st).ID); err != nil {
		t.Fatal(err)
	}
	r.run()
	if got := latestStage(t, r, it.ID, StageCorrect); got.Status != store.StatusDone || got.ID != st.ID {
		t.Fatalf("重試後 correct = %+v", got)
	}
	if n := fake.callCount(); n != 5 || fake.calls[3] != 3 || fake.calls[4] != 4 {
		t.Errorf("只重新送出 chunk 3、4：%v", fake.calls)
	}
}

// 新任務沿用原任務的資源類別：transcript 歸為 asr 是建立任務時的推定，重試不重新判斷（同 ResumeBlocked）。
func TestRetryTaskKeepsASRClass(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, fallbackOverride("groq"), StageTranscript)
	r.run()
	tr := r.stage(it, StageTranscript)
	old := latestTask(t, r, tr)
	if !retryableRun(tr) || old.ResourceClass != queue.ResourceASR {
		t.Fatalf("前置條件：transcript = %+v，任務 = %+v", tr, old)
	}
	if _, err := r.p.RetryTask(context.Background(), old.ID); err != nil {
		t.Fatal(err)
	}
	if tk := latestTask(t, r, tr); tk.ID == old.ID || tk.ResourceClass != queue.ResourceASR {
		t.Errorf("新任務 = %+v，預期 asr", tk)
	}
}

// 被重跑取代、但自己仍是 blocked 的舊步驟（Rerun 不改動 blocked 的舊步驟）：不能重試，也不能取消。
func TestTaskControlRejectsSupersededBlockedRun(t *testing.T) {
	r, it := blockedRig(t)
	ctx := context.Background()
	old := latestTask(t, r, r.stage(it, StageMetadata))
	r.rerun(it.ID, StageMetadata, nil)
	if st, _ := r.st.Read().StageRun(ctx, old.StageRunID); st.Status != store.StatusBlocked {
		t.Fatalf("前置條件：舊步驟 = %+v", st)
	}
	_, err := r.p.RetryTask(ctx, old.ID)
	wantTaskState(t, err, CodeTaskNotRetryable)
	_, _, err = r.p.CancelTask(ctx, old.ID)
	wantTaskState(t, err, CodeTaskNotCancellable)
}

// 步驟已經停住、但同一個步驟還有未結束的任務（例如晚到的 chunk 還在執行）：不重試，否則會有兩組任務同時跑。
func TestRetryTaskRefusesWhileTasksUnfinished(t *testing.T) {
	r, it := blockedRig(t)
	ctx := context.Background()
	run := r.stage(it, StageMetadata)
	if err := r.st.Tx(ctx, func(tx *store.Repo) error {
		_, err := insertStageTask(ctx, tx, it.ID, run, queue.PriorityManual, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, err := r.p.RetryTask(ctx, latestTask(t, r, run).ID)
	wantTaskState(t, err, CodeTaskNotRetryable)
}
