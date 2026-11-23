package app

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// registerBudgetMonth 登記排程工作 budget.month（COST-R9），並在還沒有排定時排在預算時區的下一個月初。
// 執行時讓 resume_on: budget 的 blocked 步驟重新建立任務，下一次 worker 執行時重新檢查預算。
func (s *Server) registerBudgetMonth(ctx context.Context) error {
	err := s.scheduler.Register(scheduler.Kind{Name: cost.MonthKind, Run: func(ctx context.Context, _ scheduler.Job, now time.Time) (scheduler.Result, error) {
		if _, err := s.pipeline.ResumeBlocked(ctx, cost.ResumeToken); err != nil {
			return scheduler.Result{}, err
		}
		next, err := s.cost.NextMonthRun(ctx, s.store.Read(), now)
		if err != nil {
			return scheduler.Result{}, err
		}
		return scheduler.Result{Next: next}, nil
	}})
	if err != nil {
		return err
	}
	return s.store.Tx(ctx, func(r *store.Repo) error { return s.cost.ScheduleMonth(ctx, r) })
}
