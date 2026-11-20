package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
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

// housekeeping 是 worker 的定期工作（worker.Periodic），目前只有清理過期的事件。
// 下一次清理的時間存在 settings，重啟後不會因為記憶體狀態消失而重複清理或漏掉。
// 所有時間都來自注入的 clock。
type housekeeping struct {
	store *store.Store
	clock clock.Clock
	log   *slog.Logger
	// deleteBefore 刪除 cutoff 之前的事件；預設用 store，測試可以替換成會失敗的實作。
	deleteBefore func(ctx context.Context, cutoff time.Time) (int64, error)
}

func newHousekeeping(st *store.Store, clk clock.Clock, log *slog.Logger) *housekeeping {
	h := &housekeeping{store: st, clock: clk, log: log}
	h.deleteBefore = func(ctx context.Context, cutoff time.Time) (int64, error) {
		return st.Write().DeleteEventsBefore(ctx, cutoff)
	}
	return h
}

// Run 在每一輪開頭由 worker 呼叫：到期才清理，清理成功後排定 24 小時後的下一次。
// 還沒有排定（第一次執行）或排定的時間無法解析時視為到期。
// 清理失敗時排定 5 分鐘後重試（仍回傳錯誤）：否則排定的時間仍停在過去，每一輪都會重試，
// 而 worker 也無從得知下一次該什麼時候醒來。
func (h *housekeeping) Run(ctx context.Context) error {
	now := h.clock.Now()
	if next, ok := h.nextCleanup(ctx); ok && now.Before(next) {
		return nil
	}
	n, err := h.deleteBefore(ctx, now.Add(-eventRetention))
	if err != nil {
		h.scheduleRetry(ctx, now)
		return fmt.Errorf("清理過期事件：%w", err)
	}
	if n > 0 {
		h.log.InfoContext(ctx, "已清理過期的事件", "count", n)
	}
	next := now.Add(eventCleanupInterval)
	if err := h.store.SetSetting(ctx, settingEventsCleanupNextAt, next.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("排定下一次事件清理：%w", err)
	}
	return nil
}

// scheduleRetry 把下一次清理排在 now 之後的 eventCleanupRetry；寫入失敗只記 log（原本的失敗已經回報）。
func (h *housekeeping) scheduleRetry(ctx context.Context, now time.Time) {
	retry := now.Add(eventCleanupRetry)
	if err := h.store.SetSetting(ctx, settingEventsCleanupNextAt, retry.UTC().Format(time.RFC3339)); err != nil {
		h.log.WarnContext(ctx, "排定事件清理的重試時間失敗", "error", err)
	}
}

// NextAt 回傳下一次清理的時間；還沒有排定時 ok 為 false（下一輪就會清理）。
func (h *housekeeping) NextAt(ctx context.Context) (time.Time, bool, error) {
	next, ok := h.nextCleanup(ctx)
	return next, ok, nil
}

// nextCleanup 讀出 settings 中排定的時間；沒有或格式不正確時 ok 為 false。
func (h *housekeeping) nextCleanup(ctx context.Context) (time.Time, bool) {
	v, ok, err := h.store.GetSetting(ctx, settingEventsCleanupNextAt)
	if err != nil || !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		h.log.WarnContext(ctx, "events_cleanup_next_at 格式不正確，視為到期", "value", v)
		return time.Time{}, false
	}
	return t, true
}
