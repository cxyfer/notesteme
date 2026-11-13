package steps

import (
	"os"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

func TestMain(m *testing.M) {
	// 單元測試會讓 server 以 yt-dlp 的名稱執行測試 binary（gate 與假工具）：改扮假工具（ADR-0015）。
	faketool.Dispatch()
	os.Exit(m.Run())
}
