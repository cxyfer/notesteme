//go:build faultinject && unix

package fault

import (
	"os"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/fakenet"
	"github.com/cxyfer/notesteme/features/internal/faketool"
)

func TestMain(m *testing.M) {
	// 子程序裡的 pipeline 以 yt-dlp、ffmpeg 的名稱執行這個 binary 時，改扮假工具（ADR-0015）。
	faketool.Dispatch()
	// 繞過 netguard 的對外連線一律失敗並記錄違規（NFR-TEST-1）。
	restore := fakenet.InstallTrap()
	code := m.Run()
	restore()
	os.Exit(code)
}
