package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/store"
)

// 事件清理（OPS-R10）：events 只保留最近 90 天，每天清理一次。
const (
	// eventRetention 是事件的保留期限。
	eventRetention = 90 * 24 * time.Hour
	// eventCleanupInterval 是兩次清理之間的間隔。
	eventCleanupInterval = 24 * time.Hour
	// eventCleanupRetry 是清理失敗之後重試的間隔：不等到明天，也不在每一輪都重試。
	eventCleanupRetry = 5 * time.Minute
	// settingEventsCleanupNextAt 是 settings 中下一次清理的時間（RFC 3339，UTC）。
	settingEventsCleanupNextAt = "events_cleanup_next_at"
)

// 垃圾桶清理（DEL-R4）：每天一次永久刪除到期的影音，失敗時 5 分鐘後重試。
const (
	trashCleanupInterval = 24 * time.Hour
	trashCleanupRetry    = 5 * time.Minute
	// settingTrashCleanupNextAt 是 settings 中下一次垃圾桶清理的時間（RFC 3339，UTC）。
	settingTrashCleanupNextAt = "trash_cleanup_next_at"
)

// trashRetention 是垃圾桶的保留期限，來自設定檔 library.trash_retention_days（DEL-R4）。
func trashRetention(cfg *config.Config) time.Duration {
	return time.Duration(cfg.TrashRetentionDays) * 24 * time.Hour
}

// trashPurger 永久刪除到期的垃圾桶影音（*pipeline.Pipeline 實作它）。
type trashPurger interface {
	PurgeExpired(ctx context.Context, now time.Time, retention time.Duration) (int, error)
}

// hkJob 是 housekeeping 的一項工作。每項工作有自己的 settings 鍵，存下一次執行的時間，
// 所以各項工作的排程、失敗與重試互不影響。
type hkJob struct {
	// name 是給人看的名稱，出現在錯誤訊息。
	name string
	// setting 是 settings 中下一次執行時間的鍵（RFC 3339，UTC）。
	setting  string
	interval time.Duration
	// retry 是失敗之後重試的間隔。
	retry time.Duration
	// run 執行工作；now 是這一輪開頭取得的時間。
	run func(ctx context.Context, now time.Time) error
}

// housekeeping 是 worker 的定期工作（worker.Periodic）：清理過期的事件（OPS-R10），
// 以及永久刪除到期的垃圾桶影音（DEL-R4，見 addTrashCleanup）。
// 下一次執行的時間存在 settings，重啟後不會因為記憶體狀態消失而重複執行或漏掉。
// 所有時間都來自注入的 clock。
type housekeeping struct {
	store *store.Store
	clock clock.Clock
	log   *slog.Logger
	// deleteBefore 刪除 cutoff 之前的事件；預設用 store，測試可以替換成會失敗的實作。
	deleteBefore func(ctx context.Context, cutoff time.Time) (int64, error)
	jobs         []*hkJob
}

func newHousekeeping(st *store.Store, clk clock.Clock, log *slog.Logger) *housekeeping {
	h := &housekeeping{store: st, clock: clk, log: log}
	h.deleteBefore = func(ctx context.Context, cutoff time.Time) (int64, error) {
		return st.Write().DeleteEventsBefore(ctx, cutoff)
	}
	h.jobs = append(h.jobs, &hkJob{
		name: "清理過期事件", setting: settingEventsCleanupNextAt, interval: eventCleanupInterval, retry: eventCleanupRetry,
		run: func(ctx context.Context, now time.Time) error {
			n, err := h.deleteBefore(ctx, now.Add(-eventRetention))
			if err != nil {
				return err
			}
			if n > 0 {
				h.log.InfoContext(ctx, "已清理過期的事件", "count", n)
			}
			return nil
		},
	})
	return h
}

// addTrashCleanup 加入垃圾桶清理：每次執行永久刪除 deleted_at 不晚於 now - retention 的影音。
func (h *housekeeping) addTrashCleanup(p trashPurger, retention time.Duration) {
	h.jobs = append(h.jobs, &hkJob{
		name: "清理過期的垃圾桶", setting: settingTrashCleanupNextAt, interval: trashCleanupInterval, retry: trashCleanupRetry,
		run: func(ctx context.Context, now time.Time) error {
			n, err := p.PurgeExpired(ctx, now, retention)
			if n > 0 {
				h.log.InfoContext(ctx, "已永久刪除過期的垃圾桶影音", "count", n)
			}
			return err
		},
	})
}

// Run 在每一輪開頭由 worker 呼叫：每項工作到期才執行，成功後排定下一次（間隔後）。
// 還沒有排定（第一次執行）或排定的時間無法解析時視為到期。
// 工作失敗時排定重試的時間（仍回傳錯誤）：否則排定的時間仍停在過去，每一輪都會重試，
// 而 worker 也無從得知下一次該什麼時候醒來。一項工作失敗不影響其他工作，所有錯誤一起回傳。
func (h *housekeeping) Run(ctx context.Context) error {
	now := h.clock.Now()
	var errs []error
	for _, j := range h.jobs {
		if err := h.runJob(ctx, j, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *housekeeping) runJob(ctx context.Context, j *hkJob, now time.Time) error {
	if next, ok := h.nextAt(ctx, j); ok && now.Before(next) {
		return nil
	}
	if err := j.run(ctx, now); err != nil {
		h.schedule(ctx, j, now.Add(j.retry))
		return fmt.Errorf("%s：%w", j.name, err)
	}
	if err := h.store.SetSetting(ctx, j.setting, now.Add(j.interval).UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("排定下一次%s：%w", j.name, err)
	}
	return nil
}

// schedule 把工作的下一次執行排在 at；寫入失敗只記 log（工作本身的失敗已經回報）。
func (h *housekeeping) schedule(ctx context.Context, j *hkJob, at time.Time) {
	if err := h.store.SetSetting(ctx, j.setting, at.UTC().Format(time.RFC3339)); err != nil {
		h.log.WarnContext(ctx, "排定重試時間失敗", "job", j.name, "error", err)
	}
}

// NextAt 回傳所有工作中最早的下一次執行時間；沒有任何工作排定時 ok 為 false（下一輪就會執行）。
func (h *housekeeping) NextAt(ctx context.Context) (time.Time, bool, error) {
	var earliest time.Time
	found := false
	for _, j := range h.jobs {
		if next, ok := h.nextAt(ctx, j); ok && (!found || next.Before(earliest)) {
			earliest, found = next, true
		}
	}
	return earliest, found, nil
}

// nextAt 讀出 settings 中工作排定的時間；沒有或格式不正確時 ok 為 false。
func (h *housekeeping) nextAt(ctx context.Context, j *hkJob) (time.Time, bool) {
	v, ok, err := h.store.GetSetting(ctx, j.setting)
	if err != nil || !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		h.log.WarnContext(ctx, "排定的時間格式不正確，視為到期", "setting", j.setting, "value", v)
		return time.Time{}, false
	}
	return t, true
}
