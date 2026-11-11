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
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// 預設值。
const (
	// DefaultMaxInFlight 是同時執行的任務上限。
	// TODO(PIPE-R8)：依資源類別（download、asr、llm…）分別設定並行上限。
	DefaultMaxInFlight = 4
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

	maxInFlight int
	maxRounds   int

	mu       sync.Mutex
	inFlight int
	errs     []error
	wg       sync.WaitGroup
}

// New 建立 Worker。
func New(q *queue.Queue, h Handler, clk clock.Clock, log *slog.Logger) *Worker {
	return &Worker{q: q, h: h, clk: clk, log: log, maxInFlight: DefaultMaxInFlight, maxRounds: MaxRounds}
}

// RunRound 領取一輪可以執行的任務，把它們的步驟標記為 running，各自開始執行後就回傳，不等待完成。
// 回傳領取的任務數。
func (w *Worker) RunRound(ctx context.Context) (int, error) {
	w.mu.Lock()
	free := w.maxInFlight - w.inFlight
	w.mu.Unlock()
	if free <= 0 {
		return 0, nil
	}
	tasks, err := w.q.Claim(ctx, free)
	if err != nil {
		return 0, fmt.Errorf("領取任務：%w", err)
	}
	for _, t := range tasks {
		w.mu.Lock()
		w.inFlight++
		w.mu.Unlock()
		w.wg.Add(1)
		go w.execute(ctx, t)
	}
	return len(tasks), nil
}

func (w *Worker) execute(ctx context.Context, t store.Task) {
	defer w.wg.Done()
	defer func() {
		w.mu.Lock()
		w.inFlight--
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
		n, err := w.RunRound(ctx)
		if err != nil {
			w.log.Error("領取任務失敗", "error", err)
		}
		if n > 0 {
			continue
		}
		w.sleep(ctx)
	}
}

// sleep 等到有事可做：收到通知、下一個任務到期，或 ctx 取消。
func (w *Worker) sleep(ctx context.Context) {
	var timeout <-chan time.Time
	if at, ok, err := w.q.NextDue(ctx); err != nil {
		w.log.Error("查詢下一個任務的時間失敗", "error", err)
	} else if ok {
		if d := at.Sub(w.clk.Now()); d > 0 {
			t := w.clk.NewTimer(d)
			defer t.Stop()
			timeout = t.C()
		}
	}
	select {
	case <-ctx.Done():
	case <-w.q.Wakeups():
	case <-timeout:
	}
}
