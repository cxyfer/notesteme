package app

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// registerReprobe 登記排程工作 source.reprobe（SRC-R18～R22）：在排程器內同步執行，平台的字幕軌清單
// 經過訂閱的 rate limiter（SUB-R10），不占用任務的資源類別（ADR-0021 第 6 點）。排定由 pipeline 在步驟完成時處理。
func (s *Server) registerReprobe() error {
	return s.scheduler.Register(scheduler.Kind{Name: store.ReprobeKind, Run: func(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.Result, error) {
		opts := pipeline.ReprobeOptions{JitterMax: s.scheduler.JitterMax()}
		if s.subscriptions != nil {
			opts.Take = s.subscriptions.TakePlatform
		}
		res, err := s.pipeline.Reprobe(ctx, job.Key, now, opts)
		if err != nil {
			return scheduler.Result{}, err
		}
		return scheduler.Result{Next: res.Next, Done: res.Done, Deferred: res.Deferred}, nil
	}})
}
