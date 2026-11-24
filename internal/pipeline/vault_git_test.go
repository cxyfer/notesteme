package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/vault"
)

// gitScript 是腳本化的 git 與 hook：記錄每次呼叫，維持最小的 clone 狀態（HEAD、遠端追蹤分支、遠端的 commit 數）。
type gitScript struct {
	calls      [][]string
	envs       [][]string // 每次 git 呼叫的環境
	hookCalls  []toolexec.Cmd
	head       string
	origin     string
	remote     int
	staged     []string
	commits    [][]string // 每個 commit 的檔案
	cloneErr   string     // clone 的 stderr（非空時失敗）
	pullErr    string     // pull 的 stderr（非空時失敗）
	pushErrs   []string   // 依序的 push 失敗（用完後成功）
	hookResult toolexec.Result
	hookErr    error
}

func gitSub(args []string) (string, []string) {
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	return args[0], args[1:]
}

func (g *gitScript) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if filepath.Base(c.Path) != "git" {
		g.hookCalls = append(g.hookCalls, c)
		return g.hookResult, g.hookErr
	}
	g.calls = append(g.calls, c.Args)
	g.envs = append(g.envs, c.Env)
	sub, rest := gitSub(c.Args)
	fail := func(s string) (toolexec.Result, error) { return toolexec.Result{ExitCode: 128, Stderr: []byte(s)}, nil }
	switch sub {
	case "clone":
		if g.cloneErr != "" {
			return fail(g.cloneErr)
		}
		if err := os.MkdirAll(filepath.Join(c.Dir, rest[len(rest)-1], ".git"), 0o755); err != nil {
			return toolexec.Result{}, err
		}
	case "rev-parse":
		ref := g.head
		if strings.HasPrefix(rest[len(rest)-1], "refs/remotes/") {
			ref = g.origin
		}
		if ref == "" {
			return toolexec.Result{ExitCode: 1}, nil
		}
		return toolexec.Result{Stdout: []byte(ref + "\n")}, nil
	case "pull":
		if g.pullErr != "" {
			return fail(g.pullErr)
		}
	case "add":
		g.staged = append(g.staged, rest[1:]...)
	case "ls-files":
		// 腳本化：-- 之後的路徑都視為有追蹤。
		i := slices.Index(rest, "--")
		return toolexec.Result{Stdout: []byte(strings.Join(rest[i+1:], "\x00") + "\x00")}, nil
	case "rm":
		i := slices.Index(rest, "--")
		g.staged = append(g.staged, rest[i+1:]...)
	case "commit":
		g.commits = append(g.commits, g.staged)
		g.staged, g.head = nil, "c"+string(rune('0'+len(g.commits)))
	case "push":
		if len(g.pushErrs) > 0 {
			e := g.pushErrs[0]
			g.pushErrs = g.pushErrs[1:]
			return fail(e)
		}
		g.origin, g.remote = g.head, len(g.commits)
	}
	return toolexec.Result{}, nil
}

func (g *gitScript) subs() string {
	var out []string
	for _, a := range g.calls {
		s, _ := gitSub(a)
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

type gitRig struct {
	*rig
	it  store.Item
	g   *gitScript
	bin string
}

const gitToken = "vault-token-123"

func newGitRig(t *testing.T, hooks map[string]vault.Hook) *gitRig {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"git", "hook"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g := &gitScript{}
	r := newRig(t, func(d *Deps) {
		d.Vaults = map[string]vault.Def{
			"remote": {ID: "remote", Adapter: vault.AdapterGit, Remote: "https://git.example.com/me/vault.git", Branch: "master", Author: "Me <me@x>", Base: "Video Notes"},
			"main":   {ID: "main", Adapter: vault.AdapterFS, Path: filepath.Join(t.TempDir(), "v"), Base: "Video Notes"},
		}
		d.Hooks = hooks
		d.Runner = g
		d.Env = map[string]string{"PATH": bin, vault.TokenEnvVar("remote"): gitToken, "LANG": "C", "NOTESTEME_MASTER_KEY": "mk"}
	})
	pub := time.Date(2025, 8, 9, 10, 0, 0, 0, time.UTC)
	r.yt.info = youtube.Info{ID: "abc123xxxxx", Title: "Designing a Cache", Language: "en", PublishedAt: &pub}
	it := r.ingest("https://www.youtube.com/watch?v=abc123xxxxx", nil, StageMetadata)
	r.run()
	it, _ = r.st.Read().ItemByID(context.Background(), it.ID)
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "Caches trade memory for latency.")
	if err := r.p.SeedNote(context.Background(), it.ID, "note\n"); err != nil {
		t.Fatal(err)
	}
	return &gitRig{rig: r, it: it, g: g, bin: bin}
}

func (r *gitRig) publish(outs ...map[string]any) (result, error) {
	r.t.Helper()
	list := make([]any, len(outs))
	for i, o := range outs {
		list[i] = o
	}
	eff, err := BuildEffective(map[string]any{"outputs": list})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.p.runPublish(context.Background(), r.it, eff)
}

func gitOut(extra ...any) map[string]any {
	m := map[string]any{"target": "vault", "vault": "remote"}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i].(string)] = extra[i+1]
	}
	return m
}

