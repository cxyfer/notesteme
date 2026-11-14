package steps

import (
	"strings"
	"testing"
	"time"
)

// 獨立模式的 --wait 在等 fake clock（沒有人推進它）時會永遠不結束：
// ownerRuns 要在真實時間的上限之後讓步驟失敗並說明原因，而不是卡住整個套件。
func TestOwnerRunsFailsWhenCLIDoesNotFinish(t *testing.T) {
	old := cliTimeout
	cliTimeout = 500 * time.Millisecond
	t.Cleanup(func() { cliTimeout = old })

	s := newTestScenario(t)
	// 直播中的影片：metadata 為 waiting，--wait 要等 fake clock 走到下一次檢查。
	s.World.Video("abc123xxxxx").LiveStatus = "is_live"
	if err := s.World.SyncTools(); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.ownerRuns(`notesteme ingest https://www.youtube.com/watch?v=abc123xxxxx --wait --timeout 1h`)
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "CLI 在") || !strings.Contains(err.Error(), "沒有結束，可能是 --wait 在等 fake clock") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ownerRuns 沒有在逾時後結束")
	}
}

// 正常結束的指令不受逾時影響。
func TestOwnerRunsWithinTimeoutSucceeds(t *testing.T) {
	s := newTestScenario(t)
	if err := s.ownerRuns(`notesteme items ls --json`); err != nil {
		t.Fatalf("ownerRuns：%v", err)
	}
}
