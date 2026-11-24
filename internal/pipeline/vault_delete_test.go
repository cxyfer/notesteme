package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/vault"
)

// gitDeleteRig 發布到 git vault 之後回傳 clone 的目錄與這次的輸出紀錄（筆記在前）。
func gitDeleteRig(t *testing.T) (*gitRig, string, []store.Output) {
	t.Helper()
	r := newGitRig(t, nil)
	if _, err := r.publish(gitOut()); err != nil {
		t.Fatal(err)
	}
	recs, err := r.st.Read().Outputs(context.Background(), r.it.ID)
	if err != nil || len(recs) != 2 {
		t.Fatalf("outputs = %+v，%v", recs, err)
	}
	slices.SortFunc(recs, func(a, b store.Output) int { return strings.Compare(a.Kind, b.Kind) }) // note 在 subtitle 前
	if recs[0].Kind != store.OutputKindNote {
		t.Fatalf("排序 = %+v", recs)
	}
	r.g.calls, r.g.staged = nil, nil
	return r, filepath.Join(r.dir, "vault-repo", "remote"), recs
}

func (r *gitRig) itemNow(t *testing.T) store.Item {
	t.Helper()
	return r.it
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// VAULT-R12、DEL-R9：先 pull，再只刪 hash 相同的檔案，以一個 commit 提交並推送；改過的檔案保留。
func TestGitVaultDeleteCommitsUntouchedFiles(t *testing.T) {
	r, clone, recs := gitDeleteRig(t)
	note, srt := recs[0].Path, recs[1].Path
	if err := os.WriteFile(filepath.Join(clone, srt), []byte("edited by me"), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, err := r.p.deleteOutputs(context.Background(), r.itemNow(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != (KeptOutput{Path: srt, Reason: KeptModified}) {
		t.Fatalf("kept = %+v", kept)
	}
	if pathExists(filepath.Join(clone, note)) || !pathExists(filepath.Join(clone, srt)) {
		t.Error("只能刪除沒改過的筆記")
	}
	if got := r.g.subs(); got != "rev-parse,pull,ls-files,rm,commit,rev-parse,rev-parse,rev-parse,push" {
		t.Errorf("呼叫順序 = %s", got)
	}
	if len(r.g.commits) != 2 || !slices.Equal(r.g.commits[1], []string{note}) || r.g.remote != 2 {
		t.Errorf("commit = %v，遠端 = %d", r.g.commits, r.g.remote)
	}
	var commitArgs []string
	for _, a := range r.g.calls {
		if s, _ := gitSub(a); s == "commit" {
			commitArgs = a
		}
	}
	if !slices.Contains(commitArgs, "notesteme: 刪除 Designing a Cache") || !slices.Contains(commitArgs, "user.name=Me") {
		t.Errorf("commit 的參數 = %v", commitArgs)
	}
	// 再刪一次：筆記已經不存在，沒有 commit 也沒有 push。
	r.g.calls = nil
	if kept, err := r.p.deleteOutputs(context.Background(), r.it); err != nil || len(kept) != 1 {
		t.Fatalf("第二次 = %+v，%v", kept, err)
	}
	if got := r.g.subs(); strings.Contains(got, "commit") || strings.Contains(got, "push") {
		t.Errorf("沒有東西可刪時不應 commit 或 push：%s", got)
	}
}

// VAULT-R9 的分類：同步失敗時 clone 中什麼都沒動；推送失敗時刪除留在本機 commit，下一次發布時一併推送。
func TestGitVaultDeleteFailures(t *testing.T) {
	cases := []struct {
		name, pull, push, reason string
		deleted                  bool // clone 中的檔案是否已刪除
	}{
		{"pull 認證失敗", "fatal: Authentication failed for 'https://git.example.com/me/vault.git'", "", KeptGitAuth, false},
		{"pull 無法快轉", "fatal: Not possible to fast-forward, aborting.", "", KeptGitRejected, false},
		{"pull 連不上", "fatal: unable to access 'https://git.example.com/me/vault.git/': Could not resolve host: git.example.com", "", KeptGitUnreachable, false},
		{"push 被拒", "", "To https://x\n ! [rejected]        HEAD -> master (non-fast-forward)", KeptGitRejected, true},
		{"push 連不上", "", "fatal: unable to access 'https://git.example.com/me/vault.git/': Could not resolve host: git.example.com", KeptGitUnreachable, true},
		{"push 其他錯誤", "", "fatal: something odd", KeptGitFailed, true},
	}
	for _, c := range cases {
		r, clone, recs := gitDeleteRig(t)
		r.g.pullErr = c.pull
		if c.push != "" {
			r.g.pushErrs = []string{c.push}
		}
		kept, err := r.p.deleteOutputs(context.Background(), r.it)
		if err != nil {
			t.Fatalf("%s：%v", c.name, err)
		}
		if len(kept) != 2 || kept[0].Reason != c.reason || kept[1].Reason != c.reason {
			t.Errorf("%s：kept = %+v", c.name, kept)
		}
		if got := pathExists(filepath.Join(clone, recs[0].Path)); got == c.deleted {
			t.Errorf("%s：筆記存在 = %v", c.name, got)
		}
		if strings.Contains(strings.Join(slices.Concat(r.g.calls...), " "), gitToken) {
			t.Errorf("%s：argv 含有 token", c.name)
		}
		if !c.deleted {
			continue
		}
		// 推送失敗：commit 留在本機；下一次發布沿用它，只再 push 一次（VAULT-R9）。
		r.g.calls = nil
		if _, err := r.publish(gitOut()); err != nil {
			t.Fatalf("%s：重新發布 %v", c.name, err)
		}
		if got := r.g.subs(); strings.Count(got, "push") != 1 {
			t.Errorf("%s：重新發布的呼叫 = %s", c.name, got)
		}
	}
}

// 找不到 git、vault 被別的發布佔住到期限：全部保留，不動 clone。
func TestGitVaultDeleteWithoutGit(t *testing.T) {
	r, clone, recs := gitDeleteRig(t)
	r.p.env = map[string]string{"PATH": t.TempDir()}
	kept, _ := r.p.deleteOutputs(context.Background(), r.it)
	if len(kept) != 2 || kept[0].Reason != KeptGitFailed || !pathExists(filepath.Join(clone, recs[0].Path)) {
		t.Errorf("沒有 git：%+v", kept)
	}
	if gitKeepReason(errors.New("x")) != KeptGitFailed || gitKeepReason(&vault.GitError{Kind: vault.KindAuth}) != KeptGitAuth {
		t.Error("gitKeepReason")
	}
}
