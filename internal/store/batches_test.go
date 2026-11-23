package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// 等待 batch 的任務：不被領取、不被租約回收；開著的 batch 的預留不算孤兒預留；
// 移除影音時任務取消，輪詢提前到現在（BATCH-R3、R9、R10，COST-R5）。
func TestBatchParkingAndCancel(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	it, sr := seedItem(t, s, "abc123xxxxx")
	a := newTask(t, s, sr, 3, nil)
	b := newTask(t, s, sr, 3, nil)
	w := s.Write()
	now := clk.Now()
	bt := Batch{ID: "b1", Provider: "anthropic", Model: "m", SubmittedAt: now, DeadlineAt: now.Add(24 * time.Hour),
		Entries: []BatchEntry{{Task: a.ID, Run: sr.ID, Chunk: 1, Reservation: "r1"}, {Task: b.ID, Run: sr.ID, Chunk: 2}}}
	if err := w.InsertBatch(ctx, bt); err != nil {
		t.Fatal(err)
	}
	// a 被領取後放下租約；錯誤的租約權杖回傳 ErrLeaseLost。
	got := claim(t, s, claimOpts("w1", 1))
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("領取 = %+v", got)
	}
	if err := w.ParkLeasedTask(ctx, a.ID, "wrong", "b1"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("錯誤的權杖 = %v", err)
	}
	if err := w.ParkLeasedTask(ctx, a.ID, got[0].LeaseToken, "b1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := w.ParkPendingTask(ctx, b.ID, "b1"); !ok || err != nil {
		t.Fatalf("ParkPendingTask = %v %v", ok, err)
	}
	if ok, _ := w.ParkPendingTask(ctx, b.ID, "b1"); ok {
		t.Error("已經在等待的任務不能再加入")
	}
	if got := claim(t, s, claimOpts("w1", 5)); len(got) != 0 {
		t.Fatalf("等待中的任務不能被領取：%+v", got)
	}
	if cands, err := w.BatchCandidateTasks(ctx, "", 9, 10); err != nil || len(cands) != 0 {
		t.Fatalf("候選 = %+v %v", cands, err)
	}
	for _, id := range []string{a.ID, b.ID} {
		tk, _ := w.Task(ctx, id)
		if tk.State != TaskWaiting || tk.BatchID != "b1" || tk.LeaseOwner != "" {
			t.Fatalf("任務 = %+v", tk)
		}
	}
	// 預留：開著的 batch 的任務不算孤兒。
	if _, err := w.InsertCostRow(ctx, CostRow{ID: "r1", ItemID: it.ID, StageRunID: sr.ID, TaskID: a.ID, Stage: "correct", State: CostReserved, USD: 0.01}); err != nil {
		t.Fatal(err)
	}
	if n, err := w.DeleteOrphanReservations(ctx); err != nil || n != 0 {
		t.Fatalf("開著的 batch 的預留被刪：%d %v", n, err)
	}
	// external ID 只寫一次。
	if err := w.SetBatchExternalID(ctx, "b1", "ext_1"); err != nil {
		t.Fatal(err)
	}
	_ = w.SetBatchExternalID(ctx, "b1", "ext_2")
	// 輪詢在 5 分鐘後；刪除影音時取消等待中的任務、輪詢提前。
	if err := w.ScheduleJob(ctx, BatchPollKind, "b1", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(r *Repo) error { _, err := r.CancelItemTasks(ctx, it.ID); return err }); err != nil {
		t.Fatal(err)
	}
	job, err := w.ScheduledJob(ctx, BatchPollKind, "b1")
	if err != nil || !job.NextRunAt.Equal(now) {
		t.Fatalf("輪詢 = %+v %v", job, err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if tk, _ := w.Task(ctx, id); tk.State != TaskCancelled {
			t.Fatalf("任務 = %+v", tk)
		}
	}
	// 取消之後 batch 仍開著：預留留著；batch 結束之後才算孤兒。
	// （provider 仍可能計費，結果回來時記為 discarded。）
	if n, _ := w.DeleteOrphanReservations(ctx); n != 0 {
		t.Fatalf("batch 還開著時刪了取消任務的預留：%d", n)
	}
	if ok, _ := w.UnparkTask(ctx, a.ID, "b1", json.RawMessage(`{}`)); ok {
		t.Error("已取消的任務不能改走一般 API")
	}
	if ok, _ := w.FinishBatchTask(ctx, a.ID, "b1"); ok {
		t.Error("已取消的任務不能標記完成")
	}
	end := now.Add(time.Hour)
	if err := w.SetBatchState(ctx, "b1", BatchEnded, &end, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetBatchState(ctx, "b1", BatchEnded, nil, "n2"); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.DeleteOrphanReservations(ctx); n != 1 {
		t.Fatalf("batch 結束之後的預留 = %d", n)
	}
	got1, err := w.Batch(ctx, "b1")
	if err != nil || got1.ExternalID != "ext_1" || got1.State != BatchEnded || got1.EndedAt == nil || !got1.EndedAt.Equal(end) || got1.Notice != "n1" || got1.Open() || len(got1.Entries) != 2 || got1.Entries[0].Reservation != "r1" {
		t.Fatalf("batch = %+v %v", got1, err)
	}
	if _, err := w.Batch(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 batch = %v", err)
	}
	if _, err := w.WaitingBatchOfRun(ctx, sr.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("沒有等待中的任務 = %v", err)
	}
}

// 改走一般 API 與完成：只處理還在這個 batch 中的任務。
func TestBatchUnparkAndFinish(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	_, sr := seedItem(t, s, "abc123xxxxx")
	a := newTask(t, s, sr, 3, nil)
	b := newTask(t, s, sr, 3, nil)
	w := s.Write()
	now := clk.Now()
	if err := w.InsertBatch(ctx, Batch{ID: "b1", Provider: "openai", Model: "m", SubmittedAt: now, DeadlineAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a.ID, b.ID} {
		if ok, err := w.ParkPendingTask(ctx, id, "b1"); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if wb, err := w.WaitingBatchOfRun(ctx, sr.ID); err != nil || wb.ID != "b1" || !wb.Open() {
		t.Fatalf("WaitingBatchOfRun = %+v %v", wb, err)
	}
	if ok, _ := w.UnparkTask(ctx, a.ID, "other", json.RawMessage(`{"x":1}`)); ok {
		t.Error("其他 batch 的 ID 不能改走一般 API")
	}
	if ok, err := w.UnparkTask(ctx, a.ID, "b1", json.RawMessage(`{"x":1}`)); !ok || err != nil {
		t.Fatal(ok, err)
	}
	tk, _ := w.Task(ctx, a.ID)
	if tk.State != TaskPending || tk.BatchID != "" || string(tk.Payload) != `{"x":1}` {
		t.Fatalf("改走一般 API 之後 = %+v", tk)
	}
	if ok, err := w.FinishBatchTask(ctx, b.ID, "b1"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if tk, _ := w.Task(ctx, b.ID); tk.State != TaskDone {
		t.Fatalf("完成之後 = %+v", tk)
	}
	if ok, _ := w.FinishBatchTask(ctx, b.ID, "b1"); ok {
		t.Error("完成兩次")
	}
}
