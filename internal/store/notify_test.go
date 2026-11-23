package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestNotifiersRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	n := Notifier{ID: "phone", Kind: "ntfy", Nonce: []byte("n"), Ciphertext: []byte("c"), PublicJSON: `{"topic":"t"}`, Events: []string{"item.done"}, Enabled: true}
	if err := w.InsertNotifier(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertNotifier(ctx, n); !errors.Is(err, ErrNotifierExists) {
		t.Fatalf("重複的 id：%v", err)
	}
	clk.Advance(time.Second)
	off := Notifier{ID: "off", Kind: "webhook", Nonce: []byte("n"), Ciphertext: []byte("c"), Events: []string{"item.done", "item.failed"}}
	if err := w.InsertNotifier(ctx, off); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().Notifier(ctx, "phone")
	if err != nil || got.Kind != "ntfy" || !got.Enabled || !slices.Equal(got.Events, []string{"item.done"}) || string(got.Ciphertext) != "c" {
		t.Fatalf("Notifier = %+v，%v", got, err)
	}
	if _, err := s.Read().Notifier(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
	// 只有啟用中、而且訂閱了事件的 notifier；名稱要完全相同（item.done 不會比對到 item.done2 之類的子字串）。
	if ids, err := s.Read().NotifierIDsForEvent(ctx, "item.done"); err != nil || !slices.Equal(ids, []string{"phone"}) {
		t.Errorf("item.done → %v，%v", ids, err)
	}
	if ids, _ := s.Read().NotifierIDsForEvent(ctx, "item.don"); len(ids) != 0 {
		t.Errorf("部分名稱不應比對到：%v", ids)
	}
	off.Enabled = true
	if err := w.UpdateNotifier(ctx, off); err != nil {
		t.Fatal(err)
	}
	if ids, _ := s.Read().NotifierIDsForEvent(ctx, "item.done"); !slices.Equal(ids, []string{"phone", "off"}) {
		t.Errorf("啟用後依建立順序：%v", ids)
	}
	if err := w.UpdateNotifier(ctx, Notifier{ID: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("更新不存在的：%v", err)
	}
	if c, _ := s.Read().CountNotifiers(ctx); c != 2 {
		t.Errorf("CountNotifiers = %d", c)
	}
	list, err := s.Read().ListNotifiers(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "phone" {
		t.Errorf("ListNotifiers = %+v，%v", list, err)
	}

	// 刪除 notifier 時刪掉它還沒送出的項目，已送出的保留（NOTIFY-R1、R8）。
	pending, _ := w.InsertOutbox(ctx, OutboxEntry{NotifierID: "phone", Event: "item.done", DedupeKey: "k1"})
	sent, _ := w.InsertOutbox(ctx, OutboxEntry{NotifierID: "phone", Event: "item.done", DedupeKey: "k2"})
	now := clk.Now()
	if err := w.RecordOutboxAttempt(ctx, sent, OutboxAttempt{State: OutboxSent, Attempts: 1, NextAttemptAt: now, LastAttemptAt: now, SentAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(r *Repo) error {
		ok, err := r.DeleteNotifier(ctx, "phone")
		if !ok {
			t.Error("應刪除 notifier")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.Read().ListOutbox(ctx, "", 10)
	if len(rows) != 1 || rows[0].ID != sent {
		t.Errorf("刪除後的 outbox = %+v（pending %s 應刪除）", rows, pending)
	}
	if ok, _ := w.DeleteNotifier(ctx, "phone"); ok {
		t.Error("第二次刪除應回報 false")
	}
}

func TestOutboxRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	t0 := clk.Now()

	if _, ok, err := s.Read().NextOutboxAttempt(ctx); ok || err != nil {
		t.Fatalf("空的 outbox：ok=%v %v", ok, err)
	}
	a, _ := w.InsertOutbox(ctx, OutboxEntry{NotifierID: "n", Event: "item.done", DedupeKey: "item.done:x:1", PayloadJSON: `{"a":1}`})
	b, _ := w.InsertOutbox(ctx, OutboxEntry{NotifierID: "n", Event: "item.failed", DedupeKey: "item.failed:x:metadata"})

	// 去重：任何狀態都算，只看 since 之後建立的。
	if ok, _ := s.Read().OutboxDedupeExists(ctx, "n", "item.done:x:1", t0.Add(-time.Hour)); !ok {
		t.Error("視窗內的同一個鍵應存在")
	}
	if ok, _ := s.Read().OutboxDedupeExists(ctx, "n", "item.done:x:1", t0); ok {
		t.Error("since 不含當下（建立時間等於 since 時不算在視窗內）")
	}
	if ok, _ := s.Read().OutboxDedupeExists(ctx, "other", "item.done:x:1", t0.Add(-time.Hour)); ok {
		t.Error("別的 notifier 不算")
	}

	due, err := s.Read().DueOutbox(ctx, t0, 10)
	if err != nil || len(due) != 2 || due[0].ID != a || due[0].PayloadJSON != `{"a":1}` || due[1].PayloadJSON != "{}" || due[0].State != OutboxPending {
		t.Fatalf("DueOutbox = %+v，%v", due, err)
	}
	// 一次失敗：延後一分鐘。
	if err := w.RecordOutboxAttempt(ctx, a, OutboxAttempt{State: OutboxPending, Attempts: 1, NextAttemptAt: t0.Add(time.Minute), LastAttemptAt: t0, LastError: "連線失敗。"}); err != nil {
		t.Fatal(err)
	}
	if err := w.DeferOutbox(ctx, b, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.Read().DueOutbox(ctx, t0, 10); len(due) != 0 {
		t.Errorf("都延後之後不應到期：%+v", due)
	}
	if at, ok, _ := s.Read().NextOutboxAttempt(ctx); !ok || !at.Equal(t0.Add(time.Minute)) {
		t.Errorf("NextOutboxAttempt = %v %v", at, ok)
	}
	if ts, _ := s.Read().OutboxAttemptsSince(ctx, "n", t0.Add(-time.Second)); len(ts) != 1 || !ts[0].Equal(t0) {
		t.Errorf("OutboxAttemptsSince = %v", ts)
	}
	if ts, _ := s.Read().OutboxAttemptsSince(ctx, "n", t0); len(ts) != 0 {
		t.Errorf("since 不含：%v", ts)
	}
	rows, _ := s.Read().ListOutbox(ctx, OutboxPending, 10)
	if len(rows) != 2 || rows[1].Attempts != 1 || rows[1].LastError != "連線失敗。" || rows[1].LastAttemptAt == nil {
		t.Errorf("ListOutbox(pending) = %+v", rows)
	}
	// dropped 之後不再改變（只改 pending）。
	if err := w.RecordOutboxAttempt(ctx, a, OutboxAttempt{State: OutboxDropped, Attempts: 2, NextAttemptAt: t0, LastAttemptAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordOutboxAttempt(ctx, a, OutboxAttempt{State: OutboxSent, Attempts: 3, NextAttemptAt: t0, LastAttemptAt: t0}); !errors.Is(err, ErrNotFound) {
		t.Errorf("已 dropped 的項目不應再寫入：%v", err)
	}
	if rows, _ := s.Read().ListOutbox(ctx, OutboxDropped, 10); len(rows) != 1 || rows[0].ID != a {
		t.Errorf("ListOutbox(dropped) = %+v", rows)
	}
	// 保留 30 天：只刪 sent、dropped。
	if n, _ := w.DeleteFinishedOutboxBefore(ctx, t0.Add(time.Hour)); n != 1 {
		t.Errorf("DeleteFinishedOutboxBefore = %d", n)
	}
	if rows, _ := s.Read().ListOutbox(ctx, "", 10); len(rows) != 1 || rows[0].ID != b {
		t.Errorf("清理後 = %+v", rows)
	}
}
