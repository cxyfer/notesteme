package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// 重跑讀取影音之後、寫入之前，影音被移到垃圾桶：重跑要在交易內重新檢查，回傳 *InTrashError，
// 不新增步驟，也不留下 pending 任務（否則垃圾桶中的影音會被 worker 處理）。
func TestRerunLosesRaceWithTrash(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	r.p.beforeRerunTx = func() {
		if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
			t.Error(err)
		}
	}
	_, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata})
	var inTrash *InTrashError
	if !errors.As(err, &inTrash) {
		t.Fatalf("Rerun = %v，預期 *InTrashError", err)
	}
	runs, _ := r.st.Read().StageRunsByItem(ctx, it.ID)
	if len(runs) != len(Stages) {
		t.Errorf("步驟有 %d 筆，預期沒有新增（%d）", len(runs), len(Stages))
	}
	if q, err := r.st.Read().QueueTasks(ctx, queue.ExecutorBuiltin, queue.AgingInterval); err != nil || len(q) != 0 {
		t.Errorf("垃圾桶中的影音不該有排隊的任務：%+v, %v", q, err)
	}
	var pending int
	for _, run := range runs {
		if run.Status == store.StatusPending {
			pending++
		}
	}
	if pending != 0 {
		t.Errorf("有 %d 個步驟停在 pending", pending)
	}
}

// 重跑讀取影音之後，影音被永久刪除：回傳 store.ErrNotFound。
func TestRerunLosesRaceWithPurge(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	// 影音在垃圾桶中時 Rerun 會在讀取時就失敗；先還原，讓它通過讀取，再在 hook 中移到垃圾桶並永久刪除。
	if _, err := r.p.Restore(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	r.p.beforeRerunTx = func() {
		if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
			t.Error(err)
		}
		if err := r.p.Purge(ctx, it.ID); err != nil {
			t.Error(err)
		}
	}
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Rerun = %v，預期 store.ErrNotFound", err)
	}
}

// 已領取的任務，影音在這之後才被標記刪除（取消之前的空隙）：Handle 一開始就檢查，不執行步驟，
// 並結束任務、把步驟改成 skipped（原因 cancelled），不留下 leased 或 running。
func TestHandleSkipsAndCancelsTaskOfTrashedItem(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	// 直接在資料庫標記刪除、不取消任務。
	if _, _, err := r.st.Write().TrashItem(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Handle(ctx, claimed[0]); err != nil {
		t.Fatalf("Handle = %v", err)
	}
	if r.yt.probes != 0 {
		t.Errorf("垃圾桶中的影音不該執行步驟，Probe 被呼叫 %d 次", r.yt.probes)
	}
	run := r.stage(it, StageMetadata)
	if run.Status != store.StatusSkipped || run.BlockReason != "cancelled" {
		t.Errorf("步驟 = %+v，預期 skipped（cancelled）", run)
	}
	if tk, err := r.st.Read().Task(ctx, claimed[0].ID); err != nil || tk.State != store.TaskCancelled {
		t.Errorf("任務 = %+v, %v，預期 cancelled", tk, err)
	}
}

// 影音已經被永久刪除（步驟與任務隨之刪除）：Handle 安靜地結束，不是錯誤。
func TestHandleQuietWhenItemWasPurged(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Handle(ctx, claimed[0]); err != nil {
		t.Fatalf("Handle = %v，影音已永久刪除不是錯誤", err)
	}
	if r.yt.probes != 0 {
		t.Errorf("Probe 被呼叫 %d 次", r.yt.probes)
	}
}

// 移到垃圾桶之後還在等待任務結束時，同一部影音的還原要等善後做完：
// 否則還原、重試後的新任務可能已經在用 media/<id>，接著才被刪掉。
func TestRestoreWaitsForTrashCleanup(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	writeMedia(t, r.dir, it.ID)
	if _, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5); err != nil {
		t.Fatal(err)
	}
	fc := &fakeCanceller{}
	fc.check = func() {
		// Trash 正在等任務結束：還原要被擋住，直到逾時，影音仍在垃圾桶中。
		short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if _, err := r.p.Restore(short, it.ID); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("善後完成前的還原 = %v，預期等到逾時", err)
		}
		if cur, _ := r.st.Read().ItemByID(ctx, it.ID); cur.DeletedAt == nil {
			t.Error("善後完成前不該被還原")
		}
		// 別部影音不受影響。
		other, cancelOther := context.WithTimeout(ctx, time.Second)
		defer cancelOther()
		if _, err := r.p.Restore(other, "0192f3a0-0000-7000-8000-0000000000aa"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("別部影音的還原 = %v，預期不被擋住、回 ErrNotFound", err)
		}
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{Canceller: fc}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Restore(ctx, it.ID); err != nil {
		t.Fatalf("善後完成後的還原：%v", err)
	}
	if n := len(r.p.trashLocks.m); n != 0 {
		t.Errorf("鎖用完應該移除，剩下 %d 個", n)
	}
}
