package faketool

import (
	"testing"

	"github.com/cxyfer/notesteme/features/internal/fakegit"
	"github.com/cxyfer/notesteme/features/internal/fakehook"
)

// 假 git 與假 hook 也經由 PATH 上的 symlink 分派（M3-S0），狀態放在場景的 fake/ 底下。
func TestDispatchGitAndHook(t *testing.T) {
	s := newScenario(t, sampleState(t))
	if code, _, errb := s.exec(Git, "-c", "core.hooksPath=/dev/null", "clone", "--branch", "main", "--single-branch",
		"https://git.example.com/me/notes.git", "repo"); code != 0 {
		t.Fatalf("git clone = %d：%s", code, errb)
	}
	calls, err := fakegit.Fake{StateDir: GitStateDir(s.root)}.Calls()
	if err != nil || len(calls) != 1 || calls[0].Argv[0] != "git" {
		t.Fatalf("calls = %+v，%v", calls, err)
	}
	if code, _, _ := s.exec(Hook, "/vault/a.md", "--exit", "0"); code != 0 {
		t.Fatalf("hook = %d", code)
	}
	runs, err := fakehook.Runs(HookRecord(s.root))
	if err != nil || len(runs) != 1 || runs[0].Argv[1] != "/vault/a.md" {
		t.Fatalf("hook runs = %+v，%v", runs, err)
	}
}
