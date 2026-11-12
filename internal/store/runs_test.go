package store

import (
	"context"
	"testing"
	"time"
)

func TestExecutedRunsByItemListsStartedRunsNewestFirstWithConfig(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	it, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	snapA, err := w.EnsureSnapshot(ctx, "sha-a", `{"language":{"fallback":["supadata"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := w.EnsureSnapshot(ctx, "sha-b", `{"language":{"targets":["en"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	newRun := func(stage, snap string) StageRun {
		t.Helper()
		sr, err := w.InsertStageRun(ctx, NewStageRun{ItemID: it.ID, Stage: stage, Status: StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			t.Fatal(err)
		}
		return sr
	}
	meta, trans, norm := newRun("metadata", snapA), newRun("transcript", snapB), newRun("normalize", snapB)

	// 還沒有任何步驟開始：沒有紀錄。
	if got, err := w.ExecutedRunsByItem(ctx, it.ID); err != nil || len(got) != 0 {
		t.Fatalf("還沒執行：%+v，%v", got, err)
	}

	// metadata 先開始並完成；transcript 在同一毫秒開始（以建立順序決定先後）；normalize 不開始。
	if err := w.SetStageState(ctx, meta.ID, StageState{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetStageResult(ctx, meta.ID, StageResult{Executor: "local", CacheKey: "k-meta", ImplVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetStageState(ctx, meta.ID, StageState{Status: StatusDone}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetStageState(ctx, trans.ID, StageState{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetStageResult(ctx, trans.ID, StageResult{Executor: "local", CacheKey: "k-trans", ImplVersion: 2}); err != nil {
		t.Fatal(err)
	}

	got, err := w.ExecutedRunsByItem(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != trans.ID || got[1].ID != meta.ID {
		t.Fatalf("應只列出已開始的步驟，且新的在前：%+v（normalize %s 不應出現）", got, norm.ID)
	}
	if got[0].Status != StatusRunning || got[0].FinishedAt != nil || got[0].CacheKey != "k-trans" || got[0].ImplVersion != 2 ||
		got[0].ConfigJSON != `{"language":{"targets":["en"]}}` || !got[0].StartedAt.Equal(clk.Now()) {
		t.Errorf("transcript = %+v", got[0])
	}
	if got[1].Status != StatusDone || got[1].FinishedAt == nil || got[1].ConfigJSON != `{"language":{"fallback":["supadata"]}}` {
		t.Errorf("metadata = %+v", got[1])
	}

	// 開始時間較晚的排在前面，即使它的建立順序較早。
	clk.Advance(time.Minute)
	if err := w.SetStageState(ctx, meta.ID, StageState{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if got, _ = w.ExecutedRunsByItem(ctx, it.ID); len(got) != 2 || got[0].ID != meta.ID {
		t.Errorf("開始時間較晚的應在前：%+v", got)
	}

	// 其他影音的紀錄不會混入。
	if other, err := w.ExecutedRunsByItem(ctx, "nope"); err != nil || len(other) != 0 {
		t.Errorf("其他影音：%+v，%v", other, err)
	}
}
