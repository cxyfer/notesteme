package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePeriodic 記錄 Run 的次數；next 是 NextAt 回報的時間（零值代表沒有排定）。
type fakePeriodic struct {
	runs    atomic.Int32
	mu      sync.Mutex
	next    time.Time
	runErr  error
	nextErr error
}

func (p *fakePeriodic) Run(context.Context) error {
	p.runs.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runErr
}

func (p *fakePeriodic) NextAt(context.Context) (time.Time, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.next, !p.next.IsZero(), p.nextErr
}

func (p *fakePeriodic) setNext(t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = t
}

// 定期工作在每一輪領取之前執行，即使沒有任何任務。
func TestPeriodicRunsAtStartOfEveryRound(t *testing.T) {
	r := newRig(t)
	p := &fakePeriodic{}
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	w.SetPeriodic(p)

	if _, err := w.RunRound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.runs.Load(); got != 1 {
		t.Fatalf("沒有任務時第一輪 Run 了 %d 次，預期 1", got)
	}
	r.addTask(t, "a", nil)
	if err := w.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	// RunUntilIdle：領到任務的一輪加上確認閒置的一輪，共 2 輪。
	if got := p.runs.Load(); got != 3 {
		t.Fatalf("累計 Run 了 %d 次，預期 3", got)
	}
}

