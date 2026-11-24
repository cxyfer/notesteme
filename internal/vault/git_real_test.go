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

// VAULT-R8：askpass 只把 token 交給設定的 remote 主機。git 會跟隨 https 的轉址，或 remote 的
// 子模組、LFS 等可能向其他主機要求憑證；主機不符時回空字串，並在 stderr 說明（不含 token）。
func TestAskpassOnlyAnswersConfiguredHost(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("沒有 sh")
	}
	const token = "vault-token-123"
	g := &Git{Vars: map[string]string{"PATH": os.Getenv("PATH")}, Home: t.TempDir(), Token: token,
		Remote: "https://Git.Example.com:8443/me/vault.git"}
	env, err := g.env()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(g.Home, "askpass.sh")
	ask := func(prompt string) (string, string) {
		t.Helper()
		c := exec.Command(sh, script, prompt)
		c.Env = env
		var stderr strings.Builder
		c.Stderr = &stderr
		out, err := c.Output()
		if err != nil {
			t.Fatalf("askpass %q: %v", prompt, err)
		}
		return strings.TrimRight(string(out), "\n"), stderr.String()
	}
	for _, c := range []struct{ prompt, want string }{
		{"Username for 'https://git.example.com:8443': ", "x-access-token"},
		{"Password for 'https://x-access-token@git.example.com:8443': ", token},
		{"Password for 'https://x-access-token@GIT.example.com:8443': ", token},
	} {
		if got, _ := ask(c.prompt); got != c.want {
			t.Errorf("%q → %q，預期 %q", c.prompt, got, c.want)
		}
	}
	for _, prompt := range []string{
		"Password for 'https://x-access-token@evil.example': ",
		"Password for 'https://x-access-token@git.example.com': ", // 不同的 port
		"Password for 'https://x-access-token@git.example.com.evil.example:8443': ",
		"Password for 'https://git.example.com:8443@evil.example': ",
		"Username for 'https://evil.example': ",
		"Password: ",
	} {
		got, stderr := ask(prompt)
		if got != "" {
			t.Errorf("%q 不能拿到憑證：%q", prompt, got)
		}
		if !strings.Contains(stderr, "拒絕") || strings.Contains(stderr, token) {
			t.Errorf("%q 的 stderr = %q", prompt, stderr)
		}
	}
	// 每次執行 git 都不跟隨轉址。
	if !slices.Contains(SafeConfig, "http.followRedirects=false") {
		t.Errorf("SafeConfig 沒有 http.followRedirects=false：%q", SafeConfig)
	}
}
