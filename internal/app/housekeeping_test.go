package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

type housekeepingRig struct {
	st  *store.Store
	clk *clock.Fake
	h   *housekeeping
}

func newHousekeepingRig(t *testing.T) *housekeepingRig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &housekeepingRig{st: st, clk: clk, h: newHousekeeping(st, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func (r *housekeepingRig) addEvent(t *testing.T, at time.Time) {
	t.Helper()
	if err := r.st.Write().InsertEvent(context.Background(), store.EventRecord{Type: "test.event", At: at}); err != nil {
		t.Fatal(err)
	}
}

func (r *housekeepingRig) count(t *testing.T) int64 {
	t.Helper()
	n, err := r.st.Read().CountEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *housekeepingRig) nextAt(t *testing.T) (time.Time, bool) {
	t.Helper()
	at, ok, err := r.h.NextAt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return at, ok
}

// 第一次執行（還沒有排定）就清理：只刪 90 天前的，保留期限內的留下，並排定 24 小時後。
func TestHousekeepingCleansOldEventsAndSchedulesNext(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	now := r.clk.Now()
	r.addEvent(t, now.Add(-91*24*time.Hour))
	r.addEvent(t, now.Add(-90*24*time.Hour)) // 剛好 90 天：保留
	r.addEvent(t, now.Add(-time.Hour))

	if _, ok := r.nextAt(t); ok {
		t.Fatal("第一次執行前不該有排定的時間")
	}
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t); n != 2 {
		t.Fatalf("清理後剩 %d 筆，預期 2", n)
	}
	next, ok := r.nextAt(t)
	if !ok || !next.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("下一次 = %v, %v；預期 %v", next, ok, now.Add(24*time.Hour))
	}
	v, _, _ := r.st.GetSetting(ctx, settingEventsCleanupNextAt)
	if v != "2026-11-13T10:00:00Z" {
		t.Fatalf("settings 的值 = %q", v)
	}
}

// 還沒到期不清理，也不改排定的時間；到期（用 fake clock 推進）才清理。
func TestHousekeepingWaitsUntilDue(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	if err := r.h.Run(ctx); err != nil { // 排定 24 小時後
		t.Fatal(err)
	}
	scheduled, _ := r.nextAt(t)

	// 一筆這一刻才變成過期的事件：晚到的舊事件。
	r.addEvent(t, r.clk.Now().Add(-100*24*time.Hour))
	r.clk.Advance(23 * time.Hour)
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t); n != 1 {
		t.Fatalf("還沒到期就清理了：剩 %d 筆，預期 1", n)
	}
	if got, _ := r.nextAt(t); !got.Equal(scheduled) {
		t.Fatalf("還沒到期不該改排定時間：%v → %v", scheduled, got)
	}

	r.clk.Advance(time.Hour) // 剛好到期
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t); n != 0 {
		t.Fatalf("到期後剩 %d 筆，預期 0", n)
	}
	if got, _ := r.nextAt(t); !got.Equal(r.clk.Now().Add(24 * time.Hour)) {
		t.Fatalf("到期後應排定 24 小時後，得到 %v", got)
	}
}

// 排定的時間無法解析時視為到期（自我修復），並改寫成正確的值。
func TestHousekeepingTreatsGarbageScheduleAsDue(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	if err := r.st.SetSetting(ctx, settingEventsCleanupNextAt, "tomorrow"); err != nil {
		t.Fatal(err)
	}
	r.addEvent(t, r.clk.Now().Add(-200*24*time.Hour))
	if _, ok := r.nextAt(t); ok {
		t.Fatal("無法解析的值不該回報為排定的時間")
	}
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := r.count(t); n != 0 {
		t.Fatalf("剩 %d 筆，預期 0", n)
	}
	if _, ok := r.nextAt(t); !ok {
		t.Fatal("清理後應改寫成有效的排定時間")
	}
}
