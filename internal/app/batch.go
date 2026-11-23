package app

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// registerBatchPoll 登記排程工作 batch.poll（BATCH-R4、R5、R9，ADR-0021 第 1 點）：key 是 batches.id，
// 每 5 分鐘查詢一次，結束時刪除這個工作。重新啟動後未結束的 batch 照樣由它輪詢，不重新送出（BATCH-R10）。
func (s *Server) registerBatchPoll() error {
	return s.scheduler.Register(scheduler.Kind{Name: store.BatchPollKind, Run: func(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.Result, error) {
		next, done, err := s.pipeline.PollBatch(ctx, job.Key, now)
		if err != nil {
			return scheduler.Result{}, err
		}
		if done {
			return scheduler.Result{Done: true}, nil
		}
		return scheduler.Result{Next: next}, nil
	}})
}
