package steps

import "github.com/cucumber/godog"

// 這個檔案放 yt-dlp 的步驟（OPS-R6、R7）；語意見 docs/specs/steps.md 第 4.12 節。
// 骨架：目前都回 pending，由 OPS-B 實作。

// opsYtDlpInstalled 實作 `yt-dlp {string} is installed`。
func (s *Scenario) opsYtDlpInstalled(version string) error { return godog.ErrPending }

// opsYtDlpReleasePasses 實作 `the yt-dlp release {string} passes the self-check`。
func (s *Scenario) opsYtDlpReleasePasses(version string) error { return godog.ErrPending }

// opsYtDlpReleaseFails 實作 `the yt-dlp release {string} fails the self-check`。
func (s *Scenario) opsYtDlpReleaseFails(version string) error { return godog.ErrPending }
