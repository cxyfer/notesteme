// Package scheduler 是 M4 的排程器（ADR-0021 第 1 點）：worker 的一個 Periodic，每一輪開頭以注入的 clock
// 執行 scheduled_jobs 中到期的工作，並把最早的 next_run_at 交給 worker 的 nextWake。
//
// 註冊制：每個切片在 worker 開始之前以 Register 登記自己的 kind（例如 subscription.check、notify.deliver），
// 再用 store 的 ScheduleJob 排定工作。註冊表是空的時候 Run 與 NextAt 不查資料庫，worker 的行為與時間完全不變（M4-S0）。
//
// 規則：
//   - 一輪內每個到期的工作最多執行一次；同時到期的依 next_run_at、再依建立順序（SUB-R9）。
//     例外：這一輪執行的工作把其他工作提前到這一輪開頭以前時（例如寫入 outbox 後的 notify.deliver），同一輪再執行一趟。
//   - 成功：下一次是 Result.Next，沒有指定時是「這次執行的時間＋間隔＋jitter」，所以停機很久之後只補跑一次。
//   - 失敗：依連續失敗次數退避（1 分鐘、5 分鐘、30 分鐘，之後固定 6 小時），不會停在過去而每一輪重試。
//   - Result.Deferred：不是失敗也不是成功（例如被 rate limit 擋下，SUB-R10），只改下一次的時間。
//   - 每一輪的總執行時間上限 30 秒（以注入的 clock 判斷），沒執行完的留到下一輪。
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// RoundBudget 是每一輪排程器的總執行時間上限（ADR-0021 第 1 點）。
const RoundBudget = 30 * time.Second

// maxDuePerRound 是一輪最多取出的到期工作數；其餘的留到下一輪（下一輪會立刻開始，因為它們仍然到期）。
const maxDuePerRound = 100

// maxErrorLen 是 last_error 保留的長度（rune）。
const maxErrorLen = 500

// backoff 是連續失敗第 n 次之後的重試間隔（ADR-0021 第 1 點）；超過的一律用最後一個。
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 6 * time.Hour}

// Backoff 回傳連續失敗 failures 次（至少 1）之後的重試間隔。
func Backoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	return backoff[min(failures, len(backoff))-1]
}

// Jitter 是確定性的隨機延遲（ADR-0021 第 1 點）：sha256(kind | key | 時段序號) 換算成
// [0, min(間隔 × 10%, max))，以毫秒為單位（scheduled_jobs 的時間精度）。max 或間隔不到 1 毫秒時沒有延遲。
func Jitter(kind, key string, slot int64, interval, max time.Duration) time.Duration {
	limit := min(interval/10, max).Milliseconds()
	if limit <= 0 {
		return 0
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d", kind, key, slot)
	n := binary.BigEndian.Uint64(h.Sum(nil)[:8])
	return time.Duration(n%uint64(limit)) * time.Millisecond
}

// Job 是交給 Handler 的一個到期工作。
type Job = store.ScheduledJob

// Result 是 Handler 執行成功時的結果。
type Result struct {
	// Next 是下一次執行的時間；零值代表「這次執行的時間＋間隔＋jitter」（Kind.Interval 必須大於 0）。
	// 對齊當地時間的工作（digest、backup.daily、budget.month）自己算好傳回來，不加 jitter。
	Next time.Time
	// Done 為 true 時結束這個工作（刪除那一列），例如 source.reprobe 超過 until。
	Done bool
	// Deferred 為 true 時這次不算成功也不算失敗：failures、last_error、last_ok_at 不變，只改成 Next（必須指定）。
	Deferred bool
}

// RetryAfter 是「失敗，但下一次不早於 NotBefore」的錯誤（SUB-R10：平台回 429 時，下一次取退避時間與
// rate limit 恢復時間中較晚的一個）。仍然算一次失敗：failures 加一、寫入 last_error。
type RetryAfter struct {
	Err       error
	NotBefore time.Time
}

func (e *RetryAfter) Error() string { return e.Err.Error() }
func (e *RetryAfter) Unwrap() error { return e.Err }

// Handler 執行一個到期的工作；now 是這一輪開頭的時間，job.NextRunAt 是排定的時間。
// 回傳錯誤代表失敗，依退避重試。
type Handler func(ctx context.Context, job Job, now time.Time) (Result, error)

// Kind 是一種排程工作。
type Kind struct {
	// Name 是 scheduled_jobs.kind，例如 subscription.check。
	Name string
	// Interval 是成功之後的預設間隔；0 代表 Handler 一定會回傳 Result.Next 或 Done。
	Interval time.Duration
	// Jitter 為 true 時預設的下一次加上確定性的隨機延遲（SUB-06）。
	Jitter bool
	Run    Handler
}

// Scheduler 實作 worker.Periodic。
type Scheduler struct {
	store     *store.Store
	clock     clock.Clock
	log       *slog.Logger
	jitterMax time.Duration

	mu     sync.RWMutex
	kinds  map[string]Kind
	order  []string
	frozen bool
}

// New 建立排程器；jitterMax 是設定檔的 scheduler.jitter_max（0 代表沒有隨機延遲）。
func New(st *store.Store, clk clock.Clock, log *slog.Logger, jitterMax time.Duration) *Scheduler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Scheduler{store: st, clock: clk, log: log, jitterMax: jitterMax, kinds: map[string]Kind{}}
}

