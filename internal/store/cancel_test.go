package store

import (
	"context"
	"errors"
	"testing"
)

// 領取任務與放回租約會改變步驟狀態，也要發布事件（PIPE-R14）。
func TestClaimAndReleasePublishStageEvents(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	_, sr := seedItem(t, s, "abc123")
	newTask(t, s, sr, 3, nil)
	got := recordEvents(s) // 建立之後才開始記錄，只看領取與放回

	claim(t, s, claimOpts("w1", 1))
	evs := got()
	if len(evs) != 1 || evs[0].Stage != "metadata" || evs[0].Status != StatusRunning || evs[0].Item != "yt:abc123" {
		t.Fatalf("領取後的事件 = %+v，預期一個 metadata running", evs)
	}

	err := s.Tx(ctx, func(r *Repo) error {
		_, err := r.ReleaseOwned(ctx, "w1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	evs = got()
	if len(evs) != 2 || evs[1].Stage != "metadata" || evs[1].Status != StatusPending {
		t.Fatalf("放回後的事件 = %+v，預期第二個是 metadata pending", evs)
	}

	// 步驟不是 running（已經放回）時，再放一次不應發布事件。
	if _, err := s.Write().ReleaseOwned(ctx, "w1"); err != nil {
		t.Fatal(err)
	}
	if n := len(got()); n != 2 {
		t.Errorf("沒有 running 的步驟時不應再發布事件，共 %d 個", n)
	}
}

// 領取在交易中進行，事件要等 commit 之後才發布；回復時不發布。
func TestClaimEventsDroppedOnRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	_, sr := seedItem(t, s, "abc123")
	newTask(t, s, sr, 3, nil)
	got := recordEvents(s)

	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		if _, err := r.ClaimTasks(ctx, claimOpts("w1", 1)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v", err)
	}
	if evs := got(); len(evs) != 0 {
		t.Errorf("rollback 後不應發布事件，收到 %+v", evs)
	}
}

// 取消影音的任務：pending 與 leased 的都改為 cancelled 並清除租約，別的影音不動；
// 之後原持有者晚到的寫回回傳 ErrLeaseLost，不會把任務蓋回 done（PIPE-R15）。
func TestCancelItemTasks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, sr1 := seedItem(t, s, "a")
	_, sr2 := seedItem(t, s, "b")
	leased := newTask(t, s, sr1, 3, nil)
	held := claim(t, s, claimOpts("w", 1))[0]
	if held.ID != leased.ID {
		t.Fatalf("預期領到 %s，實際 %s", leased.ID, held.ID)
	}
	pending := newTask(t, s, sr1, 3, nil)
	untouched := newTask(t, s, sr2, 3, nil)

	ids, err := s.Write().CancelItemTasks(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || !(ids[0] == leased.ID && ids[1] == pending.ID || ids[0] == pending.ID && ids[1] == leased.ID) {
		t.Fatalf("取消的任務 = %v，預期 %s 與 %s", ids, leased.ID, pending.ID)
	}
	for _, id := range []string{leased.ID, pending.ID} {
		got, _ := s.Read().Task(ctx, id)
		if got.State != TaskCancelled || got.LeaseToken != "" || got.LeaseOwner != "" {
			t.Errorf("任務 %s 應為 cancelled 且沒有租約：%+v", id, got)
		}
	}
	if got, _ := s.Read().Task(ctx, untouched.ID); got.State != TaskPending {
		t.Errorf("別的影音的任務不應受影響：%+v", got)
	}

	// 晚到的寫回無效。
	if err := s.Write().CompleteTask(ctx, held.ID, held.LeaseToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("取消後的寫回應回傳 ErrLeaseLost，實際 %v", err)
	}
	if got, _ := s.Read().Task(ctx, held.ID); got.State != TaskCancelled {
		t.Errorf("晚到的寫回不應改動任務：%+v", got)
	}

	// 冪等：沒有未結束的任務時回傳空。
	if ids, err := s.Write().CancelItemTasks(ctx, it.ID); err != nil || len(ids) != 0 {
		t.Errorf("再取消一次 = %v, %v，預期沒有任務", ids, err)
	}
}