// VAULT-R7、R8：clone → 寫入 clone → 只 add 這次寫入的檔案 → commit → push；沒有變更時不 commit 也不 push。
func TestGitVaultPublishCommitsOnlyChangedFiles(t *testing.T) {
	r := newGitRig(t, nil)
	clone := filepath.Join(r.dir, "vault-repo", "remote")
	// clone 中使用者自己的草稿：不能被加入。
	res, err := r.publish(gitOut())
	if err != nil || res.state.Status != store.StatusDone {
		t.Fatalf("publish = %+v，%v", res, err)
	}
	if got := r.g.subs(); got != "clone,add,commit,rev-parse,rev-parse,rev-parse,push,rev-parse" {
		t.Errorf("呼叫 = %s", got)
	}
	if len(r.g.commits) != 1 || !slices.Equal(r.g.commits[0], []string{vaultNote, vaultSRT}) || r.g.remote != 1 {
		t.Errorf("commit = %q，remote = %d", r.g.commits, r.g.remote)
	}
	if _, err := os.Stat(filepath.Join(clone, filepath.FromSlash(vaultNote))); err != nil {
		t.Errorf("筆記沒有寫進 clone：%v", err)
	}
	if o := recordFor(t, r.rig, r.it, vaultNote); o.GitCommit != "c1" || o.Vault != "remote" {
		t.Errorf("紀錄 = %+v", o)
	}
	for _, a := range r.g.calls {
		if !slices.Contains(a, "core.hooksPath=/dev/null") || !slices.Contains(a, "credential.helper=") {
			t.Errorf("%q 沒有安全設定", a)
		}
		for _, s := range a {
			if strings.Contains(s, gitToken) {
				t.Errorf("argv 含有 token：%q", a)
			}
		}
	}
	// askpass 只把 token 交給設定的 remote 主機（VAULT V15）。
	for _, env := range r.g.envs {
		if !slices.Contains(env, "NOTESTEME_GIT_HOST=git.example.com") {
			t.Errorf("git 的環境沒有 remote 主機：%q", env)
		}
	}
	// 草稿與第二次發布：沒有變更，不 add、不 commit、不 push。
	_ = os.MkdirAll(filepath.Join(clone, "Inbox"), 0o755)
	_ = os.WriteFile(filepath.Join(clone, "Inbox", "draft.md"), []byte("mine"), 0o644)
	r.g.calls = nil
	if _, err := r.publish(gitOut()); err != nil {
		t.Fatal(err)
	}
	if got := r.g.subs(); strings.Contains(got, "add") || strings.Contains(got, "commit") || strings.Contains(got, "push") || !strings.HasPrefix(got, "rev-parse,pull") {
		t.Errorf("沒有變更時的呼叫 = %s", got)
	}
	if o := recordFor(t, r.rig, r.it, vaultNote); o.GitCommit != "c1" || o.State != store.OutputStateUnchanged {
		t.Errorf("沒有變更時的紀錄 = %+v", o)
	}
	// 暫存目錄（HOME、askpass）用完就刪除。
	if entries, _ := os.ReadDir(filepath.Join(r.dir, "tmp")); len(entries) != 0 {
		t.Errorf("暫存目錄沒有刪除：%v", entries)
	}
}

// VAULT-R9：網路錯誤是 retryable；重試時沿用已建立的本機 commit，只再 push 一次。
func TestGitVaultRetryPushesExistingCommit(t *testing.T) {
	r := newGitRig(t, nil)
	r.g.pushErrs = []string{"fatal: unable to access 'https://git.example.com/me/vault.git/': Could not resolve host: git.example.com"}
	_, err := r.publish(gitOut())
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassRetryable {
		t.Fatalf("err = %v", err)
	}
	r.g.calls = nil
	if res, err := r.publish(gitOut()); err != nil || res.state.Status != store.StatusDone {
		t.Fatalf("重試 = %+v，%v", res, err)
	}
	if got := r.g.subs(); strings.Contains(got, "commit") || strings.Contains(got, "pull") || strings.Count(got, "push") != 1 {
		t.Errorf("重試的呼叫 = %s", got)
	}
	if len(r.g.commits) != 1 || r.g.remote != 1 {
		t.Errorf("commit 數 = %d，遠端 = %d", len(r.g.commits), r.g.remote)
	}
}