// ErrFrozen 是 worker 開始執行之後才 Register 的錯誤。
var ErrFrozen = errors.New("scheduler: 排程器已經開始執行，不能再登記新的 kind")

// Register 登記一種排程工作。必須在第一次 Run 之前呼叫；同名的 kind 不能登記兩次。
func (s *Scheduler) Register(k Kind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.frozen:
		return ErrFrozen
	case k.Name == "" || k.Run == nil:
		return fmt.Errorf("scheduler: kind 必須有名稱與 Handler")
	case k.Interval < 0:
		return fmt.Errorf("scheduler: %s 的間隔不能是負的", k.Name)
	}
	if _, dup := s.kinds[k.Name]; dup {
		return fmt.Errorf("scheduler: %s 已經登記過", k.Name)
	}
	s.kinds[k.Name] = k
	s.order = append(s.order, k.Name)
	return nil
}

// Kinds 回傳已登記的 kind（依登記順序）。
func (s *Scheduler) Kinds() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.order)
}

func (s *Scheduler) snapshot() (map[string]Kind, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frozen = true
	return s.kinds, s.order
}

// Run 執行所有到期的工作（worker.Periodic）。註冊表是空的時候什麼都不做，也不查資料庫。
// 一個工作失敗不影響其他工作；只有資料庫錯誤與 ctx 取消會回傳。
func (s *Scheduler) Run(ctx context.Context) error {
	kinds, order := s.snapshot()
	if len(order) == 0 {
		return nil
	}
	start := s.clock.Now()
	// 第二趟：這一輪執行的工作把其他工作提前到這一輪開頭以前（例如彙整寫入 outbox 後的 notify.deliver）時，
	// 在同一輪再執行一次，不必等下一輪。執行過的工作的下一次已排在開頭之後，第二趟只會取到被提前的工作；
	// 最多兩趟，不會無限循環。
	for pass := 0; pass < 2; pass++ {
		due, err := s.store.Read().DueScheduledJobs(ctx, order, start, maxDuePerRound)
		if err != nil {
			return err
		}
		if pass > 0 && len(due) == 0 {
			return nil
		}
		for _, job := range due {
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.clock.Now().Sub(start) >= RoundBudget {
				s.log.WarnContext(ctx, "排程器這一輪的執行時間用完，其餘的工作留到下一輪", "remaining", len(due))
				return nil
			}
			if err := s.runJob(ctx, kinds[job.Kind], job, start); err != nil {
				return err
			}
		}
	}
	return nil
}

