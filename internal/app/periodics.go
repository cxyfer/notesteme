package app

import (
	"context"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/worker"
)

// periodics 把多個定期工作合成一個 worker.Periodic：手動模式（測試）下，
// housekeeping 與 inbox watcher 都在 RunUntilIdle 的每一輪開頭執行。
type periodics []worker.Periodic

// Run 依序執行每一項；一項失敗不影響其他項，所有錯誤一起回傳。
func (ps periodics) Run(ctx context.Context) error {
	var errs []error
	for _, p := range ps {
		if err := p.Run(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// NextAt 回傳各項中最早的下一次執行時間；都沒有排定時 ok 為 false。
func (ps periodics) NextAt(ctx context.Context) (time.Time, bool, error) {
	var (
		earliest time.Time
		found    bool
		errs     []error
	)
	for _, p := range ps {
		at, ok, err := p.NextAt(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok && (!found || at.Before(earliest)) {
			earliest, found = at, true
		}
	}
	return earliest, found, errors.Join(errs...)
}
