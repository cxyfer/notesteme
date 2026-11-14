package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 清理失敗：仍回報錯誤，但排定 5 分鐘後重試；重試時間之前不會再試，到了才試。
func TestHousekeepingSchedulesRetryAfterFailure(t *testing.T) {
	ctx := context.Background()
	r := newHousekeepingRig(t)
	calls := 0
	failing := true
	r.h.deleteBefore = func(ctx context.Context, cutoff time.Time) (int64, error) {
		calls++
		if failing {
			return 0, errors.New("disk on fire")
		}
		return 0, nil
	}
	now := r.clk.Now()

	if err := r.h.Run(ctx); err == nil {
		t.Fatal("清理失敗應該回報錯誤")
	}
	next, ok := r.nextAt(t)
	if !ok || !next.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("失敗後的排定時間 = %v, %v；預期 %v", next, ok, now.Add(5*time.Minute))
	}

	r.clk.Advance(4 * time.Minute)
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("重試時間之前不該再試：呼叫 %d 次", calls)
	}

	failing = false
	r.clk.Advance(time.Minute) // 剛好 5 分鐘
	if err := r.h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("到了重試時間要再試：呼叫 %d 次", calls)
	}
	if next, _ := r.nextAt(t); !next.Equal(r.clk.Now().Add(24 * time.Hour)) {
		t.Fatalf("成功後應回到每天一次：%v", next)
	}
}
