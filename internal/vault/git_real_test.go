package vault

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// realGit 在 t.TempDir() 中建立一個真的 git repo，回傳 repo 目錄與在其中執行 git 的函式（測試自己的操作，不經 Git）。
func realGit(t *testing.T) (string, string, func(args ...string) string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("沒有 git")
	}
	dir := t.TempDir()
	home := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		c := exec.Command(gitPath, args...)
		c.Dir = dir
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q", "-b", "main")
	return dir, gitPath, run
}

// VAULT-R7：只 add 這次寫入的檔案。git 預設把 pathspec 當萬用字元：標題含 [A-Z] 時，
// `git add -- "[A-Z] - 2026-01-01.md"` 也會加入同一個資料夾裡使用者還沒提交的 "B - 2026-01-01.md"。
func TestGitCommitTreatsPathsLiterally(t *testing.T) {
	dir, gitPath, run := realGit(t)
	files := map[string]string{
		"B - 2026-01-01.md": "使用者的草稿", // 未提交，不能被加入
		"x.md":              "另一個",
		"a.md":              "第三個",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ours := []string{Segment("[A-Z] - 2026-01-01.md"), Segment("*.md"), Segment(":(glob)?.md")}
	for _, name := range ours {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("note"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]string{"PATH": os.Getenv("PATH")}
	g := &Git{Runner: toolexec.New(vars), Path: gitPath, Vars: vars, Home: t.TempDir()}
	if _, err := g.Commit(context.Background(), dir, ours, "notesteme: t", "N <n@example.com>"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	committed := strings.Split(strings.TrimRight(run("show", "--name-only", "-z", "--format=", "HEAD"), "\x00"), "\x00")
	slices.Sort(committed)
	want := slices.Clone(ours)
	slices.Sort(want)
	if !slices.Equal(committed, want) {
		t.Fatalf("commit 的檔案 = %q，預期只有 %q", committed, want)
	}
}
