package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/api"
)

// toolSpec 描述 /readyz 要確認可執行的外部工具（OPS-R5）。
type toolSpec struct {
	name   string // /readyz 回報的名稱，也是在 PATH 中尋找的檔名
	envVar string // 指定路徑的環境變數
}

var readyTools = []toolSpec{
	{name: "ffmpeg", envVar: "NOTESTEME_FFMPEG"},
	{name: "yt-dlp", envVar: "NOTESTEME_YTDLP"},
}

// toolChecks 建立 /readyz 的外部工具檢查。路徑只看 env 提供的 Vars（含 PATH），不讀程序的環境。
//
// TODO(OPS-R8)：尋找順序的第一層「設定檔的路徑」與工具版本檢查，等外部工具 runner 切片一併實作。
func toolChecks(vars map[string]string) []api.ReadyCheck {
	checks := make([]api.ReadyCheck, 0, len(readyTools))
	for _, t := range readyTools {
		checks = append(checks, api.ReadyCheck{
			Name: t.name,
			Check: func(context.Context) error {
				_, err := lookTool(vars, t)
				return err
			},
		})
	}
	return checks
}

// lookTool 依「環境變數 → PATH」找出可執行的工具。環境變數有設定時只認它，不再退回 PATH。
func lookTool(vars map[string]string, t toolSpec) (string, error) {
	if p := vars[t.envVar]; p != "" {
		if err := checkExecutable(p); err != nil {
			return "", fmt.Errorf("%s=%s：%w", t.envVar, p, err)
		}
		return p, nil
	}
	for _, dir := range filepath.SplitList(vars["PATH"]) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, t.name)
		if checkExecutable(p) == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("在 PATH 中找不到 %s", t.name)
}

// checkExecutable 確認 p 是（跟隨 symlink 後的）一般檔案且有執行權限。
func checkExecutable(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return errors.New("不是可執行的檔案")
	}
	return nil
}
