package pipeline

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// 永久刪除要等這部影音「所有」任務結束，不論資料庫中的狀態：任務在資料庫中已經是 cancelled
// （移到垃圾桶時取消的），但 worker 可能還在執行它，Cancel 逾時之後仍沒結束。
// Worker.Cancel 會略過不在執行中的 ID，所以全部交給它沒有壞處。
func TestPurgeWaitsForTasksAlreadyCancelledInDB(t *testing.T) {
	c := &purgeCanceller{}
	r := newRig(t)
	r.p.SetTaskCanceller(c)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	// 移到垃圾桶時的 canceller 沒有結束它（模擬等待逾時）：任務在資料庫中已是 cancelled，但 worker 仍在執行。
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{Canceller: &fakeCanceller{err: context.DeadlineExceeded}}); err != nil {
		t.Fatal(err)
	}
	if tk, _ := r.st.Read().Task(ctx, claimed[0].ID); tk.State != store.TaskCancelled {
		t.Fatalf("任務 = %+v，預期 cancelled", tk)
	}

	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.ids, []string{claimed[0].ID}) {
		t.Errorf("交給 canceller 的任務 = %v，預期 [%s]（資料庫中已是 cancelled 的也要）", c.ids, claimed[0].ID)
	}
}

// 移到垃圾桶沒有指定 canceller 時，用 SetTaskCanceller 設定的那個：呼叫端忘了傳，
// 執行中的任務就不會被結束，媒體快取也會在子程序還在寫的時候被刪掉。
func TestTrashFallsBackToConfiguredCanceller(t *testing.T) {
	c := &purgeCanceller{}
	r := newRig(t)
	r.p.SetTaskCanceller(c)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.ids, []string{claimed[0].ID}) {
		t.Errorf("交給 canceller 的任務 = %v，預期 [%s]", c.ids, claimed[0].ID)
	}

	// 明確指定的 canceller 優先。
	other := &purgeCanceller{}
	c.ids = nil
	it2 := r.ingest("https://www.youtube.com/watch?v=def456xxxxx", nil, "")
	if _, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Trash(ctx, it2.ID, TrashOptions{Canceller: other}); err != nil {
		t.Fatal(err)
	}
	if len(other.ids) != 1 || len(c.ids) != 0 {
		t.Errorf("指定的 canceller 收到 %v，設定的 canceller 收到 %v", other.ids, c.ids)
	}
}

// 不在垃圾桶中的影音：永久刪除被拒絕，它的任務一個都不能被取消，也不通知 canceller。
func TestPurgeOutsideTrashDoesNotCancelTasks(t *testing.T) {
	c := &purgeCanceller{}
	r := newRig(t)
	r.p.SetTaskCanceller(c)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")

	if err := r.p.Purge(ctx, it.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v，預期 ErrNotFound", err)
	}
	tk, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageMetadata).ID)
	if err != nil || tk.State != store.TaskPending {
		t.Errorf("任務 = %+v, %v，預期仍是 pending", tk, err)
	}
	if len(c.ids) != 0 {
		t.Errorf("不該通知 canceller：%v", c.ids)
	}
}
