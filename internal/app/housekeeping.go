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
}

func newHousekeeping(st *store.Store, clk clock.Clock, log *slog.Logger) *housekeeping {
	return &housekeeping{store: st, clock: clk, log: log}
}

// Run 在每一輪開頭由 worker 呼叫：到期才清理，清理成功後排定 24 小時後的下一次。
// 還沒有排定（第一次執行）或排定的時間無法解析時視為到期。清理失敗不重新排定，下一輪再試。
func (h *housekeeping) Run(ctx context.Context) error {
	now := h.clock.Now()
	if next, ok := h.nextCleanup(ctx); ok && now.Before(next) {
		return nil
	}
	n, err := h.store.Write().DeleteEventsBefore(ctx, now.Add(-eventRetention))
	if err != nil {
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
