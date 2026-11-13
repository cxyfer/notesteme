// Package worker 領取佇列中的任務並交給 Handler 執行。
//
// 兩種模式（ADR-0015）：
//   - production：Run 在背景持續執行，由通知與下一個任務的到期時間喚醒，不忙碌輪詢（NFR-RES-2）；
//   - 手動：測試呼叫 RunRound、RunUntilIdle，server 不自己跑 worker，任務的進度完全由測試推動。
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// 預設值。
const (
	// MaxRounds 是 RunUntilIdle 的輪數上限，避免任務無限地互相產生而卡住測試。
	MaxRounds = 1000
)

// Handler 執行一個任務並把結果寫回（步驟狀態、產出、下游任務）。
// 回傳錯誤代表發生了不該發生的問題（例如資料庫失敗）；任務會維持 leased，租約到期後重新領取。
type Handler interface {
	Handle(ctx context.Context, t store.Task) error
}

// Worker 領取並執行任務。
type Worker struct {
	q   *queue.Queue
	h   Handler
	log *slog.Logger
	clk clock.Clock

	maxRounds int

	// roundMu 讓一次領取（計算空位、領取、登記執行中）不被另一個 RunRound、Cancel、StopClaiming 打斷：
	// Cancel 與 StopClaiming 取得它時，一定看不到「已領取但還沒登記」的任務。
	roundMu sync.Mutex
	// draining 為 true 時不再領取（PIPE-R12）；由 roundMu 保護。
	draining bool
	mu       sync.Mutex
	// running 是各資源類別在記憶體中執行中的任務數；領取的空位是類別的上限減去它（PIPE-R8）。
	running map[string]int
	// active 是執行中的任務（以任務 ID 為 key），可以取消，也可以等它結束（PIPE-R12、PIPE-R15）。
	active map[string]*activeTask
	errs   []error
	wg     sync.WaitGroup
}

// activeTask 是一個執行中的任務：cancel 取消它的 ctx（子程序隨之整組結束），done 在它結束後關閉。
type activeTask struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// New 建立 Worker。
func New(q *queue.Queue, h Handler, clk clock.Clock, log *slog.Logger) *Worker {
	return &Worker{q: q, h: h, clk: clk, log: log, running: map[string]int{}, active: map[string]*activeTask{}, maxRounds: MaxRounds}
}

// RunRound 領取一輪可以執行的任務，把它們的步驟標記為 running，各自開始執行後就回傳，不等待完成。
// 每個資源類別依自己的上限與暫停設定領取（PIPE-R8、PIPE-R10）；設定改變後的下一輪就生效。
// 回傳領取的任務數。
func (w *Worker) RunRound(ctx context.Context) (int, error) {
	w.roundMu.Lock()
	defer w.roundMu.Unlock()
	if w.draining {
		return 0, nil
	}
	w.mu.Lock()
	running := maps.Clone(w.running)
	w.mu.Unlock()
	tasks, err := w.q.ClaimFree(ctx, running)
	if err != nil {
		return 0, fmt.Errorf("領取任務：%w", err)
	}
	for _, t := range tasks {
		// 先登記再啟動；領取到登記的整段期間都持有 roundMu，Cancel 與 Drain 取得 roundMu 之後就一定找得到這個任務。
		tctx, cancel := context.WithCancel(ctx)
		at := &activeTask{cancel: cancel, done: make(chan struct{})}
		w.mu.Lock()
		w.running[t.ResourceClass]++
		w.active[t.ID] = at
		w.mu.Unlock()
		w.wg.Add(1)
		go w.execute(tctx, t, at)
	}
	return len(tasks), nil
}

func (w *Worker) execute(ctx context.Context, t store.Task, at *activeTask) {
	defer w.wg.Done()
	defer func() {
		at.cancel()
		w.mu.Lock()
		delete(w.active, t.ID)
		w.mu.Unlock()
		close(at.done)
	}()
	defer func() {
		w.mu.Lock()
		w.running[t.ResourceClass]--
		w.mu.Unlock()
		// 完成一個任務就有空位了；下游任務也可能已經建立。
		w.q.Wake()
	}()
	defer func() {
		if r := recover(); r != nil {
			w.fail(fmt.Errorf("任務 %s（%s）發生 panic：%v", t.ID, t.Kind, r))
		}
	}()
	if err := w.h.Handle(ctx, t); err != nil && ctx.Err() == nil {
		w.fail(fmt.Errorf("任務 %s（%s）：%w", t.ID, t.Kind, err))
	}
}

func (w *Worker) fail(err error) {
	w.log.Error("任務執行失敗", "error", err)
	w.mu.Lock()
	w.errs = append(w.errs, err)
	w.mu.Unlock()
}

// Wait 等到執行中的任務都結束，回傳（並清除）這段期間 Handler 回報的錯誤。
func (w *Worker) Wait() error {
	w.wg.Wait()
	w.mu.Lock()
	defer w.mu.Unlock()
	err := errors.Join(w.errs...)
	w.errs = nil
	return err
}

