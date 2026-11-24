package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/vault"
)

// fileRemoteRunner 讓產品的 git 可以用本機的 bare repo 當遠端：VAULT-R8 的 protocol.allow=never 會擋掉本機路徑，
// 測試在產品的 -c 之後補上 protocol.file.allow=always（後面的設定優先）。beforePush 在第一次 push 之前執行一次。
type fileRemoteRunner struct {
	inner      toolexec.Runner
	beforePush func()
	pushes     int
}

func (r *fileRemoteRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	i := 0
	for i+1 < len(c.Args) && c.Args[i] == "-c" {
		i += 2
	}
	args := append(append(append([]string{}, c.Args[:i]...), "-c", "protocol.file.allow=always"), c.Args[i:]...)
	if i < len(c.Args) && c.Args[i] == "push" {
		if r.pushes == 0 && r.beforePush != nil {
			r.beforePush()
		}
		r.pushes++
	}
	c.Args = args
	return r.inner.Run(ctx, c)
}

// realGitVault 是真的 git 與本機 bare repo：other 是另一個 clone（模擬使用者在別的地方修改 vault）。
type realGitVault struct {
	*rig
	it     store.Item
	runner *fileRemoteRunner
	bare   string
	other  string
	git    func(dir string, args ...string) string
}

func newRealGitVault(t *testing.T) *realGitVault {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("沒有 git")
	}
	home := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.Command(gitPath, args...)
		c.Dir = dir
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=Other", "GIT_AUTHOR_EMAIL=o@x", "GIT_COMMITTER_NAME=Other", "GIT_COMMITTER_EMAIL=o@x"}
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	base := t.TempDir()
	bare := filepath.Join(base, "vault.git")
	git(base, "init", "-q", "--bare", "-b", "master", bare)
	other := filepath.Join(base, "other")
	git(base, "clone", "-q", bare, other)
	git(other, "checkout", "-q", "-b", "master")
	if err := os.WriteFile(filepath.Join(other, "README.md"), []byte("my vault\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(other, "add", "README.md")
	git(other, "commit", "-q", "-m", "init")
	git(other, "push", "-q", "origin", "master")

	bin := filepath.Dir(gitPath)
	vars := map[string]string{"PATH": bin}
	runner := &fileRemoteRunner{inner: toolexec.New(vars)}
	r := newRig(t, func(d *Deps) {
		// Author 留空：沒有 commit 身分（GIT_CONFIG_GLOBAL=/dev/null、HOME 是空的暫存目錄）時也要能 commit。
		d.Vaults = map[string]vault.Def{"remote": {ID: "remote", Adapter: vault.AdapterGit, Remote: bare, Branch: "master", Base: "Video Notes"}}
		d.Runner = runner
		d.Env = map[string]string{"PATH": bin, "LANG": "C", "NOTESTEME_MASTER_KEY": "mk"}
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
	g := &realGitVault{rig: r, it: it, runner: runner, bare: bare, other: other, git: git}
	eff, err := BuildEffective(map[string]any{"outputs": []any{gitOut()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.runPublish(context.Background(), it, eff); err != nil {
		t.Fatal(err)
	}
	runner.pushes = 0
	git(other, "pull", "-q", "--ff-only", "origin", "master")
	return g
}

// 遠端（bare repo）中追蹤的檔案。
func (g *realGitVault) remoteFiles() string {
	return strings.TrimSpace(g.git(g.bare, "ls-tree", "-r", "--name-only", "master"))
}

const (
	realNote = "Video Notes/YouTube/Designing a Cache - 2025-08-09.md"
	realSRT  = "Video Notes/YouTube/_Assets/Designing a Cache/Designing a Cache.srt"
)

// VAULT-R12：真的 git。沒改過的檔案以一個 commit 刪除並推送；遠端被使用者改過的檔案 pull 之後 hash 不同而保留；
// 沒有 commit 身分（author 留空、沒有全域設定）也能 commit。
func TestRealGitVaultDelete(t *testing.T) {
	g := newRealGitVault(t)
	before := strings.Count(g.git(g.bare, "log", "--oneline", "master"), "\n")
	// 使用者在別的地方改了字幕並推送：刪除前的 pull 會帶進來。
	if err := os.WriteFile(filepath.Join(g.other, realSRT), []byte("my edits\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g.git(g.other, "commit", "-q", "-am", "edit srt")
	g.git(g.other, "push", "-q", "origin", "master")

	kept, err := g.p.deleteOutputs(context.Background(), g.it)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != (KeptOutput{Path: realSRT, Reason: KeptModified}) {
		t.Fatalf("kept = %+v", kept)
	}
	if got := g.remoteFiles(); got != "README.md\n"+realSRT {
		t.Errorf("遠端的檔案 = %q", got)
	}
	// author 與 committer 都要是設定的（預設的）身分，不能是 git 從主機自動猜的。
	log := g.git(g.bare, "log", "--format=%an <%ae>|%cn <%ce>|%s", "-1", "master")
	if strings.TrimSpace(log) != "Notesteme <notesteme@localhost>|Notesteme <notesteme@localhost>|notesteme: 刪除 Designing a Cache" {
		t.Errorf("最後一個 commit = %q", log)
	}
	if n := strings.Count(g.git(g.bare, "log", "--oneline", "master"), "\n"); n != before+2 {
		t.Errorf("commit 數 = %d，預期 %d（使用者一個、刪除一個）", n, before+2)
	}
}

// VAULT-R9：推送時遠端剛好多了一個 commit（non-fast-forward）：保留並回報 git_rejected，刪除留在本機的 commit，
// clone 不被 reset；遠端仍有這些檔案。
func TestRealGitVaultDeleteRejected(t *testing.T) {
	g := newRealGitVault(t)
	g.runner.beforePush = func() {
		if err := os.WriteFile(filepath.Join(g.other, "race.md"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		g.git(g.other, "add", "race.md")
		g.git(g.other, "commit", "-q", "-m", "race")
		g.git(g.other, "push", "-q", "origin", "master")
	}
	kept, err := g.p.deleteOutputs(context.Background(), g.it)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].Reason != KeptGitRejected || kept[1].Reason != KeptGitRejected {
		t.Fatalf("kept = %+v", kept)
	}
	if got := g.remoteFiles(); !strings.Contains(got, realNote) || !strings.Contains(got, realSRT) {
		t.Errorf("遠端應仍有檔案：%q", got)
	}
	clone := filepath.Join(g.dir, "vault-repo", "remote")
	if strings.TrimSpace(g.git(clone, "log", "--format=%s", "-1")) != "notesteme: 刪除 Designing a Cache" {
		t.Error("刪除應留在本機的 commit")
	}
}
