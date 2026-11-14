package steps

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/features/internal/faketool"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// 這個檔案放 yt-dlp 的步驟（OPS-R6、R7）；語意見 docs/specs/steps.md 第 4.12 節。
// 在 OPS 規格規定的目錄結構中建立假的 venv，不連外網：假 python 的 pip 只認得場景登記的版本。

// opsYtDlpInstalled 實作 `yt-dlp {string} is installed`：
// <data>/tools/yt-dlp/<版本>/ 是假 venv，<data>/tools/yt-dlp-venv 是指向它的相對 symlink。
func (s *Scenario) opsYtDlpInstalled(version string) error {
	tools := ytdlp.ToolsDir(s.World.DataDir())
	venv := filepath.Join(ytdlp.VersionsDir(tools), version)
	if err := os.MkdirAll(filepath.Dir(venv), 0o700); err != nil {
		return err
	}
	if err := faketool.InstallVenv(venv, version); err != nil {
		return fmt.Errorf("建立假 venv：%w", err)
	}
	link := ytdlp.LinkPath(tools)
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(filepath.Join(ytdlp.VersionsDirName, version), link)
}

// opsYtDlpIsActive 實作 `yt-dlp {string} is the active version`：
// <data>/tools/yt-dlp-venv 必須是 symlink，解析後指向 yt-dlp/ 底下的 venv，且該 venv 內的 yt-dlp 回報 version。
// symlink 不存在、壞掉（目標不存在）、或指到別處都失敗，才抓得到「更新後沒切換」與「失敗仍切換」。
func (s *Scenario) opsYtDlpIsActive(version string) error {
	tools := ytdlp.ToolsDir(s.World.DataDir())
	link := ytdlp.LinkPath(tools)
	fi, err := os.Lstat(link)
	if err != nil {
		return fmt.Errorf("yt-dlp-venv 不存在：%w", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("yt-dlp-venv 不是 symlink（%s）", fi.Mode())
	}
	venv, err := filepath.EvalSymlinks(link)
	if err != nil {
		target, _ := os.Readlink(link)
		return fmt.Errorf("yt-dlp-venv 是壞掉的 symlink（指向 %q）：%w", target, err)
	}
	got, err := faketool.VenvYtDlpVersion(venv)
	if err != nil {
		return fmt.Errorf("yt-dlp-venv 指向 %s：%w", venv, err)
	}
	if got != version {
		return fmt.Errorf("使用中的 yt-dlp 是 %q（%s），預期 %q", got, venv, version)
	}
	return nil
}

// opsYtDlpReleasePasses 實作 `the yt-dlp release {string} passes the self-check`。
func (s *Scenario) opsYtDlpReleasePasses(version string) error {
	return s.registerYtDlpRelease(version, false)
}

// opsYtDlpReleaseFails 實作 `the yt-dlp release {string} fails the self-check`：
// 只有對檢查影片的探測失敗，`--version` 照常成功。
func (s *Scenario) opsYtDlpReleaseFails(version string) error {
	return s.registerYtDlpRelease(version, true)
}

// registerYtDlpRelease 讓假 pip 可以安裝 version，並確保檢查影片存在（探測才會成功）。
func (s *Scenario) registerYtDlpRelease(version string, failsProbe bool) error {
	w := s.World
	if w.Tools.Releases == nil {
		w.Tools.Releases = map[string]*faketool.Release{}
	}
	rel := &faketool.Release{}
	if failsProbe {
		rel.ProbeFailsFor = []string{ytdlp.CheckVideoID}
	}
	w.Tools.Releases[version] = rel
	v := w.Video(ytdlp.CheckVideoID)
	if v.Title == "" {
		v.Title, v.Duration, v.Language = "Me at the zoo", 19, "en"
	}
	return w.SyncTools()
}
