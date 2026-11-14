package pipeline

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// needsCookie 是需要 YouTube cookie 的 needs_action 錯誤（SRC-R9、members-only）。
func needsCookie() error {
	return &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "yt-dlp", Message: "需要 cookie。", Actions: []string{"上傳 cookie"},
		ResumeOn: "cookie:youtube",
	}
}

// blockedRig 建立一部 metadata 步驟因缺 cookie 而 blocked 的影音。
func blockedRig(t *testing.T) (*rig, store.Item) {
	t.Helper()
	r := newRig(t)
	r.yt.probeErr = needsCookie()
	it := r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked || st.ResumeOn != "cookie:youtube" {
		t.Fatalf("前置條件：metadata = %+v，預期 blocked", st)
	}
	return r, it
}

func TestResumeBlockedRestoresPendingAndCreatesTask(t *testing.T) {
	r, it := blockedRig(t)
	ctx := context.Background()
	old, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageMetadata).ID)
	if err != nil || old.State != store.TaskFailed {
		t.Fatalf("原本的任務 = %+v, %v", old, err)
	}

	n, err := r.p.ResumeBlocked(ctx, "cookie:youtube")
	if err != nil || n != 1 {
		t.Fatalf("ResumeBlocked = %d, %v", n, err)
	}
	st := r.stage(it, StageMetadata)
	if st.Status != store.StatusPending || st.ResumeOn != "" || st.BlockReason != "" || st.Message != "" || len(st.Actions) != 0 {
		t.Errorf("恢復後的步驟 = %+v，預期 pending 且清除 blocked 的說明", st)
	}
	tk, err := r.st.Read().TaskByStageRun(ctx, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.ID == old.ID || tk.State != store.TaskPending || tk.Attempts != 0 || tk.Kind != StageMetadata {
		t.Errorf("新任務 = %+v，原任務 %s", tk, old.ID)
	}

	// 補上 cookie 之後真的能跑完。
	r.yt.mu.Lock()
	r.yt.probeErr = nil
	r.yt.mu.Unlock()
	r.run()
	if got := r.stage(it, StageMetadata); got.Status != store.StatusDone {
		t.Errorf("恢復後 metadata = %+v，預期 done", got)
	}
}

// 新任務沿用原本任務的優先順序（PIPE-R9），不是一律當成手動新增。
func TestResumeBlockedKeepsOriginalPriority(t *testing.T) {
	r := newRig(t)
	r.yt.probeErr = needsCookie()
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: watch, Priority: queue.PriorityBackfill})
	if err != nil {
		t.Fatal(err)
	}
	it := res[0].Item
	r.run()
	if n, err := r.p.ResumeBlocked(context.Background(), "cookie:youtube"); err != nil || n != 1 {
		t.Fatalf("ResumeBlocked = %d, %v", n, err)
	}
	tk, err := r.st.Read().TaskByStageRun(context.Background(), r.stage(it, StageMetadata).ID)
	if err != nil || tk.Priority != queue.PriorityBackfill {
		t.Errorf("新任務 = %+v, %v，預期優先順序 %d", tk, err, queue.PriorityBackfill)
	}
}

func TestResumeBlockedIgnoresOtherTokens(t *testing.T) {
	r, it := blockedRig(t)
	for _, token := range []string{"cookie:bilibili", "", "cookie:youtube2"} {
		if n, err := r.p.ResumeBlocked(context.Background(), token); err != nil || n != 0 {
			t.Errorf("ResumeBlocked(%q) = %d, %v，預期 0", token, n, err)
		}
	}
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked {
		t.Errorf("token 不符不應恢復：%+v", st)
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskPending); n != 0 {
		t.Errorf("pending 任務數 = %d，預期 0", n)
	}
}

func TestResumeBlockedSkipsDeletedItems(t *testing.T) {
	r, it := blockedRig(t)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET deleted_at = 1 WHERE id = ?`, it.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := r.p.ResumeBlocked(context.Background(), "cookie:youtube"); err != nil || n != 0 {
		t.Fatalf("ResumeBlocked = %d, %v，已刪除的影音不應恢復", n, err)
	}
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked {
		t.Errorf("步驟 = %+v，預期仍是 blocked", st)
	}
}

func TestResumeBlockedIsIdempotent(t *testing.T) {
	r, _ := blockedRig(t)
	ctx := context.Background()
	if n, err := r.p.ResumeBlocked(ctx, "cookie:youtube"); err != nil || n != 1 {
		t.Fatalf("第一次 = %d, %v", n, err)
	}
	if n, err := r.p.ResumeBlocked(ctx, "cookie:youtube"); err != nil || n != 0 {
		t.Fatalf("第二次 = %d, %v，預期 0", n, err)
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 1 {
		t.Errorf("pending 任務數 = %d，預期只有 1（不重複建立）", n)
	}
}

// 新建立任務後要喚醒 worker。
func TestResumeBlockedWakesWorker(t *testing.T) {
	woke := 0
	r := newRig(t, func(d *Deps) { d.Wake = func() { woke++ } })
	r.yt.probeErr = needsCookie()
	r.ingest(watch, nil, "")
	r.run()
	before := woke
	if n, err := r.p.ResumeBlocked(context.Background(), "cookie:youtube"); err != nil || n != 1 {
		t.Fatalf("ResumeBlocked = %d, %v", n, err)
	}
	if woke != before+1 {
		t.Errorf("Wake 呼叫 %d 次，預期多一次", woke-before)
	}
}
