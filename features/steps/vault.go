package steps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/features/internal/fakegit"
	"github.com/cxyfer/notesteme/features/internal/fakehook"
	"github.com/cxyfer/notesteme/internal/config"
)

// registerVault 註冊「Vault 與 hook」的步驟（steps.md 4.16，M3）。
//
// M3-S6 實作了全部的句型：假 git 遠端（features/internal/fakegit）的設定與檢查、假 hook（fakehook）的執行紀錄。
func registerVault(r *Registry, s *Scenario) {
	r.Step(`the vault remote rejects pushes as {string}`, s.vaultRemoteRejects)
	r.Step(`the vault remote fails the next push with a network error`, s.vaultRemoteFailsNextPush)
	r.Step(`the vault remote received a commit with the files:`, s.vaultRemoteReceivedFiles)
	r.Step(`the vault remote received a single new commit`, s.vaultRemoteSingleCommit)
	r.Step(`every git command for the vault ran with {string}`, s.everyGitCommandRanWith)
	r.Step(`no git command for the vault contained {string}`, s.noGitCommandContained)
	r.Step(`the hook {string} ran with the arguments:`, s.hookRanWith)
	r.Step(`the hook {string} did not run`, s.hookDidNotRun)
	r.Step(`the hook {string} saw the environment variable {string}`, s.hookSawEnv)
	r.Step(`the hook {string} did not see the environment variable {string}`, s.hookDidNotSeeEnv)
}

func (s *Scenario) vaultRemoteRejects(kind string) error {
	if kind != fakegit.RejectNonFastForward && kind != fakegit.RejectBadCredentials {
		return fmt.Errorf("拒絕的方式只有 %q 與 %q，收到 %q", fakegit.RejectNonFastForward, fakegit.RejectBadCredentials, kind)
	}
	f := s.World.FakeGit()
	st, err := f.Remote()
	if err != nil {
		return err
	}
	st.Reject = kind
	return f.SetRemote(st)
}

func (s *Scenario) vaultRemoteFailsNextPush() error {
	f := s.World.FakeGit()
	st, err := f.Remote()
	if err != nil {
		return err
	}
	st.FailNextPush = true
	return f.SetRemote(st)
}

// vaultRemoteReceivedFiles 比對假遠端最後一個 commit 變動的檔案與表格（欄位 path）完全相同（不論順序）。
func (s *Scenario) vaultRemoteReceivedFiles(table *godog.Table) error {
	if len(table.Rows) == 0 || len(table.Rows[0].Cells) != 1 || table.Rows[0].Cells[0].Value != "path" {
		return errors.New("表格只能有一個欄位 path")
	}
	var want []string
	for _, r := range table.Rows[1:] {
		want = append(want, r.Cells[0].Value)
	}
	st, err := s.World.FakeGit().Remote()
	if err != nil {
		return err
	}
	if len(st.Commits) == 0 {
		return errors.New("假遠端沒有收到任何 commit")
	}
	got := slices.Clone(st.Commits[len(st.Commits)-1].Files)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("最後一個 commit 的檔案是 %q，預期 %q", got, want)
	}
	return nil
}

// vaultRemoteSingleCommit 確認場景開始後假遠端的分支只多了一個 commit（假遠端在場景中第一次 clone 時才建立，所以就是總數）。
func (s *Scenario) vaultRemoteSingleCommit() error {
	st, err := s.World.FakeGit().Remote()
	if err != nil {
		return err
	}
	if len(st.Commits) != 1 {
		return fmt.Errorf("假遠端有 %d 個 commit，預期 1 個", len(st.Commits))
	}
	return nil
}

func (s *Scenario) gitCalls() ([]fakegit.Call, error) {
	calls, err := s.World.FakeGit().Calls()
	if err != nil {
		return nil, err
	}
	if len(calls) == 0 {
		return nil, errors.New("假 git 沒有收到任何呼叫（沒有東西可以驗證）")
	}
	return calls, nil
}

