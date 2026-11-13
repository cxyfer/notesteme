package store

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/events"
)

func TestItemCreatedPublishedAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	got := recordEvents(s)

	var it Item
	var seenInTx int
	err := s.Tx(ctx, func(r *Repo) error {
		var err error
		it, _, err = r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"})
		seenInTx = len(got())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenInTx != 0 {
		t.Errorf("交易還沒 commit 就發布了 %d 個事件", seenInTx)
	}
	want := events.Event{Type: events.TypeItemCreated, Item: "yt:abc123", ItemID: it.ID}
	if evs := got(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("事件 = %+v，預期 [%+v]", evs, want)
	}

	// 已存在而沒有新增：不再發布。
	if _, created, err := s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"}); err != nil || created {
		t.Fatalf("created = %v, err = %v", created, err)
	}
	if evs := got(); len(evs) != 1 {
		t.Errorf("已存在的影音不應再發布事件，收到 %+v", evs)
	}
}

func TestItemCreatedDroppedOnRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	got := recordEvents(s)

	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		if _, _, err := r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc123", URL: "u"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v", err)
	}
	if evs := got(); len(evs) != 0 {
		t.Errorf("rollback 後不應發布 item.created，收到 %+v", evs)
	}
}

// 單一陳述句（不在交易中）成功後立即發布。
func TestItemCreatedPublishedForSingleStatement(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	got := recordEvents(s)
	it, created, err := s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "bilibili", ExternalID: "BV1xx411c7mD", Part: 2, URL: "u"})
	if err != nil || !created {
		t.Fatalf("created = %v, err = %v", created, err)
	}
	want := events.Event{Type: events.TypeItemCreated, Item: "bili:BV1xx411c7mD/p2", ItemID: it.ID}
	if evs := got(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("事件 = %+v，預期 [%+v]", evs, want)
	}
}
