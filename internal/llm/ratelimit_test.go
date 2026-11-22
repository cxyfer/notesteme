package llm

import (
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// LLM-R7 場景：rpm 1 時第二個請求要等滿一分鐘，剛好過一分鐘就放行。
func TestLimiterRPM(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewLimiter(clk)
	lim := Limits{RPM: 1}
	if w := l.Reserve("anthropic", lim, 100); w != 0 {
		t.Fatalf("第一個請求應該放行，等待 %v", w)
	}
	if w := l.Reserve("anthropic", lim, 100); w != time.Minute {
		t.Fatalf("第二個請求應該等 1m，得到 %v", w)
	}
	// 其他 provider 各自計算。
	if w := l.Reserve("openai", lim, 100); w != 0 {
		t.Fatalf("另一個 provider 不受影響，等待 %v", w)
	}
	clk.Advance(59 * time.Second)
	if w := l.Reserve("anthropic", lim, 100); w != time.Second {
		t.Fatalf("59 秒後還要等 1s，得到 %v", w)
	}
	clk.Advance(time.Second)
	if w := l.Reserve("anthropic", lim, 100); w != 0 {
		t.Fatalf("剛好一分鐘後應該放行，等待 %v", w)
	}
}

// 容量是每分鐘的上限：rpm 3 可以連續 3 個，之後每 20 秒補 1 個。
func TestLimiterBurstAndRefill(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewLimiter(clk)
	lim := Limits{RPM: 3}
	for i := 0; i < 3; i++ {
		if w := l.Reserve("p", lim, 0); w != 0 {
			t.Fatalf("第 %d 個應該放行，等待 %v", i+1, w)
		}
	}
	if w := l.Reserve("p", lim, 0); w != 20*time.Second {
		t.Fatalf("第 4 個應該等 20s，得到 %v", w)
	}
	clk.Advance(20 * time.Second)
	if w := l.Reserve("p", lim, 0); w != 0 {
		t.Fatalf("補了 1 個之後應該放行，等待 %v", w)
	}
}

func TestLimiterTPM(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewLimiter(clk)
	lim := Limits{TPM: 1000}
	if w := l.Reserve("p", lim, 600); w != 0 {
		t.Fatalf("600 tokens 應該放行，等待 %v", w)
	}
	// 還剩 400：要 600 時等補回 200（12 秒）。
	if w := l.Reserve("p", lim, 600); w != 12*time.Second {
		t.Fatalf("應該等 12s，得到 %v", w)
	}
	// 額度不夠時不扣：等 12 秒後剛好可以。
	clk.Advance(12 * time.Second)
	if w := l.Reserve("p", lim, 600); w != 0 {
		t.Fatalf("12s 後應該放行，等待 %v", w)
	}
	// 超過容量的請求只要 bucket 是滿的就放行，否則永遠不會通過。
	clk.Advance(time.Hour)
	if w := l.Reserve("p", lim, 5000); w != 0 {
		t.Fatalf("bucket 滿的時候超過容量的請求應該放行，等待 %v", w)
	}
	if w := l.Reserve("p", lim, 10); w <= 0 {
		t.Fatal("透支之後要等待")
	}
}

// RPM 與 TPM 任一不夠就整個不扣（不會只扣掉其中一個）。
func TestLimiterReserveIsAtomic(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewLimiter(clk)
	lim := Limits{RPM: 10, TPM: 100}
	if w := l.Reserve("p", lim, 100); w != 0 {
		t.Fatal(w)
	}
	if w := l.Reserve("p", lim, 50); w == 0 {
		t.Fatal("TPM 不夠時應該等待")
	}
	// RPM 沒有被扣：10 個請求的額度還剩 9。
	for i := 0; i < 9; i++ {
		if w := l.Reserve("p", Limits{RPM: 10}, 0); w != 0 {
			t.Fatalf("第 %d 個請求的 RPM 額度被錯誤扣除，等待 %v", i+1, w)
		}
	}
}

// Charge 透支（驗證重試立刻送出），之後的請求等待補回；沒有上限時一律放行。
func TestLimiterChargeAndUnlimited(t *testing.T) {
	clk := clock.NewFake(t0)
	l := NewLimiter(clk)
	lim := Limits{RPM: 1}
	if w := l.Reserve("p", lim, 0); w != 0 {
		t.Fatal(w)
	}
	l.Charge("p", lim, 0)
	if w := l.Reserve("p", lim, 0); w != 2*time.Minute {
		t.Fatalf("透支一個之後要等 2m，得到 %v", w)
	}
	for i := 0; i < 100; i++ {
		if w := l.Reserve("q", Limits{}, 1<<20); w != 0 {
			t.Fatalf("沒有上限時應該放行，等待 %v", w)
		}
	}
}