func (s *Scenario) everyGitCommandRanWith(elem string) error {
	calls, err := s.gitCalls()
	if err != nil {
		return err
	}
	for _, c := range calls {
		if !slices.Contains(c.Argv, elem) {
			return fmt.Errorf("git 呼叫 %q 沒有參數 %q", c.Argv, elem)
		}
	}
	return nil
}

func (s *Scenario) noGitCommandContained(text string) error {
	calls, err := s.gitCalls()
	if err != nil {
		return err
	}
	for _, c := range calls {
		for _, a := range c.Argv {
			if strings.Contains(a, text) {
				return fmt.Errorf("git 呼叫 %q 含有 %q", c.Argv, text)
			}
		}
	}
	return nil
}

// hookRuns 回傳設定檔中這個 hook 的執行紀錄：argv 的長度相同、沒有變數的元素逐字相同的紀錄。
// 設定檔沒有這個 hook 時失敗。
func (s *Scenario) hookRuns(id string) ([]fakehook.Run, error) {
	b, err := os.ReadFile(s.World.Expand(s.World.Env[config.EnvConfig]))
	if err != nil {
		return nil, fmt.Errorf("讀取設定檔：%w", err)
	}
	var cfg struct {
		Hooks map[string]struct {
			Argv []string `yaml:"argv"`
		} `yaml:"hooks"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("設定檔不是合法的 YAML：%w", err)
	}
	h, ok := cfg.Hooks[id]
	if !ok {
		return nil, fmt.Errorf("設定檔沒有 hook %q", id)
	}
	all, err := fakehook.Runs(s.World.HookRecord())
	if err != nil {
		return nil, err
	}
	var out []fakehook.Run
	for _, r := range all {
		if len(r.Argv) != len(h.Argv) {
			continue
		}
		match := true
		for i := 1; i < len(h.Argv); i++ {
			if !strings.Contains(h.Argv[i], "{{") && r.Argv[i] != h.Argv[i] {
				match = false
				break
			}
		}
		if match {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Scenario) lastHookRun(id string) (fakehook.Run, error) {
	runs, err := s.hookRuns(id)
	if err != nil {
		return fakehook.Run{}, err
	}
	if len(runs) == 0 {
		return fakehook.Run{}, fmt.Errorf("hook %s 沒有執行過", id)
	}
	return runs[len(runs)-1], nil
}

// hookRanWith 比對 hook 最近一次的 argv（不含第一個元素，實際路徑換回虛擬路徑）與 JSON 字串陣列完全相同。
func (s *Scenario) hookRanWith(id string, doc *godog.DocString) error {
	var want []string
	if err := json.Unmarshal([]byte(doc.Content), &want); err != nil {
		return fmt.Errorf("預期的參數不是 JSON 字串陣列：%w", err)
	}
	run, err := s.lastHookRun(id)
	if err != nil {
		return err
	}
	got := make([]string, 0, len(run.Argv))
	for _, a := range run.Argv[1:] {
		got = append(got, s.World.Unexpand(a))
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("hook %s 的參數是 %q，預期 %q", id, got, want)
	}
	return nil
}

func (s *Scenario) hookDidNotRun(id string) error {
	runs, err := s.hookRuns(id)
	if err != nil {
		return err
	}
	if len(runs) > 0 {
		return fmt.Errorf("hook %s 執行了 %d 次，預期沒有執行", id, len(runs))
	}
	return nil
}

func (s *Scenario) hookSawEnv(id, name string) error {
	run, err := s.lastHookRun(id)
	if err != nil {
		return err
	}
	if _, ok := run.Env[name]; !ok {
		return fmt.Errorf("hook %s 的環境沒有 %s（有 %d 個變數）", id, name, len(run.Env))
	}
	return nil
}

func (s *Scenario) hookDidNotSeeEnv(id, name string) error {
	run, err := s.lastHookRun(id)
	if err != nil {
		return err
	}
	if _, ok := run.Env[name]; ok {
		return fmt.Errorf("hook %s 的環境有 %s", id, name)
	}
	return nil
}
