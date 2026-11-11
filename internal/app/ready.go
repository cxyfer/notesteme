package app

import (
	"context"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// readyTools 是 /readyz 要確認可執行的外部工具（OPS-R5）。
var readyTools = []toolexec.Spec{toolexec.FFmpeg, toolexec.YtDlp}

// toolChecks 建立 /readyz 的外部工具檢查。尋找順序與實際執行工具時相同（toolexec.Resolver，OPS-R8），
// 只看注入的環境變數（含 PATH），不讀程序的環境。
//
// TODO(OPS-R6)：工具版本檢查與 yt-dlp 的自我檢查。
func toolChecks(r toolexec.Resolver) []api.ReadyCheck {
	checks := make([]api.ReadyCheck, 0, len(readyTools))
	for _, t := range readyTools {
		checks = append(checks, api.ReadyCheck{
			Name: t.Name,
			Check: func(context.Context) error {
				_, err := r.Resolve(t)
				return err
			},
		})
	}
	return checks
}
