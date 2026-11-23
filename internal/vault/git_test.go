package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// scriptRunner 是腳本化的假 runner：記錄每次呼叫，依子命令回傳設定好的結果。
type scriptRunner struct {
	calls []toolexec.Cmd
	// reply 依子命令（略過 -c 設定之後的第一個參數）回傳結果；沒有設定時成功、沒有輸出。
	reply map[string]toolexec.Result
	err   map[string]error
}

func subcommand(args []string) (string, []string) {
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

func (r *scriptRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	r.calls = append(r.calls, c)
	sub, rest := subcommand(c.Args)
	key := sub
	if sub == "rev-parse" && slices.ContainsFunc(rest, func(a string) bool { return strings.HasPrefix(a, "refs/remotes/") }) {
		key = "rev-parse-origin"
	}
	return r.reply[key], r.err[key]
}

func (r *scriptRunner) subs() []string {
	var out []string
	for _, c := range r.calls {
		s, _ := subcommand(c.Args)
		out = append(out, s)
	}
	return out
}

func newTestGit(t *testing.T, r *scriptRunner, token string) *Git {
	t.Helper()
	return &Git{Runner: r, Path: "/usr/bin/git", Vars: map[string]string{"PATH": "/bin", "SSH_AUTH_SOCK": "/s", "NOTESTEME_MASTER_KEY": "mk"}, Home: t.TempDir(), Token: token}
}

var gitDef = Def{ID: "remote", Adapter: AdapterGit, Remote: "https://git.example.com/me/vault.git", Branch: "master", Author: "Me <me@example.com>"}

// VAULT-R8：每次呼叫都帶安全設定；環境只有規定的變數；憑證經 askpass 的環境變數，不在 argv 也不在腳本內容。
func TestGitSafetyAndCredentials(t *testing.T) {
	r := &scriptRunner{}
	g := newTestGit(t, r, "vault-token-123")
	dir := filepath.Join(t.TempDir(), "repo")
	if err := g.Sync(context.Background(), gitDef, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Commit(context.Background(), dir, []string{"-rf.md", "a b.md"}, "notesteme: t\nInjected: x", gitDef.Author); err != nil {
		t.Fatal(err)
	}
	if err := g.Push(context.Background(), dir, gitDef.Branch); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.subs(), ","); got != "clone,add,commit,rev-parse,push" {
		t.Fatalf("呼叫 = %s", got)
	}
	for _, c := range r.calls {
		for _, want := range SafeConfig {
			if !slices.Contains(c.Args, want) {
				t.Errorf("%q 沒有 %s", c.Args, want)
			}
		}
		for _, a := range c.Args {
			if strings.Contains(a, "vault-token-123") {
				t.Errorf("argv 含有 token：%q", c.Args)
			}
		}
		env := strings.Join(c.Env, "\n")
		for _, want := range []string{"PATH=/bin", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=" + g.Home, "SSH_AUTH_SOCK=/s", "NOTESTEME_GIT_TOKEN=vault-token-123", "GIT_ASKPASS="} {
			if !strings.Contains(env, want) {
				t.Errorf("環境缺少 %s：%s", want, env)
			}
		}
		if strings.Contains(env, "NOTESTEME_MASTER_KEY") {
			t.Error("git 的環境不能有 master key")
		}
		if c.Timeout != GitTimeout {
			t.Errorf("逾時 = %v", c.Timeout)
		}
	}
	clone := r.calls[0]
	if _, rest := subcommand(clone.Args); !slices.Equal(rest, []string{"--branch", "master", "--single-branch", "--quiet", "--", gitDef.Remote, "repo"}) || clone.Dir != filepath.Dir(dir) {
		t.Errorf("clone = %q in %s", rest, clone.Dir)
	}
	// add 的路徑在 -- 之後（以 - 開頭的檔名不會變成選項）；commit 訊息壓成一行，committer 由 -c 指定。
	if _, rest := subcommand(r.calls[1].Args); !slices.Equal(rest, []string{"--", "-rf.md", "a b.md"}) {
		t.Errorf("add = %q", rest)
	}
	commit := r.calls[2].Args
	if !slices.Contains(commit, "user.name=Me") || !slices.Contains(commit, "user.email=me@example.com") ||
		!slices.Contains(commit, "notesteme: t Injected: x") || !slices.Contains(commit, "--author=Me <me@example.com>") {
		t.Errorf("commit = %q", commit)
	}
	script, err := os.ReadFile(filepath.Join(g.Home, "askpass.sh"))
	if err != nil || strings.Contains(string(script), "vault-token-123") || !strings.Contains(string(script), tokenEnv) {
		t.Errorf("askpass = %q，%v", script, err)
	}
	if _, rest := subcommand(r.calls[4].Args); !slices.Equal(rest, []string{"--quiet", "origin", "HEAD:refs/heads/master"}) {
		t.Errorf("push = %q", rest)
	}
	// 沒有 token 時不設定 askpass。
	r2 := &scriptRunner{}
	if err := newTestGit(t, r2, "").Push(context.Background(), dir, "main"); err != nil {
		t.Fatal(err)
	}
	if env := strings.Join(r2.calls[0].Env, " "); strings.Contains(env, "GIT_ASKPASS") || strings.Contains(env, tokenEnv) {
		t.Errorf("沒有 token 時的環境 = %s", env)
	}
}

func TestGitSyncPullsExistingClone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &scriptRunner{}
	if err := newTestGit(t, r, "").Sync(context.Background(), gitDef, dir); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.subs(), ","); got != "rev-parse,pull" {
		t.Errorf("呼叫 = %s", got)
	}
	// 遠端分支還不存在（空的 repo）時不 pull。
	r = &scriptRunner{reply: map[string]toolexec.Result{"rev-parse-origin": {ExitCode: 1}}}
	if err := newTestGit(t, r, "").Sync(context.Background(), gitDef, dir); err != nil || strings.Join(r.subs(), ",") != "rev-parse" {
		t.Errorf("空 repo：%v %v", r.subs(), err)
	}
	// 不是 git repo 的非空資料夾：不覆蓋。
	other := t.TempDir()
	_ = os.WriteFile(filepath.Join(other, "x"), nil, 0o644)
	if err := newTestGit(t, &scriptRunner{}, "").Sync(context.Background(), gitDef, other); err == nil {
		t.Error("非空、不是 repo 的資料夾應拒絕")
	}
}

