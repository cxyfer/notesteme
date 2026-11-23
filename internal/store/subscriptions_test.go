package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSubscriptionsRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	a, err := w.InsertSubscription(ctx, Subscription{Kind: "channel", Platform: "youtube", ExternalID: "UCa", URL: "u", Schedule: 6 * time.Hour, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || !a.CreatedAt.Equal(clk.Now()) || a.Schedule != 6*time.Hour || string(a.Overrides) != "{}" || a.BaselineAt != nil {
		t.Errorf("建立的訂閱 = %+v", a)
	}
	// 同一個來源可以有兩列（handle 解析後停用的那個，SUB-R12）；依建立順序。
	b, err := w.InsertSubscription(ctx, Subscription{Kind: "channel", Platform: "youtube", ExternalID: "UCa", URL: "v", Schedule: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.Read().SubscriptionsBySource(ctx, "channel", "youtube", "UCa")
	if err != nil || len(same) != 2 || same[0].ID != a.ID || same[1].ID != b.ID || same[1].Enabled {
		t.Fatalf("SubscriptionsBySource = %+v，%v", same, err)
	}
	if n, _ := s.Read().CountSubscriptions(ctx); n != 2 {
		t.Errorf("CountSubscriptions = %d", n)
	}

	// 檢查結果：baseline 只記第一次。
	t1 := clk.Now()
	if err := w.RecordSubscriptionCheck(ctx, a.ID, SubscriptionCheck{Title: "T", ExternalID: "UCa", Enabled: true, BaselineAt: &t1, CheckedAt: t1, LastError: "x", LastErrorClass: "retryable"}); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(time.Hour)
	if err := w.RecordSubscriptionCheck(ctx, a.ID, SubscriptionCheck{Title: "T2", ExternalID: "UCa", Enabled: true, BaselineAt: &t2, CheckedAt: t2}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Read().Subscription(ctx, a.ID)
	if got.Title != "T2" || got.BaselineAt == nil || !got.BaselineAt.Equal(t1) || !got.LastCheckedAt.Equal(t2) || got.LastError != "" || got.LastErrorClass != "" {
		t.Errorf("檢查之後 = %+v", got)
	}
	if err := w.RecordSubscriptionCheck(ctx, "nope", SubscriptionCheck{CheckedAt: t2}); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的訂閱：%v", err)
	}

	// 條目：重新寫入時保留已知的片長。
	d := 900
	if err := w.UpsertSubscriptionEntry(ctx, SubscriptionEntry{SubscriptionID: a.ID, Platform: "youtube", ExternalID: "v1", Title: "x", DurationS: &d, Decision: EntryDeferred, Reason: "backfill_per_check"}); err != nil {
		t.Fatal(err)
	}
	if err := w.UpsertSubscriptionEntry(ctx, SubscriptionEntry{SubscriptionID: a.ID, Platform: "youtube", ExternalID: "v1", Title: "x", Decision: EntryCreated, ItemID: "it1"}); err != nil {
		t.Fatal(err)
	}
	es, err := s.Read().SubscriptionEntries(ctx, a.ID, 10)
	if err != nil || len(es) != 1 || es[0].Decision != EntryCreated || es[0].Reason != "" || es[0].ItemID != "it1" || es[0].DurationS == nil || *es[0].DurationS != 900 {
		t.Fatalf("條目 = %+v，%v", es, err)
	}

	if has, err := s.Read().HasTombstone(ctx, "youtube", "v1", 0); err != nil || has {
		t.Errorf("HasTombstone = %v %v", has, err)
	}

	// 刪除：條目一起刪除。
	if ok, err := w.DeleteSubscription(ctx, a.ID); err != nil || !ok {
		t.Fatalf("DeleteSubscription = %v %v", ok, err)
	}
	if es, _ := s.Read().SubscriptionEntries(ctx, a.ID, 10); len(es) != 0 {
		t.Errorf("刪除後仍有條目：%+v", es)
	}
	if _, err := s.Read().Subscription(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("刪除後：%v", err)
	}
	if err := w.UpdateSubscriptionSettings(ctx, Subscription{ID: a.ID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("修改不存在的訂閱：%v", err)
	}
}
