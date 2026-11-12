package steps

import (
	"fmt"
	"slices"

	"github.com/cucumber/godog"
)

// registerQueue 註冊「Pipeline 與佇列」的步驟，句型見 docs/specs/steps.md。
func registerQueue(r *Registry, s *Scenario) {
	r.Step(`the concurrency limit of {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the queue is paused`, func() error { return godog.ErrPending })
	r.Step(`the resource class {string} is paused`, func(string) error { return godog.ErrPending })
	r.Step(`the owner pauses the queue`, func() error { return godog.ErrPending })
	r.Step(`the owner resumes the queue`, func() error { return godog.ErrPending })
	r.Step(`the worker starts one round`, func() error { return godog.ErrPending })
	r.Step(`{int} tasks of {string} are running`, func(int, string) error { return godog.ErrPending })
	r.Step(`1 task of {string} is running`, func(string) error { return godog.ErrPending })
	r.Step(`the implementation version of the stage {string} is {int}`, func(string, int) error { return godog.ErrPending })
	r.Step(`the next task belongs to {string}`, func(string) error { return godog.ErrPending })
	r.Step(`the task of {string} for {string} has {int} attempts`, func(string, string, int) error { return godog.ErrPending })
	r.Step(`the source {string} was called {int} times`, s.sourceWasCalled)
	r.Step(`no task is running`, func() error { return godog.ErrPending })
}

// platformCaptionFlag 是平台字幕來源對應的 yt-dlp 旗標：下載字幕時，人工字幕用 --write-subs，自動字幕用 --write-auto-subs。
var platformCaptionFlag = map[string]string{
	"youtube-manual": "--write-subs",
	"youtube-auto":   "--write-auto-subs",
}

// sourceWasCalled 比對字幕來源被呼叫的次數。
// 平台來源是假 yt-dlp 的呼叫紀錄中下載對應字幕的次數；其他來源（Supadata、ASR）是 fakesource 腳本記錄的請求數。
func (s *Scenario) sourceWasCalled(id string, want int) error {
	got, err := s.sourceCalls(id)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("來源 %q 被呼叫 %d 次，預期 %d 次", id, got, want)
	}
	return nil
}

func (s *Scenario) sourceCalls(id string) (int, error) {
	flag, platform := platformCaptionFlag[id]
	if !platform {
		return s.World.Sources.Calls(id), nil
	}
	calls, err := s.World.ToolCalls()
	if err != nil {
		return 0, fmt.Errorf("讀取假工具的呼叫紀錄：%w", err)
	}
	n := 0
	for _, c := range calls {
		if c.Tool == "yt-dlp" && slices.Contains(c.Argv, "--skip-download") && slices.Contains(c.Argv, flag) {
			n++
		}
	}
	return n, nil
}
