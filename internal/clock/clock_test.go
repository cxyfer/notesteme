package clock

import (
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func recv(t *testing.T, tm Timer) (time.Time, bool) {
	t.Helper()
	select {
	case v := <-tm.C():
		return v, true
	default:
		return time.Time{}, false
	}
}

func TestFakeNowSetAdvance(t *testing.T) {
	f := NewFake(t0)
	if !f.Now().Equal(t0) {
		t.Fatalf("Now = %v", f.Now())
	}
	f.Advance(2 * time.Hour)
	if want := t0.Add(2 * time.Hour); !f.Now().Equal(want) {
		t.Fatalf("Now = %v, want %v", f.Now(), want)
	}
	f.Set(t0)
	if !f.Now().Equal(t0) {
		t.Fatalf("Set 後 Now = %v", f.Now())
	}
}

func TestFakeTimerFiresOnAdvance(t *testing.T) {
	f := NewFake(t0)
	tm := f.NewTimer(time.Minute)
	if _, ok := recv(t, tm); ok {
		t.Fatal("尚未到期不應觸發")
	}
	f.Advance(59 * time.Second)
	if _, ok := recv(t, tm); ok {
		t.Fatal("59 秒時不應觸發")
	}
	f.Advance(time.Second)
	v, ok := recv(t, tm)
	if !ok {
		t.Fatal("到期應觸發")
	}
	if want := t0.Add(time.Minute); !v.Equal(want) {
		t.Fatalf("收到 %v, want %v", v, want)
	}
	if tm.Stop() {
		t.Fatal("已到期的 Stop 應回傳 false")
	}
}

func TestFakeTimerFiresOnSet(t *testing.T) {
	f := NewFake(t0)
	tm := f.NewTimer(time.Hour)
	f.Set(t0.Add(24 * time.Hour))
	if _, ok := recv(t, tm); !ok {
		t.Fatal("Set 到期後應觸發")
	}
	// 往回設不觸發新的計時器。
	tm2 := f.NewTimer(time.Hour)
	f.Set(t0)
	if _, ok := recv(t, tm2); ok {
		t.Fatal("往回設時間不應觸發")
	}
}

func TestFakeTimerStop(t *testing.T) {
	f := NewFake(t0)
	tm := f.NewTimer(time.Minute)
	if !tm.Stop() {
		t.Fatal("未到期的 Stop 應回傳 true")
	}
	if tm.Stop() {
		t.Fatal("重複 Stop 應回傳 false")
	}
	f.Advance(time.Hour)
	if _, ok := recv(t, tm); ok {
		t.Fatal("已停止的計時器不應觸發")
	}
}

func TestFakeTimerNonPositive(t *testing.T) {
	f := NewFake(t0)
	tm := f.NewTimer(0)
	if _, ok := recv(t, tm); !ok {
		t.Fatal("d <= 0 應立即到期")
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := NewFake(t0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm := f.NewTimer(time.Second)
			_ = f.Now()
			f.Advance(time.Second)
			tm.Stop()
		}()
	}
	wg.Wait()
}

func TestReal(t *testing.T) {
	var c Clock = Real{}
	before := time.Now()
	if c.Now().Before(before) {
		t.Fatal("Real.Now 不應早於呼叫前")
	}
	tm := c.NewTimer(time.Millisecond)
	select {
	case <-tm.C():
	case <-time.After(5 * time.Second):
		t.Fatal("Real timer 未觸發")
	}
	tm2 := c.NewTimer(time.Hour)
	if !tm2.Stop() {
		t.Fatal("Stop 應回傳 true")
	}
}

var _ Clock = (*Fake)(nil)
