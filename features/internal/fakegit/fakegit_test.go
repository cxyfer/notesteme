package fakegit

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const remote = "https://git.example.com/me/vault.git"

func newFake(t *testing.T) (Fake, string) {
	t.Helper()
	root := t.TempDir()
	f := Fake{StateDir: filepath.Join(root, "fake", "git")}
	if err := f.SetRemote(RemoteState{URL: remote, Branch: "master"}); err != nil {
		t.Fatal(err)
	}
	return f, root
}

func run(t *testing.T, f Fake, dir string, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := f.Run(dir, args, &out, &errb)
	return code, out.String() + errb.String()
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyAddedFilesAreCommittedAndPushed(t *testing.T) {
	f, root := newFake(t)
	clone := filepath.Join(root, "vault-repo", "remote")
	hard := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.helper="}
	if code, out := run(t, f, root, append(hard, "clone", "--branch", "master", "--single-branch", remote, clone)...); code != 0 {
		t.Fatalf("clone: %d %s", code, out)
	}
	write(t, filepath.Join(clone, "Video Notes/YouTube/a.md"), "note")
	write(t, filepath.Join(clone, "Inbox/draft.md"), "not ours")
	for _, args := range [][]string{
		{"-C", clone, "pull", "--ff-only"},
		{"-C", clone, "add", "--", "Video Notes/YouTube/a.md"},
		{"-C", clone, "commit", "-m", "notesteme: a", "--author=Notesteme <notesteme@localhost>"},
		{"-C", clone, "push", "origin", "HEAD:master"},
	} {
		if code, out := run(t, f, root, append(hard, args...)...); code != 0 {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	st, _ := f.Remote()
	if len(st.Commits) != 1 || !slices.Equal(st.Commits[0].Files, []string{"Video Notes/YouTube/a.md"}) || st.Pushes != 1 {
		t.Errorf("remote = %+v", st)
	}
	// 沒有東西可以 commit 時失敗，push 不會多出 commit。
	if code, _ := run(t, f, root, "-C", clone, "commit", "-m", "again"); code == 0 {
		t.Error("沒有 staged 的檔案時 commit 應該失敗")
	}
	calls, _ := f.Calls()
	if len(calls) != 6 || !slices.Contains(calls[0].Argv, "core.hooksPath=/dev/null") {
		t.Errorf("calls = %+v", calls)
	}
}

func TestRejectionsAndNetworkErrors(t *testing.T) {
	f, root := newFake(t)
	clone := filepath.Join(root, "c")
	if code, out := run(t, f, root, "clone", remote, clone); code != 0 {
		t.Fatal(out)
	}
	write(t, filepath.Join(clone, "a.md"), "x")
	run(t, f, root, "-C", clone, "add", "--", "a.md")
	run(t, f, root, "-C", clone, "commit", "-m", "m")

	st, _ := f.Remote()
	st.FailNextPush = true
	_ = f.SetRemote(st)
	if code, out := run(t, f, root, "-C", clone, "push", "origin", "HEAD:master"); code != 128 || !strings.Contains(out, "Could not resolve host") {
		t.Errorf("network: %d %s", code, out)
	}
	if code, _ := run(t, f, root, "-C", clone, "push", "origin", "HEAD:master"); code != 0 {
		t.Error("重試應該成功")
	}
	if st, _ := f.Remote(); len(st.Commits) != 1 {
		t.Errorf("重試後 commits = %d，不應重複", len(st.Commits))
	}

	st, _ = f.Remote()
	st.Reject = RejectNonFastForward
	_ = f.SetRemote(st)
	if code, out := run(t, f, root, "-C", clone, "pull", "--ff-only"); code == 0 || !strings.Contains(out, "fast-forward") {
		t.Errorf("non-ff pull: %d %s", code, out)
	}
	st.Reject = RejectBadCredentials
	_ = f.SetRemote(st)
	if code, out := run(t, f, root, "-C", clone, "push", "origin", "HEAD:master"); code != 128 || !strings.Contains(out, "Authentication failed") {
		t.Errorf("bad credentials: %d %s", code, out)
	}
}

func TestAddRejectsPathsOutsideTheRepo(t *testing.T) {
	f, root := newFake(t)
	clone := filepath.Join(root, "c")
	run(t, f, root, "clone", remote, clone)
	if code, _ := run(t, f, root, "-C", clone, "add", "--", "../outside.md"); code == 0 {
		t.Error("repo 外的路徑應該被拒絕")
	}
	if code, _ := run(t, f, root, "clone", "ext::sh -c id", filepath.Join(root, "d")); code == 0 {
		t.Error("不認得的 remote 應該失敗")
	}
}

func TestFirstCloneCreatesAnEmptyRemote(t *testing.T) {
	root := t.TempDir()
	f := Fake{StateDir: filepath.Join(root, "fake", "git")}
	if code, out := run(t, f, root, "clone", "--branch", "master", remote, filepath.Join(root, "c")); code != 0 {
		t.Fatalf("clone: %d %s", code, out)
	}
	if st, _ := f.Remote(); st.URL != remote || st.Branch != "master" {
		t.Errorf("remote = %+v", st)
	}
	g := Fake{StateDir: filepath.Join(t.TempDir(), "git")}
	for _, bad := range []string{"ext::sh -c id", "file:///data/config", "/srv/git/vault.git"} {
		if code, _ := run(t, g, root, "clone", bad, filepath.Join(t.TempDir(), "d")); code == 0 {
			t.Errorf("%q 不應該被自動建立", bad)
		}
	}
	if !allowedRemote("git@github.com:me/vault.git") {
		t.Error("scp 形式的 ssh 網址要允許")
	}
}

// M3-S6 的擴充：clone 接受 --；rev-parse 的遠端追蹤分支在 clone、push 後更新，空的 repo 時失敗；pull 快轉。
func TestTrackingRefAndFastForward(t *testing.T) {
	f, root := newFake(t)
	dir := filepath.Join(root, "clone")
	if code, out := run(t, f, root, "-c", "core.hooksPath=/dev/null", "clone", "--branch", "master", "--single-branch", "--", remote, "clone"); code != 0 {
		t.Fatalf("clone：%d %s", code, out)
	}
	if code, _ := run(t, f, dir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/master"); code != 1 {
		t.Errorf("空的 repo 的追蹤分支應不存在：%d", code)
	}
	write(t, filepath.Join(dir, "a.md"), "a")
	run(t, f, dir, "add", "--", "a.md")
	run(t, f, dir, "commit", "-m", "m")
	if code, out := run(t, f, dir, "rev-parse", "HEAD"); code != 0 || strings.TrimSpace(out) != "c1" {
		t.Errorf("HEAD = %q", out)
	}
	run(t, f, dir, "push", "origin", "HEAD:refs/heads/master")
	if code, out := run(t, f, dir, "rev-parse", "--verify", "refs/remotes/origin/master"); code != 0 || strings.TrimSpace(out) != "c1" {
		t.Errorf("push 後的追蹤分支 = %d %q", code, out)
	}
	// 另一個 clone 推了新的 commit：pull --ff-only 快轉 HEAD 與追蹤分支。
	st, _ := f.Remote()
	st.Commits = append(st.Commits, Commit{ID: "c9", Files: []string{"b.md"}})
	_ = f.SetRemote(st)
	if code, _ := run(t, f, dir, "pull", "--ff-only", "origin", "master"); code != 0 {
		t.Fatal("pull 失敗")
	}
	if _, out := run(t, f, dir, "rev-parse", "HEAD"); strings.TrimSpace(out) != "c9" {
		t.Errorf("快轉後的 HEAD = %q", out)
	}
}
