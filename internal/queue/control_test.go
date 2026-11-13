package queue

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestControlDefaultsFollowADR0004(t *testing.T) {
	st, clk := setup(t)
	c := New(st, clk, "w").Control()
	if err := c.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{ResourceDownload: 2, ResourceASR: 3, ResourceLLM: 4}
	for class, n := range want {
		if got := c.Limit(class); got != n {
			t.Errorf("%s 的預設上限 = %d，預期 %d", class, got, n)
		}
	}
	if got := c.Limit(ResourceFrames); got < 1 {
		t.Errorf("frames 的預設上限應是 CPU 核心數，實際 %d", got)
	}
	if got := c.Limit(ResourceLocal); got != LocalLimit {
		t.Errorf("local 的上限 = %d，預期 %d", got, LocalLimit)
	}
}

func TestControlValidatesInput(t *testing.T) {
	ctx := context.Background()
	st, clk := setup(t)
	c := New(st, clk, "w").Control()

	if err := c.SetLimits(ctx, map[string]int{"bogus": 2}); !errors.Is(err, ErrUnknownClass) {
		t.Errorf("不認識的類別：%v", err)
	}
	// local 是內部用的，不開放調整。
	if err := c.SetLimits(ctx, map[string]int{ResourceLocal: 2}); !errors.Is(err, ErrUnknownClass) {
		t.Errorf("local 不開放調整：%v", err)
	}
	for _, n := range []int{0, -1} {
		if err := c.SetLimits(ctx, map[string]int{ResourceDownload: n}); !errors.Is(err, ErrInvalidLimit) {
			t.Errorf("上限 %d：%v", n, err)
		}
	}
	// 上限的最大值是 MaxLimit：剛好可以，超過就拒絕。
	if err := c.SetLimits(ctx, map[string]int{ResourceDownload: MaxLimit}); err != nil {
		t.Errorf("上限 %d 應該可以：%v", MaxLimit, err)
	}
	if err := c.SetLimits(ctx, map[string]int{ResourceDownload: MaxLimit + 1}); !errors.Is(err, ErrInvalidLimit) {
		t.Errorf("上限 %d：%v", MaxLimit+1, err)
	}
	if got := c.Limit(ResourceDownload); got != MaxLimit {
		t.Errorf("超過上限的請求不該改動設定，download = %d", got)
	}
	if err := c.SetLimits(ctx, map[string]int{ResourceDownload: 2}); err != nil {
		t.Fatal(err)
	}
	// 有一項不合法就全部不改。
	if err := c.SetLimits(ctx, map[string]int{ResourceDownload: 5, "bogus": 1}); err == nil {
		t.Fatal("應失敗")
	}
	if got := c.Limit(ResourceDownload); got != 2 {
		t.Errorf("失敗後不應改動，download = %d", got)
	}
	if err := c.Pause(ctx, "bogus"); !errors.Is(err, ErrUnknownClass) {
		t.Errorf("暫停不認識的類別：%v", err)
	}
	if err := c.Resume(ctx, ResourceLocal); !errors.Is(err, ErrUnknownClass) {
		t.Errorf("local 不能單獨暫停：%v", err)
	}
}

// 上限與暫停存在 settings 表：重啟（換一個 Queue、重新 Load）之後保留；只改有給的類別。
func TestControlPersistsAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	st, clk := setup(t)
	c := New(st, clk, "w1").Control()
	if err := c.SetLimits(ctx, map[string]int{ResourceDownload: 5}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLimits(ctx, map[string]int{ResourceASR: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, ResourceLLM); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, ""); err != nil {
		t.Fatal(err)
	}

	next := New(st, clk, "w2").Control()
	if got := next.Limit(ResourceDownload); got != 2 {
		t.Errorf("Load 之前應是預設值，download = %d", got)
	}
	if err := next.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if next.Limit(ResourceDownload) != 5 || next.Limit(ResourceASR) != 1 || next.Limit(ResourceLLM) != 4 {
		t.Errorf("上限沒有保留：%+v", next.Status())
	}
	if !next.Paused() || !next.ClassPaused(ResourceLLM) {
		t.Errorf("暫停沒有保留：%+v", next.Status())
	}

	// 整個佇列恢復後，個別類別的暫停仍在。
	if err := next.Resume(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if next.Paused() || !next.ClassPaused(ResourceLLM) || next.ClassPaused(ResourceASR) {
		t.Errorf("恢復整個佇列不應解除個別類別的暫停：%+v", next.Status())
	}
}

func TestFreeSlotsDependOnLimitRunningAndPause(t *testing.T) {
	ctx := context.Background()
	st, clk := setup(t)
	c := New(st, clk, "w").Control()

	free := c.Free(map[string]int{ResourceDownload: 1, ResourceASR: 3})
	if free[ResourceDownload] != 1 || free[ResourceASR] != 0 || free[ResourceLLM] != 4 || free[ResourceLocal] != LocalLimit {
		t.Errorf("空位 = %v", free)
	}
	if _, ok := free[ResourceASR]; ok {
		t.Error("已滿的類別不應出現在結果中")
	}

	if err := c.Pause(ctx, ResourceLLM); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Free(nil)[ResourceLLM]; ok {
		t.Error("暫停的類別沒有空位")
	}
	if err := c.Pause(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if free := c.Free(nil); len(free) != 0 {
		t.Errorf("整個佇列暫停時沒有任何空位：%v", free)
	}

	// 重複暫停同一個類別不會重複記錄。
	if err := c.Pause(ctx, ResourceASR); err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, ResourceASR); err != nil {
		t.Fatal(err)
	}
	var paused []string
	for _, cs := range c.Status().Classes {
		if cs.Paused {
			paused = append(paused, cs.Class)
		}
	}
	if !slices.Equal(paused, []string{ResourceASR, ResourceLLM}) {
		t.Errorf("暫停的類別 = %v", paused)
	}
}