// VAULT-R9：non-fast-forward 與認證失敗是 needs_action，訊息與動作依規格。
func TestGitVaultBlockedErrors(t *testing.T) {
	r := newGitRig(t, nil)
	r.g.pushErrs = []string{"To https://x\n ! [rejected]        HEAD -> master (non-fast-forward)"}
	_, err := r.publish(gitOut())
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassNeedsAction ||
		ec.Message != "vault 的遠端分支 master 有本機沒有的提交，Notesteme 不會自動合併。請先同步 vault repo 再重新發布。" ||
		!slices.Equal(ec.Actions, []string{"重新發布"}) {
		t.Fatalf("non-fast-forward：%v", err)
	}
	r2 := newGitRig(t, nil)
	r2.g.cloneErr = "fatal: Authentication failed for 'https://git.example.com/me/vault.git'"
	_, err = r2.publish(gitOut())
	if !errors.As(err, &ec) || ec.Class != errclass.ClassNeedsAction ||
		ec.Message != "無法登入 vault 的遠端（認證失敗）。請更新 vault remote 的憑證。" ||
		!slices.Equal(ec.Actions, []string{"更新憑證", "重新發布"}) {
		t.Fatalf("認證失敗：%v", err)
	}
	if strings.Contains(err.Error(), gitToken) {
		t.Error("錯誤含有 token")
	}
	r2.g.cloneErr = "fatal: something odd"
	if _, err = r2.publish(gitOut()); !errors.As(err, &ec) || ec.Class != errclass.ClassPermanent {
		t.Errorf("其他錯誤：%v", err)
	}
	if err := gitError(vault.Def{ID: "x"}, errors.New("disk")); !errors.As(err, &ec) || ec.Class != errclass.ClassPermanent {
		t.Errorf("非 git 錯誤：%v", err)
	}
}

func TestGitVaultNeedsGitBinary(t *testing.T) {
	r := newGitRig(t, nil)
	_ = os.Remove(filepath.Join(r.bin, "git"))
	_, err := r.publish(gitOut())
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassPermanent || !strings.Contains(ec.Message, "找不到 git") {
		t.Errorf("err = %v", err)
	}
	r.p.runner = nil
	if _, err := r.publish(gitOut()); err == nil {
		t.Error("沒有 runner 應失敗")
	}
}

var notifyHook = vault.Hook{ID: "notify", Argv: []string{"hook", "{{note_path}}", "{{title}}", "{{commit}}"}, Timeout: time.Second, EnvAllowlist: []string{"LANG", "NOTESTEME_MASTER_KEY"}}

// VAULT-R11：寫入（與 push）成功後執行 hook；失敗只提醒，步驟仍是 done，動作是重新發布。
func TestVaultHooksRunAfterPublish(t *testing.T) {
	r := newGitRig(t, map[string]vault.Hook{"notify": notifyHook})
	res, err := r.publish(gitOut("hooks", []any{"notify"}))
	if err != nil || res.state.Status != store.StatusDone || res.state.Message != "" || len(r.g.hookCalls) != 1 {
		t.Fatalf("publish = %+v，%v，hook %d 次", res, err, len(r.g.hookCalls))
	}
	c := r.g.hookCalls[0]
	clone := filepath.Join(r.dir, "vault-repo", "remote")
	if !slices.Equal(c.Args, []string{filepath.Join(clone, filepath.FromSlash(vaultNote)), "Designing a Cache", "c1"}) {
		t.Errorf("args = %q", c.Args)
	}
	if !slices.Equal(c.Env, []string{"LANG=C", "NOTESTEME_ITEM_ID=" + r.it.ID}) {
		t.Errorf("env = %q", c.Env)
	}
	if !strings.HasPrefix(c.Dir, filepath.Join(r.dir, "tmp")) {
		t.Errorf("工作目錄 = %s", c.Dir)
	}

	r.g.hookResult = toolexec.Result{ExitCode: 3}
	res, err = r.publish(gitOut("hooks", []any{"notify"}))
	if err != nil || res.state.Status != store.StatusDone || res.state.Message != "hook notify 失敗（結束碼 3）。檔案已經寫入。" ||
		!slices.Equal(res.state.Actions, []string{"重新發布"}) {
		t.Fatalf("hook 失敗 = %+v，%v", res.state, err)
	}
	r.g.hookResult, r.g.hookErr = toolexec.Result{}, context.DeadlineExceeded
	if res, _ = r.publish(gitOut("hooks", []any{"notify"})); res.state.Message != "hook notify 逾時（1 秒），已停止。檔案已經寫入。" {
		t.Errorf("逾時 = %q", res.state.Message)
	}
	if _, err := r.publish(gitOut("hooks", []any{"gone"})); err == nil || !strings.Contains(err.Error(), "沒有這個 hook") {
		t.Errorf("不存在的 hook：%v", err)
	}
}

// VAULT-R6、R11：有檔案被擋下時 hook 不執行。
func TestVaultHooksDoNotRunWhenBlocked(t *testing.T) {
	r := newGitRig(t, map[string]vault.Hook{"notify": notifyHook})
	if _, err := r.publish(map[string]any{"target": "vault", "vault": "main"}); err != nil {
		t.Fatal(err)
	}
	root := r.p.vaults["main"].Path
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(vaultNote)), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := r.publish(map[string]any{"target": "vault", "vault": "main", "on_user_change": "skip", "hooks": []any{"notify"}})
	var ec *errclass.Error
	if !errors.As(err, &ec) || ec.Class != errclass.ClassNeedsAction || len(r.g.hookCalls) != 0 {
		t.Fatalf("err = %v，hook %d 次", err, len(r.g.hookCalls))
	}
}
