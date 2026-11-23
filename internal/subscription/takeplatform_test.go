package subscription

import (
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
)

// TakePlatform 與訂閱的探索共用平台的 bucket（SUB-R10、SRC-R19）。
func TestTakePlatformSharesBucket(t *testing.T) {
	cfg := config.Subscriptions{}
	cfg.RateLimits.YouTube, cfg.RateLimits.Bilibili = 2, 1
	r := newYtRig(t, cfg)
	for i := 0; i < 2; i++ {
		if ok, _ := r.svc.TakePlatform(PlatformYouTube); !ok {
			t.Fatalf("第 %d 顆應該有額度", i+1)
		}
	}
	ok, at := r.svc.TakePlatform(PlatformYouTube)
	if ok || !at.Equal(r.clk.Now().Add(30*time.Second)) {
		t.Fatalf("用完：ok=%v at=%v", ok, at)
	}
	if err := r.svc.take(PlatformYouTube, ""); err == nil {
		t.Fatal("探索與重新 Probe 共用同一個 bucket")
	}
	if ok, _ := r.svc.TakePlatform(PlatformBilibili); !ok {
		t.Fatal("不同平台各自的 bucket")
	}
}