// 定期工作失敗只記 log，任務照常領取。
func TestPeriodicFailureDoesNotBlockTasks(t *testing.T) {
	r := newRig(t)
	h := &recorder{st: r.st}
	p := &fakePeriodic{runErr: errors.New("boom")}
	w := New(r.q, h, r.clk, quiet)
	w.SetPeriodic(p)
	r.addTask(t, "a", nil)
	if err := w.RunUntilIdle(context.Background()); err != nil {
		t.Fatalf("RunUntilIdle = %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.seen) != 1 {
		t.Fatalf("處理了 %d 個任務，預期 1", len(h.seen))
	}
}

// 停止領取之後（正常關閉）不再執行定期工作。
func TestPeriodicSkippedWhileDraining(t *testing.T) {
	r := newRig(t)
	p := &fakePeriodic{}
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	w.SetPeriodic(p)
	w.StopClaiming()
	if _, err := w.RunRound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.runs.Load(); got != 0 {
		t.Fatalf("停止領取後仍 Run 了 %d 次", got)
	}
}

// 閒置的 Run 要在定期工作到期時醒來，不需要任何通知（沒有任務時也一樣）。
func TestRunWakesWhenPeriodicIsDue(t *testing.T) {
	r := newRig(t)
	p := &fakePeriodic{}
	p.setNext(r.clk.Now().Add(24 * time.Hour))
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	w.SetPeriodic(p)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	waitFor(t, func() bool { return p.runs.Load() == 1 && r.clk.PendingTimers() > 0 })
	r.clk.Advance(23 * time.Hour)
	time.Sleep(20 * time.Millisecond) // 還沒到期：不該多跑一輪
	if got := p.runs.Load(); got != 1 {
		t.Fatalf("到期前 Run 了 %d 次，預期 1", got)
	}
	r.clk.Advance(time.Hour)
	// fakePeriodic 不會重新排定：到期之後 worker 會再多跑一輪（見 nextWake），所以是「至少」2 次；
	// 用 == 2 只在輪詢剛好看到中間值時才成立，是不穩定的。
	waitFor(t, func() bool { return p.runs.Load() >= 2 })
}

// 定期工作的時間早於下一個任務時，以較早者為準；已經到期的時間要立刻再跑一輪，但同一個到期時間只觸發一次。
func TestNextWakePicksEarliestAndFiresOncePerDueTime(t *testing.T) {
	r := newRig(t)
	p := &fakePeriodic{}
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	w.SetPeriodic(p)
	ctx := context.Background()
	now := r.clk.Now()

	if _, ok := w.nextWake(ctx, now); ok {
		t.Fatal("沒有任務也沒有排定的定期工作時不該有喚醒時間")
	}
	p.setNext(now.Add(-time.Hour))
	if d, ok := w.nextWake(ctx, now); !ok || d > 0 {
		t.Fatalf("已經到期的定期工作要立刻再跑一輪：nextWake = %v, %v", d, ok)
	}
	// 同一個到期時間已經觸發過：再問一次不能又立刻醒來（跑了還是失敗、沒有重新排定時不會忙碌重試）。
	if _, ok := w.nextWake(ctx, now); ok {
		t.Fatal("同一個到期時間只能觸發一次")
	}
	// 重新排定成另一個過去的時間，是新的到期時間，會再觸發一次。
	p.setNext(now.Add(-time.Minute))
	if d, ok := w.nextWake(ctx, now); !ok || d > 0 {
		t.Fatalf("新的到期時間要再觸發：nextWake = %v, %v", d, ok)
	}
	p.setNext(now.Add(2 * time.Hour))
	if d, ok := w.nextWake(ctx, now); !ok || d != 2*time.Hour {
		t.Fatalf("nextWake = %v, %v；預期 2h", d, ok)
	}
	later := now.Add(time.Hour)
	r.addTask(t, "a", &later)
	if d, ok := w.nextWake(ctx, now); !ok || d != time.Hour {
		t.Fatalf("任務較早時 nextWake = %v, %v；預期 1h", d, ok)
	}
	p.setNext(now.Add(30 * time.Minute))
	if d, ok := w.nextWake(ctx, now); !ok || d != 30*time.Minute {
		t.Fatalf("定期工作較早時 nextWake = %v, %v；預期 30m", d, ok)
	}
	// 查詢失敗只記 log，不影響任務的喚醒時間。
	p.nextErr = errors.New("boom")
	if d, ok := w.nextWake(ctx, now); !ok || d != time.Hour {
		t.Fatalf("NextAt 失敗時 nextWake = %v, %v；預期只看任務 1h", d, ok)
	}
}

// 定期工作每輪都跑了、時間卻一直停在過去（例如失敗又沒有重新排定）：Run 只多跑一輪就睡，不會忙碌迴圈。
func TestRunDoesNotBusyLoopOnAnOverduePeriodic(t *testing.T) {
	r := newRig(t)
	p := &fakePeriodic{}
	p.setNext(r.clk.Now().Add(-time.Hour))
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	w.SetPeriodic(p)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()

	// 第一輪開頭跑一次，到期的時間讓它立刻再跑一輪，共 2 次。
	waitFor(t, func() bool { return p.runs.Load() >= 2 })
	time.Sleep(50 * time.Millisecond)
	if got := p.runs.Load(); got != 2 {
		t.Fatalf("同一個到期時間 Run 了 %d 次，預期正好 2 次（沒有忙碌迴圈）", got)
	}

	// 重新排定成未來：到期時照常醒來。
	p.setNext(r.clk.Now().Add(time.Hour))
	r.q.Wake()
	waitFor(t, func() bool { return p.runs.Load() == 3 && r.clk.PendingTimers() > 0 })
	r.clk.Advance(time.Hour)
	waitFor(t, func() bool { return p.runs.Load() >= 4 }) // 同上：到期後會多跑一輪，不能用 == 4
}

// cancelingPeriodic 在 Run 中呼叫 Worker.Cancel，就像永久刪除到期影音的清理那樣。
type cancelingPeriodic struct {
	fakePeriodic
	w *Worker
}

func (p *cancelingPeriodic) Run(ctx context.Context) error {
	p.runs.Add(1)
	return p.w.Cancel(ctx, []string{"no-such-task"})
}

// 定期工作可以呼叫 Cancel：它不能在持有 roundMu 時執行，否則 Cancel 等不到鎖而死結。
func TestPeriodicMayCallCancel(t *testing.T) {
	r := newRig(t)
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	p := &cancelingPeriodic{w: w}
	w.SetPeriodic(p)
	r.addTask(t, "a", nil)

	done := make(chan error, 1)
	go func() { done <- w.RunUntilIdle(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunUntilIdle 逾時：定期工作呼叫 Cancel 死結")
	}
	if p.runs.Load() == 0 {
		t.Fatal("定期工作沒有執行")
	}
}

// blockingPeriodic 的 Run 在 entered 關閉後停住，直到 release 關閉。
type blockingPeriodic struct {
	fakePeriodic
	entered, release chan struct{}
}

func (p *blockingPeriodic) Run(context.Context) error {
	if p.runs.Add(1) == 1 {
		close(p.entered)
		<-p.release
	}
	return nil
}

// 定期工作正在執行時停止領取：StopClaiming 要等它結束才回傳，之後的輪次不再執行定期工作。
func TestStopClaimingWaitsForRunningPeriodic(t *testing.T) {
	r := newRig(t)
	p := &blockingPeriodic{entered: make(chan struct{}), release: make(chan struct{})}
	w := New(r.q, &recorder{st: r.st}, r.clk, quiet)
	w.SetPeriodic(p)

	go func() { _, _ = w.RunRound(context.Background()) }()
	<-p.entered
	stopped := make(chan struct{})
	go func() { w.StopClaiming(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("定期工作還在執行，StopClaiming 不該回傳")
	case <-time.After(50 * time.Millisecond):
	}
	close(p.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("定期工作結束後 StopClaiming 應該回傳")
	}
	if _, err := w.RunRound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := p.runs.Load(); got != 1 {
		t.Errorf("停止領取後定期工作又執行了：共 %d 次", got)
	}
}