// Cancel 取消指定的任務：正在這個 worker 執行的，取消它的 ctx（toolexec 會把整組子程序結束），
// 並等到它們都結束才回傳；不在執行中的任務 ID 略過。ctx 取消時不再等待。
// 呼叫端應該先在資料庫中把任務改為 cancelled（pipeline.CancelItem），這樣即使任務在 Cancel 之後才做完，
// 寫回也會因為租約失效而被丟棄（PIPE-R15）。
func (w *Worker) Cancel(ctx context.Context, taskIDs []string) error {
	// RunRound 在領取（交易 commit）與登記到 active 的整段期間都持有 roundMu。先取得它，
	// 才不會在這之間查 active 而漏掉剛領取的任務。已領取的任務之後即使開始執行，
	// 租約也已經因為資料庫中的 cancelled 而失效，寫回會被丟棄。
	w.roundMu.Lock()
	var waits []*activeTask
	w.mu.Lock()
	for _, id := range taskIDs {
		if at, ok := w.active[id]; ok {
			at.cancel()
			waits = append(waits, at)
		}
	}
	w.mu.Unlock()
	w.roundMu.Unlock() // 只在查詢與取消期間持有；等待任務結束時不擋住領取
	for _, at := range waits {
		select {
		case <-at.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// ReleaseTimeout 是 Drain 放回租約的上限；放回租約用不受呼叫端 ctx 取消影響的 ctx，避免寬限期用掉了期限。
// 呼叫端替 Drain 設定期限時，應該是寬限期加上它。
const ReleaseTimeout = 10 * time.Second

// StopClaiming 讓 worker 不再領取新任務（PIPE-R12），執行中的任務不受影響。可重複呼叫。
// 正常關閉一開始就要呼叫，不能等到 HTTP 關閉之後：關閉期間 worker 不該再開始新任務。
func (w *Worker) StopClaiming() {
	// 取得 roundMu 代表沒有一輪領取正在進行；之後的 RunRound 都會看到 draining。
	w.roundMu.Lock()
	w.draining = true
	w.roundMu.Unlock()
}

// Drain 正常關閉（PIPE-R12）：停止領取，最多等 grace（用注入的 clock 計時）讓執行中的任務完成；
// 到期後取消剩下的任務並等它們結束（子程序整組結束），最後把還持有的租約放回 pending。
// 被取消的任務不計入重試次數。ctx 取消時視同寬限期到期，也不再等待任務結束，但租約仍會放回。
// 租約放回之後，沒結束的任務即使晚點寫回也會因為租約失效而被丟棄。
func (w *Worker) Drain(ctx context.Context, grace time.Duration) error {
	w.StopClaiming()

	idle := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(idle)
	}()

	select {
	case <-idle:
	default:
		if grace > 0 {
			timer := w.clk.NewTimer(grace)
			select {
			case <-idle:
			case <-timer.C():
			case <-ctx.Done():
			}
			timer.Stop()
		}
	}

	select {
	case <-idle:
	default:
		w.log.Warn("寬限期結束，取消還在執行的任務")
		w.mu.Lock()
		for _, at := range w.active {
			at.cancel()
		}
		w.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
		}
	}

	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ReleaseTimeout)
	defer cancel()
	n, err := w.q.ReleaseOwned(rctx)
	if err != nil {
		return fmt.Errorf("釋放租約：%w", err)
	}
	if n > 0 {
		w.log.Info("已放回未完成任務的租約", "count", n)
	}
	return nil
}

// RunUntilIdle 持續領取並執行任務，直到沒有可以執行的任務為止（上限 MaxRounds 輪）。
// not_before 還沒到的任務不算可以執行；測試推進 clock 後再呼叫一次。
func (w *Worker) RunUntilIdle(ctx context.Context) error {
	for range w.maxRounds {
		n, err := w.RunRound(ctx)
		if err != nil {
			return errors.Join(err, w.Wait())
		}
		if err := w.Wait(); err != nil {
			return err
		}
		if n == 0 {
			return ctx.Err()
		}
	}
	return fmt.Errorf("worker 在 %d 輪內沒有閒置，任務可能不斷產生新任務", w.maxRounds)
}

// Run 是 production 的主迴圈：領取、執行；沒有可執行的任務時睡到收到通知或下一個任務到期。
// ctx 取消後停止領取新任務，並等待執行中的任務結束才回傳。
func (w *Worker) Run(ctx context.Context) {
	defer func() { _ = w.Wait() }()
	for ctx.Err() == nil {
		// 睡眠只看「這一輪開始之後才到期」的任務：領不到的到期任務（類別暫停或已滿）
		// 不能遮蔽其他類別之後才到期的重試；它們由通知（恢復、調高上限、任務結束）喚醒。
		since := w.clk.Now()
		n, err := w.RunRound(ctx)
		if err != nil {
			w.log.Error("領取任務失敗", "error", err)
		}
		if n > 0 {
			continue
		}
		w.sleep(ctx, since)
	}
}

// sleep 等到有事可做：收到通知、下一個任務到期，或 ctx 取消。after 是這一輪領取開始前的時間。
func (w *Worker) sleep(ctx context.Context, after time.Time) {
	var timeout <-chan time.Time
	if at, ok, err := w.q.NextDue(ctx, after); err != nil {
		w.log.Error("查詢下一個任務的時間失敗", "error", err)
	} else if ok {
		d := at.Sub(w.clk.Now())
		if d <= 0 {
			// 領取期間剛好到期：不睡，馬上再領一輪。
			return
		}
		t := w.clk.NewTimer(d)
		defer t.Stop()
		timeout = t.C()
	}
	select {
	case <-ctx.Done():
	case <-w.q.Wakeups():
	case <-timeout:
	}
}
