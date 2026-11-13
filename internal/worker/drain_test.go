package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// stoppable 模擬 pipeline.Handle：停在 gate，ctx 取消時什麼都不寫就回傳；gate 打開則正常寫回 done。
// lateWrite 為 true 時，ctx 取消後仍嘗試寫回（模擬已經做完、只差寫回的任務），結果記在 lateErr。
type stoppable struct {
	st        *store.Store
	started   chan store.Task
	gate      chan struct{}
	lateWrite bool

	mu      sync.Mutex
	lateErr error
}

func newStoppable(st *store.Store) *stoppable {
	return &stoppable{st: st, started: make(chan store.Task, 32), gate: make(chan struct{})}
}

func (h *stoppable) write(ctx context.Context, t store.Task) error {
	return h.st.Tx(ctx, func(r *store.Repo) error {
		if err := r.CompleteTask(ctx, t.ID, t.LeaseToken); err != nil {
			return err
		}
		return r.SetStageState(ctx, t.StageRunID, store.StageState{Status: store.StatusDone})
	})
}

func (h *stoppable) Handle(ctx context.Context, t store.Task) error {
	h.started <- t
	select {
	case <-h.gate:
		return h.write(ctx, t)
	case <-ctx.Done():
		if h.lateWrite {
			err := h.write(context.WithoutCancel(ctx), t)
			h.mu.Lock()
			h.lateErr = err
			h.mu.Unlock()
		}
		return nil
	}
}

func (h *stoppable) late() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lateErr
}

// startDrain 在背景呼叫 Drain，並等到它進入寬限期（fake clock 上出現計時器，代表已停止領取）。
func startDrain(t *testing.T, r *rig, w *Worker, grace time.Duration) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- w.Drain(context.Background(), grace) }()
	waitFor(t, func() bool { return r.clk.PendingTimers() > 0 })
	return done
}

func waitDrain(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Drain = %v", err)
		}
	case <-time.After(10 * time.Second): // 只是防止卡死的上限
		t.Fatal("Drain 沒有結束")
	}
}

func TestDrainLetsTasksFinishWithinGrace(t *testing.T) {
	r := newRig(t)
	tk := r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	if n := roundOf(t, w); n != 1 {
		t.Fatalf("RunRound = %d", n)
	}
	<-h.started

	done := startDrain(t, r, w, 30*time.Second)
	select {
	case err := <-done:
		t.Fatalf("寬限期內任務還在執行，Drain 不該結束：%v", err)
	default:
	}
	close(h.gate) // 寬限期內完成，時鐘沒有推進
	waitDrain(t, done)

	got, _ := r.st.Read().Task(context.Background(), tk.ID)
	if got.State != store.TaskDone {
		t.Errorf("寬限期內完成的任務應寫回為 done：%+v", got)
	}
	run, _ := r.st.Read().StageRun(context.Background(), tk.StageRunID)
	if run.Status != store.StatusDone {
		t.Errorf("步驟應為 done，實際 %s", run.Status)
	}
}

func TestDrainCancelsTasksAfterGraceAndReleasesLeases(t *testing.T) {
	r := newRig(t)
	tk := r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	if n := roundOf(t, w); n != 1 {
		t.Fatalf("RunRound = %d", n)
	}
	<-h.started

	done := startDrain(t, r, w, 30*time.Second)
	r.clk.Advance(29 * time.Second)
	select {
	case err := <-done:
		t.Fatalf("寬限期還沒到，Drain 不該結束：%v", err)
	default:
	}
	r.clk.Advance(time.Second) // 寬限期到期
	waitDrain(t, done)

	got, _ := r.st.Read().Task(context.Background(), tk.ID)
	if got.State != store.TaskPending || got.LeaseToken != "" || got.LeaseOwner != "" {
		t.Errorf("到期被取消的任務應放回 pending 並清除租約：%+v", got)
	}
	if got.Attempts != 0 {
		t.Errorf("放回的任務不應計入 attempts，實際 %d", got.Attempts)
	}
	run, _ := r.st.Read().StageRun(context.Background(), tk.StageRunID)
	if run.Status != store.StatusPending {
		t.Errorf("步驟應回到 pending，實際 %s", run.Status)
	}
}

func TestDrainWithZeroGraceCancelsImmediately(t *testing.T) {
	r := newRig(t)
	tk := r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	roundOf(t, w)
	<-h.started

	waitDrain(t, func() <-chan error {
		done := make(chan error, 1)
		go func() { done <- w.Drain(context.Background(), 0) }()
		return done
	}())
	if got, _ := r.st.Read().Task(context.Background(), tk.ID); got.State != store.TaskPending || got.Attempts != 0 {
		t.Errorf("寬限期 0 應立刻取消並放回：%+v", got)
	}
}

func TestDrainStopsClaiming(t *testing.T) {
	r := newRig(t)
	r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	roundOf(t, w)
	<-h.started

	done := startDrain(t, r, w, 30*time.Second)
	b := r.addTask(t, "b", nil)
	if n := roundOf(t, w); n != 0 {
		t.Errorf("Drain 期間不該領取新任務，領了 %d 個", n)
	}
	close(h.gate)
	waitDrain(t, done)

	if got, _ := r.st.Read().Task(context.Background(), b.ID); got.State != store.TaskPending {
		t.Errorf("新任務應維持 pending：%+v", got)
	}
}

