package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type rig struct {
	st  *store.Store
	clk *clock.Fake
	q   *queue.Queue
}

func newRig(t *testing.T) *rig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &rig{st: st, clk: clk, q: queue.New(st, clk, "w1")}
}

func (r *rig) addTask(t *testing.T, ext string, nb *time.Time) store.Task {
	t.Helper()
	ctx := context.Background()
	var tk store.Task
	err := r.st.Tx(ctx, func(rp *store.Repo) error {
		it, _, err := rp.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "u"})
		if err != nil {
			return err
		}
		snap, err := rp.EnsureSnapshot(ctx, "sha", "{}")
		if err != nil {
			return err
		}
		sr, err := rp.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: "metadata", Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			return err
		}
		tk, err = rp.InsertTask(ctx, store.NewTask{StageRunID: sr.ID, Kind: "metadata", Executor: queue.ExecutorBuiltin,
			ResourceClass: queue.ResourceDownload, Priority: queue.PriorityManual, NotBefore: nb})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// recorder 記錄處理過的任務，並把它們標記為 done。
type recorder struct {
	st   *store.Store
	mu   sync.Mutex
	seen []string
	fn   func(store.Task) error
}

func (h *recorder) Handle(ctx context.Context, t store.Task) error {
	h.mu.Lock()
	h.seen = append(h.seen, t.ID)
	h.mu.Unlock()
	if h.fn != nil {
		if err := h.fn(t); err != nil {
			return err
		}
	}
	return h.st.Tx(ctx, func(r *store.Repo) error {
		if err := r.CompleteTask(ctx, t.ID); err != nil {
			return err
		}
		return r.SetStageState(ctx, t.StageRunID, store.StageState{Status: store.StatusDone})
	})
}

func TestRunUntilIdleExecutesEveryRunnableTask(t *testing.T) {
	r := newRig(t)
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		r.addTask(t, id, nil)
	}
	h := &recorder{st: r.st}
	w := New(r.q, h, r.clk, quiet)
	if err := w.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.seen) != 6 {
		t.Errorf("處理了 %d 個任務，預期 6（超過同時執行上限時要分輪）", len(h.seen))
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskDone); n != 6 {
		t.Errorf("done = %d", n)
	}
}

func TestRunUntilIdleSkipsTasksNotDueYet(t *testing.T) {
	r := newRig(t)
	later := r.clk.Now().Add(2 * time.Hour)
	r.addTask(t, "a", &later)
	h := &recorder{st: r.st}
	w := New(r.q, h, r.clk, quiet)

	if err := w.RunUntilIdle(context.Background()); err != nil || len(h.seen) != 0 {
		t.Fatalf("not_before 未到不應執行：%v, %v", h.seen, err)
	}
	r.clk.Advance(2 * time.Hour)
	if err := w.RunUntilIdle(context.Background()); err != nil || len(h.seen) != 1 {
		t.Fatalf("時間到了應執行：%v, %v", h.seen, err)
	}
}

func TestRunRoundMarksRunningWithoutWaiting(t *testing.T) {
	r := newRig(t)
	tk := r.addTask(t, "a", nil)
	gate := make(chan struct{})
	h := &recorder{st: r.st, fn: func(store.Task) error { <-gate; return nil }}
	w := New(r.q, h, r.clk, quiet)

	n, err := w.RunRound(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RunRound = %d, %v", n, err)
	}
	run, _ := r.st.Read().StageRun(context.Background(), tk.StageRunID)
	if run.Status != store.StatusRunning {
		t.Errorf("RunRound 回傳時步驟應為 running，實際 %s", run.Status)
	}
	close(gate)
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
	run, _ = r.st.Read().StageRun(context.Background(), tk.StageRunID)
	if run.Status != store.StatusDone {
		t.Errorf("完成後步驟應為 done，實際 %s", run.Status)
	}
}

func TestHandlerErrorsAndPanicsAreReported(t *testing.T) {
	r := newRig(t)
	r.addTask(t, "a", nil)
	boom := errors.New("boom")
	w := New(r.q, &recorder{st: r.st, fn: func(store.Task) error { return boom }}, r.clk, quiet)
	if err := w.RunUntilIdle(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}

	r2 := newRig(t)
	r2.addTask(t, "a", nil)
	w2 := New(r2.q, &recorder{st: r2.st, fn: func(store.Task) error { panic("oops") }}, r2.clk, quiet)
	if err := w2.RunUntilIdle(context.Background()); err == nil {
		t.Error("panic 應轉成錯誤")
	}
}

func TestRunUntilIdleGivesUpAfterMaxRounds(t *testing.T) {
	r := newRig(t)
	r.addTask(t, "a", nil)
	// 每次處理完都再放回佇列，永遠有事可做。
	h := &recorder{st: r.st}
	h2 := HandlerFunc(func(ctx context.Context, tk store.Task) error {
		return r.st.Tx(ctx, func(rp *store.Repo) error { return rp.RequeueTask(ctx, tk.ID, nil, false) })
	})
	_ = h
	w := New(r.q, h2, r.clk, quiet)
	w.maxRounds = 5
	if err := w.RunUntilIdle(context.Background()); err == nil {
		t.Error("超過輪數上限應回傳錯誤")
	}
}

// HandlerFunc 讓函式成為 Handler。
type HandlerFunc func(ctx context.Context, t store.Task) error

func (f HandlerFunc) Handle(ctx context.Context, t store.Task) error { return f(ctx, t) }

func TestRunWakesOnNotificationAndOnDueTime(t *testing.T) {
	r := newRig(t)
	h := &recorder{st: r.st}
	w := New(r.q, h, r.clk, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()

	// 通知喚醒：新增任務後 Wake。
	r.addTask(t, "a", nil)
	r.q.Wake()
	waitFor(t, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.seen) == 1 })

	// 到期喚醒：任務在未來，推進 fake clock 後由計時器喚醒，不需要任何通知。
	later := r.clk.Now().Add(time.Hour)
	r.addTask(t, "b", &later)
	r.q.Wake() // 讓 worker 重新計算下一個到期時間
	waitFor(t, func() bool { return r.clk.PendingTimers() > 0 })
	r.clk.Advance(time.Hour)
	waitFor(t, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.seen) == 2 })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消後 Run 沒有結束")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待條件成立逾時")
}
