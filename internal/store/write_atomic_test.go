package store

import (
	"context"
	"errors"
	"testing"
)

// Write() 的寫入也要和事件同時成功或同時失敗：事件寫入失敗（這裡用拿掉 events 表模擬）時，
// 狀態變更不能留下。以前這些路徑是「先改狀態、再另一個陳述句寫事件」，事件失敗時狀態已經提交。
func TestWriteRepoEmitPathsAreAtomic(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)

	itemA, srA := seedItem(t, s, "aaaaaaaaaaa")
	_, srB := seedItem(t, s, "bbbbbbbbbbb")
	_, srC := seedItem(t, s, "ccccccccccc")

	// C 先被領取（只有它是 pending 的任務），之後才建立 B 的任務。
	taskC := newTask(t, s, srC, 0, nil)
	if got := claim(t, s, claimOpts("w1", 1)); len(got) != 1 || got[0].ID != taskC.ID {
		t.Fatalf("前置：領到 %+v", got)
	}
	taskB := newTask(t, s, srB, 0, nil)

	if _, err := s.w.ExecContext(ctx, `DROP TABLE events`); err != nil {
		t.Fatal(err)
	}
	w := s.Write()

	t.Run("SetStageState", func(t *testing.T) {
		if err := w.SetStageState(ctx, srA.ID, StageState{Status: StatusDone}); err == nil {
			t.Fatal("事件寫不進去，應該失敗")
		}
		got, err := s.Read().StageRun(ctx, srA.ID)
		if err != nil || got.Status != StatusPending {
			t.Errorf("狀態應該維持 pending：%+v, %v", got, err)
		}
	})

	t.Run("InsertItemIfAbsent", func(t *testing.T) {
		if _, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "newnewnewne", URL: "u"}); err == nil {
			t.Fatal("事件寫不進去，應該失敗")
		}
		if _, err := s.Read().ItemByKey(ctx, "youtube", "newnewnewne", 0); !errors.Is(err, ErrNotFound) {
			t.Errorf("影音不該被建立：%v", err)
		}
	})

	t.Run("InsertStageRun", func(t *testing.T) {
		snap := mustSnapshot(t, s)
		if _, err := w.InsertStageRun(ctx, NewStageRun{ItemID: itemA.ID, Stage: "transcript", Status: StatusPending, ConfigSnapshotID: snap}); err == nil {
			t.Fatal("事件寫不進去，應該失敗")
		}
		runs, err := s.Read().StageRunsByItem(ctx, itemA.ID)
		if err != nil || len(runs) != 1 {
			t.Errorf("步驟不該被建立：%d 個, %v", len(runs), err)
		}
	})

	t.Run("ClaimTasks", func(t *testing.T) {
		if _, err := w.ClaimTasks(ctx, claimOpts("w2", 1)); err == nil {
			t.Fatal("事件寫不進去，應該失敗")
		}
		got, err := s.Read().Task(ctx, taskB.ID)
		if err != nil || got.State != TaskPending {
			t.Errorf("任務應該維持 pending：%+v, %v", got, err)
		}
		if sr, _ := s.Read().StageRun(ctx, srB.ID); sr.Status != StatusPending {
			t.Errorf("步驟應該維持 pending：%s", sr.Status)
		}
	})

	t.Run("ReleaseOwned", func(t *testing.T) {
		if _, err := w.ReleaseOwned(ctx, "w1"); err == nil {
			t.Fatal("事件寫不進去，應該失敗")
		}
		got, err := s.Read().Task(ctx, taskC.ID)
		if err != nil || got.State != TaskLeased {
			t.Errorf("任務應該維持 leased：%+v, %v", got, err)
		}
		if sr, _ := s.Read().StageRun(ctx, srC.ID); sr.Status != StatusRunning {
			t.Errorf("步驟應該維持 running：%s", sr.Status)
		}
	})

	t.Run("ReleaseLeases", func(t *testing.T) {
		if _, err := w.ReleaseLeases(ctx, "someone-else"); err == nil {
			t.Fatal("事件寫不進去，應該失敗")
		}
		got, err := s.Read().Task(ctx, taskC.ID)
		if err != nil || got.State != TaskLeased {
			t.Errorf("任務應該維持 leased：%+v, %v", got, err)
		}
	})
}

func mustSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	var id string
	if err := s.Tx(context.Background(), func(r *Repo) error {
		var err error
		id, err = r.EnsureSnapshot(context.Background(), "sha-extra", `{}`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}
