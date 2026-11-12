package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/events"
)

// recordEvents 讓 s 把事件記進回傳的函式；呼叫它可取得目前為止收到的事件。
func recordEvents(s *Store) func() []events.Event {
	var mu sync.Mutex
	var got []events.Event
	s.SetEventSink(func(ev events.Event) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev)
	})
	return func() []events.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]events.Event(nil), got...)
	}
}

func TestStageEventsPublishedAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	got := recordEvents(s)

	var seenInTx int
	var it Item
	var sr StageRun
	err := s.Tx(ctx, func(r *Repo) error {
		var err error
		if it, _, err = r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"}); err != nil {
			return err
		}
		snap, err := r.EnsureSnapshot(ctx, "sha", `{}`)
		if err != nil {
			return err
		}
		if sr, err = r.InsertStageRun(ctx, NewStageRun{ItemID: it.ID, Stage: "metadata", Status: StatusPending, ConfigSnapshotID: snap}); err != nil {
			return err
		}
		seenInTx = len(got())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenInTx != 0 {
		t.Errorf("交易還沒 commit 就發布了 %d 個事件", seenInTx)
	}
	want := events.Event{Type: "stage.status", Item: "yt:abc123", ItemID: it.ID, Stage: "metadata", Status: "pending"}
	if evs := got(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("事件 = %+v，預期 [%+v]", evs, want)
	}

	// 單一陳述句：成功後立即發布。
	if err := s.Write().SetStageState(ctx, sr.ID, StageState{Status: StatusDone}); err != nil {
		t.Fatal(err)
	}
	evs := got()
	if len(evs) != 2 || evs[1].Status != "done" || evs[1].Item != "yt:abc123" {
		t.Fatalf("事件 = %+v，預期第二個是 done", evs)
	}
}

func TestStageEventsDroppedOnRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	got := recordEvents(s)

	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		it, _, err := r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"})
		if err != nil {
			return err
		}
		snap, err := r.EnsureSnapshot(ctx, "sha", `{}`)
		if err != nil {
			return err
		}
		if _, err = r.InsertStageRun(ctx, NewStageRun{ItemID: it.ID, Stage: "metadata", Status: StatusPending, ConfigSnapshotID: snap}); err != nil {
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

func TestSetStageStateOfMissingStageIsNotAnError(t *testing.T) {
	s, _ := openTest(t)
	got := recordEvents(s)
	if err := s.Write().SetStageState(context.Background(), "missing", StageState{Status: StatusDone}); err != nil {
		t.Fatalf("SetStageState = %v", err)
	}
	if evs := got(); len(evs) != 0 {
		t.Errorf("沒有這個步驟時不應發布事件，收到 %+v", evs)
	}
}

func TestStageWritesWithoutSinkStillWork(t *testing.T) {
	s, _ := openTest(t)
	_, sr := seedItem(t, s, "abc123")
	if err := s.Write().SetStageState(context.Background(), sr.ID, StageState{Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
}