// VAULT-R9：依 stderr 分類；錯誤中的 token 遮蔽。
func TestGitErrorClassification(t *testing.T) {
	cases := map[string]ErrorKind{
		"fatal: Authentication failed for 'https://x'":                                KindAuth,
		"git@host: Permission denied (publickey).":                                    KindAuth,
		"fatal: could not read Username for 'https://x': terminal prompts disabled":   KindAuth,
		" ! [rejected]        HEAD -> master (non-fast-forward)":                      KindRejected,
		"fatal: Not possible to fast-forward, aborting.":                              KindRejected,
		"fatal: unable to access 'https://x/': Could not resolve host: x":             KindNetwork,
		"fatal: unable to access 'https://x/': The requested URL returned error: 502": KindNetwork,
		"ssh: connect to host x port 22: Connection refused":                          KindNetwork,
		"fatal: pathspec 'x' did not match any files":                                 KindOther,
	}
	for stderr, want := range cases {
		r := &scriptRunner{reply: map[string]toolexec.Result{"push": {ExitCode: 128, Stderr: []byte(stderr + " tok-9")}}}
		err := newTestGit(t, r, "tok-9").Push(context.Background(), t.TempDir(), "main")
		var ge *GitError
		if !errors.As(err, &ge) || ge.Kind != want {
			t.Errorf("%q：err = %v，預期 kind %d", stderr, err, want)
			continue
		}
		if strings.Contains(ge.Error(), "tok-9") {
			t.Errorf("錯誤含有 token：%v", ge)
		}
	}
	// 逾時是網路類；其他 runner 錯誤原樣回傳。
	r := &scriptRunner{err: map[string]error{"push": context.DeadlineExceeded}}
	var ge *GitError
	if err := newTestGit(t, r, "").Push(context.Background(), t.TempDir(), "main"); !errors.As(err, &ge) || ge.Kind != KindNetwork {
		t.Errorf("逾時：%v", err)
	}
	r = &scriptRunner{err: map[string]error{"push": errors.New("boom")}}
	if err := newTestGit(t, r, "").Push(context.Background(), t.TempDir(), "main"); err == nil || errors.As(err, &ge) {
		t.Errorf("runner 錯誤：%v", err)
	}
}

func TestGitHeadAheadAndCommit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// 沒有變更：不 add 也不 commit。
	r := &scriptRunner{}
	if c, err := newTestGit(t, r, "").Commit(ctx, dir, nil, "m", "A <a@b>"); c != "" || err != nil || len(r.calls) != 0 {
		t.Errorf("沒有變更：%q %v %v", c, err, r.calls)
	}
	// HEAD 與遠端追蹤分支相同：不領先；不同或追蹤分支不存在：領先；沒有 HEAD：不領先。
	same := map[string]toolexec.Result{"rev-parse": {Stdout: []byte("abc\n")}, "rev-parse-origin": {Stdout: []byte("abc\n")}}
	if ahead, err := newTestGit(t, &scriptRunner{reply: same}, "").Ahead(ctx, dir, "main"); ahead || err != nil {
		t.Errorf("相同：%v %v", ahead, err)
	}
	diff := map[string]toolexec.Result{"rev-parse": {Stdout: []byte("abc\n")}, "rev-parse-origin": {Stdout: []byte("old\n")}}
	if ahead, _ := newTestGit(t, &scriptRunner{reply: diff}, "").Ahead(ctx, dir, "main"); !ahead {
		t.Error("不同時應領先")
	}
	none := map[string]toolexec.Result{"rev-parse": {Stdout: []byte("abc\n")}, "rev-parse-origin": {ExitCode: 1}}
	if ahead, _ := newTestGit(t, &scriptRunner{reply: none}, "").Ahead(ctx, dir, "main"); !ahead {
		t.Error("沒有追蹤分支時應領先")
	}
	unborn := map[string]toolexec.Result{"rev-parse": {ExitCode: 1}}
	if ahead, _ := newTestGit(t, &scriptRunner{reply: unborn}, "").Ahead(ctx, dir, "main"); ahead {
		t.Error("沒有 HEAD 時不領先")
	}
	// commit 失敗回傳錯誤。
	bad := &scriptRunner{reply: map[string]toolexec.Result{"commit": {ExitCode: 1, Stdout: []byte("nothing to commit")}}}
	if _, err := newTestGit(t, bad, "").Commit(ctx, dir, []string{"a"}, "m", "bad author"); err == nil {
		t.Error("commit 失敗應回傳錯誤")
	}
	if !slices.Contains(bad.calls[1].Args, "user.name=Notesteme") {
		t.Errorf("解析不了的作者用預設身分：%q", bad.calls[1].Args)
	}
	if TokenEnvVar("my-vault") != "NOTESTEME_VAULT_MY_VAULT_TOKEN" {
		t.Error(TokenEnvVar("my-vault"))
	}
}