// runJob 執行一個工作並寫回結果；回傳的錯誤只有資料庫錯誤（Handler 的錯誤記在 last_error）。
func (s *Scheduler) runJob(ctx context.Context, k Kind, job Job, now time.Time) error {
	res, runErr := s.call(ctx, k, job, now)
	w := s.store.Write()
	if runErr == nil && res.Done {
		_, err := w.DeleteScheduledJob(ctx, job.Kind, job.Key)
		return err
	}
	rec := store.ScheduledJobRun{LastRunAt: now, LastOKAt: job.LastOKAt, LastError: job.LastError, Failures: job.Failures}
	switch {
	case runErr != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rec.Failures++
		rec.LastError = truncate(runErr.Error())
		rec.NextRunAt = now.Add(Backoff(rec.Failures))
		if ra := (*RetryAfter)(nil); errors.As(runErr, &ra) && ra.NotBefore.After(rec.NextRunAt) {
			rec.NextRunAt = ra.NotBefore
		}
		s.log.WarnContext(ctx, "排程工作失敗，稍後重試", "kind", job.Kind, "key", job.Key, "failures", rec.Failures, "error", rec.LastError)
	case res.Deferred:
		if res.Next.IsZero() {
			return fmt.Errorf("scheduler: %s 延後時必須指定下一次的時間", job.Kind)
		}
		rec.NextRunAt = res.Next
	default:
		ok := now
		rec.LastOKAt, rec.LastError, rec.Failures = &ok, "", 0
		rec.NextRunAt = res.Next
		if rec.NextRunAt.IsZero() {
			rec.NextRunAt = s.defaultNext(k, job.Key, now)
		}
	}
	if err := w.RecordScheduledJobRun(ctx, job.Kind, job.Key, rec); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// call 執行 Handler；panic 視為失敗，不讓整個 worker 停下。
func (s *Scheduler) call(ctx context.Context, k Kind, job Job, now time.Time) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("排程工作 panic：%v", p)
		}
	}()
	res, err = k.Run(ctx, job, now)
	if err == nil && !res.Done && !res.Deferred && res.Next.IsZero() && k.Interval <= 0 {
		err = fmt.Errorf("%s 沒有間隔，Handler 必須回傳下一次的時間", k.Name)
	}
	return res, err
}

// defaultNext 是成功之後的預設下一次：這次執行的時間＋間隔，加上 Kind.Jitter 的確定性延遲。
func (s *Scheduler) defaultNext(k Kind, key string, now time.Time) time.Time {
	if !k.Jitter {
		return now.Add(k.Interval)
	}
	return NextWithJitter(k.Name, key, now, k.Interval, s.jitterMax)
}

// NextWithJitter 是「now＋interval＋確定性的隨機延遲」：Kind.Interval 不固定的工作（例如每個訂閱各自的檢查頻率，
// SUB-R9）自己算下一次時使用，結果與 Kind.Jitter 的預設相同。
func NextWithJitter(kind, key string, now time.Time, interval, jitterMax time.Duration) time.Time {
	base := now.Add(interval)
	slot := base.UnixMilli() / max(interval.Milliseconds(), 1)
	return base.Add(Jitter(kind, key, slot, interval, jitterMax))
}

// JitterMax 回傳設定檔的 scheduler.jitter_max。
func (s *Scheduler) JitterMax() time.Duration { return s.jitterMax }

// NextAt 回傳已登記的 kind 中最早的 next_run_at（worker.Periodic）；註冊表是空的或沒有工作時 ok 為 false。
func (s *Scheduler) NextAt(ctx context.Context) (time.Time, bool, error) {
	_, order := s.snapshot()
	if len(order) == 0 {
		return time.Time{}, false, nil
	}
	return s.store.Read().NextScheduledJobAt(ctx, order)
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= maxErrorLen {
		return s
	}
	r := []rune(s)
	return string(r[:maxErrorLen]) + "…"
}