// StopClaiming 之後不再領取新任務，執行中的任務不受影響，之後的 Drain 仍然完整運作。
func TestStopClaimingStopsNewTasksButKeepsRunningOnes(t *testing.T) {
	r := newRig(t)
	a := r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	roundOf(t, w)
	<-h.started

	w.StopClaiming()
	b := r.addTask(t, "b", nil)
	if n := roundOf(t, w); n != 0 {
		t.Errorf("StopClaiming 之後不該領取，領了 %d 個", n)
	}
	if got, _ := r.st.Read().Task(context.Background(), b.ID); got.State != store.TaskPending {
		t.Errorf("新任務應維持 pending：%+v", got)
	}
	if got, _ := r.st.Read().Task(context.Background(), a.ID); got.State != store.TaskLeased {
		t.Errorf("執行中的任務不該受影響：%+v", got)
	}
	w.StopClaiming() // 可重複呼叫

	done := startDrain(t, r, w, 30*time.Second)
	close(h.gate)
	waitDrain(t, done)
	if got, _ := r.st.Read().Task(context.Background(), a.ID); got.State != store.TaskDone {
		t.Errorf("寬限期內完成的任務應為 done：%+v", got)
	}
}

// Drain 本身的 ctx 取消時不再等任務，但租約仍然放回。
func TestDrainReleasesLeasesWhenCallerContextIsCancelled(t *testing.T) {
	r := newRig(t)
	tk := r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	roundOf(t, w)
	<-h.started

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Drain(ctx, 30*time.Second) }()
	waitFor(t, func() bool { return r.clk.PendingTimers() > 0 })
	cancel()
	waitDrain(t, done)
	if got, _ := r.st.Read().Task(context.Background(), tk.ID); got.State != store.TaskPending {
		t.Errorf("租約應放回 pending：%+v", got)
	}
}

// Cancel 結束指定的執行中任務並等它們結束；不認識的 ID 略過。
func TestCancelStopsRunningTasks(t *testing.T) {
	r := newRig(t)
	a := r.addTask(t, "a", nil)
	r.addTask(t, "b", nil)
	h := newStoppable(r.st)
	w := New(r.q, h, r.clk, quiet)
	if n := roundOf(t, w); n != 2 {
		t.Fatalf("RunRound = %d", n)
	}
	<-h.started
	<-h.started

	if err := w.Cancel(context.Background(), []string{a.ID, "not-running"}); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	_, stillActive := w.active[a.ID]
	remaining := len(w.active)
	w.mu.Unlock()
	if stillActive || remaining != 1 {
		t.Errorf("Cancel 回傳時被取消的任務應已結束：active=%d，仍在 active=%v", remaining, stillActive)
	}

	close(h.gate)
	if err := w.Wait(); err != nil {
		t.Fatal(err)
	}
}

// 領取進行中（Claim 的交易已 commit、任務還沒登記）時，Cancel 要等這一輪結束才查 active，
// 否則會找不到剛領取的任務，而它之後會照常執行。
func TestCancelWaitsForClaimRoundInProgress(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	w := New(r.q, newStoppable(r.st), r.clk, quiet)

	w.roundMu.Lock() // 模擬 RunRound 正在領取
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	at := &activeTask{cancel: cancel, done: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- w.Cancel(ctx, []string{"t1"}) }()

	select {
	case err := <-done:
		w.roundMu.Unlock()
		t.Fatalf("領取進行中 Cancel 不該先回傳（會漏掉剛領取的任務）：%v", err)
	case <-time.After(50 * time.Millisecond): // 確認 Cancel 真的在等
	}

	// 這一輪登記好任務並結束：Cancel 接著要看得到它。
	w.mu.Lock()
	w.active["t1"] = at
	w.mu.Unlock()
	w.roundMu.Unlock()

	select {
	case <-tctx.Done():
	case <-time.After(10 * time.Second): // 只是防止卡死的上限
		t.Fatal("Cancel 沒有取消剛登記的任務")
	}
	close(at.done)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("任務結束後 Cancel 沒有回傳")
	}
}

// 任務在資料庫中被取消之後才做完：寫回因租約失效而被拒絕，不會把任務蓋回 done（PIPE-R15）。
func TestLateCompletionAfterCancelDoesNotOverwriteState(t *testing.T) {
	r := newRig(t)
	tk := r.addTask(t, "a", nil)
	h := newStoppable(r.st)
	h.lateWrite = true
	w := New(r.q, h, r.clk, quiet)
	roundOf(t, w)
	<-h.started

	ctx := context.Background()
	var ids []string
	err := r.st.Tx(ctx, func(rp *store.Repo) error {
		var err error
		if err = rp.SetStageState(ctx, tk.StageRunID, store.StageState{Status: store.StatusSkipped, BlockReason: "cancelled"}); err != nil {
			return err
		}
		ids, err = rp.CancelItemTasks(ctx, itemOf(t, r, tk))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != tk.ID {
		t.Fatalf("取消的任務 = %v", ids)
	}
	if err := w.Cancel(ctx, ids); err != nil {
		t.Fatal(err)
	}

	if err := h.late(); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("取消後才做完的寫回應回傳 ErrLeaseLost，實際 %v", err)
	}
	got, _ := r.st.Read().Task(ctx, tk.ID)
	run, _ := r.st.Read().StageRun(ctx, tk.StageRunID)
	if got.State != store.TaskCancelled || run.Status != store.StatusSkipped {
		t.Errorf("晚到的結果不應覆蓋取消：任務 %s，步驟 %s", got.State, run.Status)
	}
}

func itemOf(t *testing.T, r *rig, tk store.Task) string {
	t.Helper()
	run, err := r.st.Read().StageRun(context.Background(), tk.StageRunID)
	if err != nil {
		t.Fatal(err)
	}
	return run.ItemID
}
