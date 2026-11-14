package store

import (
	"context"
	"testing"
)

// seedStage 為 item 新增一個步驟，回傳步驟。
func seedStage(t *testing.T, s *Store, itemID, stage, snapSHA string) StageRun {
	t.Helper()
	ctx := context.Background()
	snap, err := s.Write().EnsureSnapshot(ctx, snapSHA, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := s.Write().InsertStageRun(ctx, NewStageRun{ItemID: itemID, Stage: stage, Status: StatusPending, ConfigSnapshotID: snap})
	if err != nil {
		t.Fatal(err)
	}
	return sr
}

func TestResumeOnWrittenOnlyWhenBlocked(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	_, sr := seedItem(t, s, "abc123xxxxx")

	if sr.ResumeOn != "" {
		t.Fatalf("新步驟的 ResumeOn = %q，預期空", sr.ResumeOn)
	}
	// blocked：寫入 resume_on。
	if err := s.Write().SetStageState(ctx, sr.ID, StageState{Status: StatusBlocked, BlockReason: "needs_action", ResumeOn: "cookie:bilibili"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().StageRun(ctx, sr.ID)
	if err != nil || got.ResumeOn != "cookie:bilibili" {
		t.Fatalf("blocked 後 ResumeOn = %q, %v", got.ResumeOn, err)
	}
	// 改成其他狀態：即使呼叫端帶了 ResumeOn 也要清空。
	if err := s.Write().SetStageState(ctx, sr.ID, StageState{Status: StatusPending, ResumeOn: "cookie:bilibili"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Read().StageRun(ctx, sr.ID); got.ResumeOn != "" {
		t.Errorf("非 blocked 的 ResumeOn = %q，預期空", got.ResumeOn)
	}
	// 改成 running 也會清掉。
	if err := s.Write().SetStageState(ctx, sr.ID, StageState{Status: StatusBlocked, ResumeOn: "cookie:youtube"}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Read().StageRun(ctx, sr.ID); got.ResumeOn != "cookie:youtube" {
		t.Fatalf("ResumeOn = %q", got.ResumeOn)
	}
	if err := s.Write().SetStageState(ctx, sr.ID, StageState{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Read().StageRun(ctx, sr.ID); got.ResumeOn != "" {
		t.Errorf("running 的 ResumeOn = %q，預期空", got.ResumeOn)
	}
}

func TestBlockedRunsByResume(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	block := func(id, resume string) {
		t.Helper()
		if err := s.Write().SetStageState(ctx, id, StageState{Status: StatusBlocked, BlockReason: "needs_action", ResumeOn: resume}); err != nil {
			t.Fatal(err)
		}
	}

	// a：metadata blocked on bilibili cookie；另一個步驟 normalize 之後也 blocked。
	itA, trA := seedItem(t, s, "aaaaaaaaaaa")
	block(trA.ID, "cookie:bilibili")
	nrA := seedStage(t, s, itA.ID, "normalize", "sha-a2")
	block(nrA.ID, "cookie:bilibili")
	// b：blocked on youtube cookie，不應出現在 bilibili 的結果。
	_, trB := seedItem(t, s, "bbbbbbbbbbb")
	block(trB.ID, "cookie:youtube")
	// c：沒有 resume_on 的 blocked（例如缺 API key）。
	_, trC := seedItem(t, s, "ccccccccccc")
	block(trC.ID, "")
	// d：已刪除的影音。
	itD, trD := seedItem(t, s, "ddddddddddd")
	block(trD.ID, "cookie:bilibili")
	if _, err := s.w.ExecContext(ctx, `UPDATE items SET deleted_at = 1 WHERE id = ?`, itD.ID); err != nil {
		t.Fatal(err)
	}
	// e：舊的 blocked，之後同一 (item, stage) 又有更新的一筆（pending）；不應出現。
	itE, trE := seedItem(t, s, "eeeeeeeeeee")
	block(trE.ID, "cookie:bilibili")
	seedStage(t, s, itE.ID, "metadata", "sha-e2")

	got, err := s.Read().BlockedRunsByResume(ctx, "cookie:bilibili")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != trA.ID || got[1].ID != nrA.ID || got[0].ResumeOn != "cookie:bilibili" || got[0].Status != StatusBlocked {
		t.Fatalf("BlockedRunsByResume(bilibili) = %+v，預期 [%s %s]", got, trA.ID, nrA.ID)
	}
	if got, err = s.Read().BlockedRunsByResume(ctx, "cookie:youtube"); err != nil || len(got) != 1 || got[0].ID != trB.ID {
		t.Errorf("BlockedRunsByResume(youtube) = %+v, %v", got, err)
	}
	if got, err = s.Read().BlockedRunsByResume(ctx, "cookie:none"); err != nil || len(got) != 0 {
		t.Errorf("BlockedRunsByResume(none) = %+v, %v", got, err)
	}
}
