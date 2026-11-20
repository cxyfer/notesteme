package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/worker"
)

// 編譯期確認：worker 可以當 pipeline 的 TaskCanceller，也可以當 Periodic 的使用者。
var _ pipeline.TaskCanceller = (*worker.Worker)(nil)

// fakePurger 記下每次呼叫的參數。
type fakePurger struct {
	calls     []purgeCall
	failFirst int // 前幾次呼叫回傳錯誤
	n         int // 每次回傳的永久刪除數
}

type purgeCall struct {
	now       time.Time
	retention time.Duration
}

func (f *fakePurger) PurgeExpired(_ context.Context, now time.Time, retention time.Duration) (int, error) {
	f.calls = append(f.calls, purgeCall{now, retention})
	if len(f.calls) <= f.failFirst {
		return 0, errors.New("disk on fire")
	}
	return f.n, nil
}

func (r *housekeepingRig) trashSetting(t *testing.T) string {
	t.Helper()
	v, _, err := r.st.GetSetting(context.Background(), settingTrashCleanupNextAt)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// 垃圾桶清理：第一次就執行，時間與保留期限來自 clock 與設定；之後每 24 小時一次，下一次存在自己的 settings 鍵。
func TestHousekeepingPurgesTrashDailyWithOwnSetting(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	p := &fakePurger{n: 2}
	retention := 7 * 24 * time.Hour
	r.h.addTrashCleanup(p, retention)
	start := r.clk.Now()

	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 || !p.calls[0].now.Equal(start) || p.calls[0].retention != retention {
		t.Fatalf("calls = %+v，預期 1 次，now = %v、retention = %v", p.calls, start, retention)
	}
	if got := r.trashSetting(t); got != "2026-11-13T10:00:00Z" {
		t.Fatalf("trash_cleanup_next_at = %q", got)
	}
	if settingTrashCleanupNextAt != "trash_cleanup_next_at" {
		t.Errorf("settings 鍵 = %q", settingTrashCleanupNextAt)
	}
	// 事件清理有自己的鍵，互不影響。
	if v, _, _ := r.st.GetSetting(ctx, settingEventsCleanupNextAt); v != "2026-11-13T10:00:00Z" {
		t.Errorf("events_cleanup_next_at = %q", v)
	}

	r.clk.Advance(23*time.Hour + 59*time.Minute)
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("還沒到期就清理了：%d 次", len(p.calls))
	}
	r.clk.Advance(time.Minute) // 剛好 24 小時
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 2 || !p.calls[1].now.Equal(start.Add(24*time.Hour)) {
		t.Fatalf("calls = %+v", p.calls)
	}
	if got := r.trashSetting(t); got != "2026-11-14T10:00:00Z" {
		t.Errorf("trash_cleanup_next_at = %q", got)
	}
}

// 失敗時 5 分鐘後重試，不等到明天，也不在每一輪都重試；成功後回到每天一次。
func TestHousekeepingTrashCleanupRetriesAfterFiveMinutes(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	p := &fakePurger{failFirst: 1}
	r.h.addTrashCleanup(p, time.Hour)
	now := r.clk.Now()

	err := r.h.Run(ctx)
	if err == nil {
		t.Fatal("清理失敗應該回報錯誤")
	}
	if got, want := r.trashSetting(t), now.Add(5*time.Minute).UTC().Format(time.RFC3339); got != want {
		t.Fatalf("失敗後的排定時間 = %q，預期 %q", got, want)
	}
	r.clk.Advance(4 * time.Minute)
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("重試時間之前不該再試：%d 次", len(p.calls))
	}
	r.clk.Advance(time.Minute)
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("到了重試時間要再試：%d 次", len(p.calls))
	}
	if got, want := r.trashSetting(t), r.clk.Now().Add(24*time.Hour).UTC().Format(time.RFC3339); got != want {
		t.Errorf("成功後應回到每天一次：%q，預期 %q", got, want)
	}
}

// 一項工作失敗不影響另一項：事件清理失敗時垃圾桶清理照常執行，反之亦然；排程各自獨立。
func TestHousekeepingJobsFailIndependently(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	r.h.deleteBefore = func(context.Context, time.Time) (int64, error) { return 0, errors.New("events broken") }
	p := &fakePurger{}
	r.h.addTrashCleanup(p, time.Hour)
	now := r.clk.Now()

	if err := r.h.Run(ctx); err == nil {
		t.Fatal("事件清理失敗應該回報錯誤")
	}
	if len(p.calls) != 1 {
		t.Fatalf("事件清理失敗時垃圾桶清理仍要執行：%d 次", len(p.calls))
	}
	if v, _, _ := r.st.GetSetting(ctx, settingEventsCleanupNextAt); v != now.Add(5*time.Minute).UTC().Format(time.RFC3339) {
		t.Errorf("事件清理應該 5 分鐘後重試：%q", v)
	}
	if got := r.trashSetting(t); got != now.Add(24*time.Hour).UTC().Format(time.RFC3339) {
		t.Errorf("垃圾桶清理成功，應該排在 24 小時後：%q", got)
	}

	// 垃圾桶清理失敗、事件清理成功。
	r2 := newHousekeepingRig(t)
	p2 := &fakePurger{failFirst: 1}
	r2.h.addTrashCleanup(p2, time.Hour)
	if err := r2.h.Run(ctx); err == nil {
		t.Fatal("垃圾桶清理失敗應該回報錯誤")
	}
	if v, _, _ := r2.st.GetSetting(ctx, settingEventsCleanupNextAt); v != r2.clk.Now().Add(24*time.Hour).UTC().Format(time.RFC3339) {
		t.Errorf("事件清理成功，應該排在 24 小時後：%q", v)
	}
}

// NextAt 是所有工作中最早的一個，worker 閒置時才會在最早的時間醒來。
func TestHousekeepingNextAtIsEarliestJob(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	p := &fakePurger{failFirst: 1}
	r.h.addTrashCleanup(p, time.Hour)
	if _, ok := r.nextAt(t); ok {
		t.Fatal("還沒執行過，不該有排定的時間")
	}
	now := r.clk.Now()
	_ = r.h.Run(ctx) // 垃圾桶清理失敗：5 分鐘後重試；事件清理成功：24 小時後
	if next, ok := r.nextAt(t); !ok || !next.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("NextAt = %v, %v；預期 %v", next, ok, now.Add(5*time.Minute))
	}
}
