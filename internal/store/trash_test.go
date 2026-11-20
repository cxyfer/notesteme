package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
)

func TestTrashItemSetsDeletedAtAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	it, _ := seedItem(t, s, "abc123xxxxx")
	if err := s.Write().UpdateItemMeta(ctx, it.ID, ItemMeta{Title: "PostgreSQL in 100 Seconds"}); err != nil {
		t.Fatal(err)
	}
	got := recordEvents(s)

	got1, changed, err := s.Write().TrashItem(ctx, it.ID)
	if err != nil || !changed {
		t.Fatalf("TrashItem = %v, %v", changed, err)
	}
	if got1.DeletedAt == nil || !got1.DeletedAt.Equal(clk.Now()) {
		t.Errorf("DeletedAt = %v，預期 %v", got1.DeletedAt, clk.Now())
	}
	want := events.Event{Type: events.TypeItemDeleted, Item: "yt:abc123xxxxx", ItemID: it.ID, Title: "PostgreSQL in 100 Seconds"}
	if evs := got(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("事件 = %+v，預期 [%+v]", evs, want)
	}

	// 已在垃圾桶中：不改 deleted_at，也不再發布事件。
	clk.Advance(time.Hour)
	again, changed, err := s.Write().TrashItem(ctx, it.ID)
	if err != nil || changed {
		t.Fatalf("第二次 TrashItem = %v, %v", changed, err)
	}
	if !again.DeletedAt.Equal(*got1.DeletedAt) {
		t.Errorf("deleted_at 被改成 %v", again.DeletedAt)
	}
	if evs := got(); len(evs) != 1 {
		t.Errorf("已在垃圾桶中不應再發布事件，收到 %+v", evs)
	}
}

func TestTrashItemUnknown(t *testing.T) {
	s, _ := openTest(t)
	if _, _, err := s.Write().TrashItem(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("TrashItem = %v，預期 ErrNotFound", err)
	}
	if _, _, err := s.Write().RestoreItem(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RestoreItem = %v，預期 ErrNotFound", err)
	}
}

func TestTrashItemRollsBackWithEvent(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "abc123xxxxx")
	got := recordEvents(s)
	boom := errors.New("boom")
	err := s.Tx(ctx, func(r *Repo) error {
		if _, _, err := r.TrashItem(ctx, it.ID); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Tx = %v", err)
	}
	if now, _ := s.Read().ItemByID(ctx, it.ID); now.DeletedAt != nil {
		t.Errorf("rollback 後 deleted_at = %v", now.DeletedAt)
	}
	for _, ev := range got() {
		if ev.Type == events.TypeItemDeleted {
			t.Errorf("rollback 後不應發布 item.deleted")
		}
	}
}

func TestRestoreItem(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	it, _ := seedItem(t, s, "abc123xxxxx")

	// 不在垃圾桶中：不改任何東西。
	if _, changed, err := s.Write().RestoreItem(ctx, it.ID); err != nil || changed {
		t.Fatalf("還原不在垃圾桶中的影音 = %v, %v", changed, err)
	}
	if _, _, err := s.Write().TrashItem(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	got := recordEvents(s)
	back, changed, err := s.Write().RestoreItem(ctx, it.ID)
	if err != nil || !changed {
		t.Fatalf("RestoreItem = %v, %v", changed, err)
	}
	if back.DeletedAt != nil {
		t.Errorf("DeletedAt = %v，預期 nil", back.DeletedAt)
	}
	want := events.Event{Type: events.TypeItemRestored, Item: "yt:abc123xxxxx", ItemID: it.ID}
	if evs := got(); len(evs) != 1 || evs[0] != want {
		t.Fatalf("事件 = %+v，預期 [%+v]", evs, want)
	}
}

func TestListTrashNewestFirst(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	a, _ := seedItem(t, s, "aaaaaaaaaaa")
	b, _ := seedItem(t, s, "bbbbbbbbbbb")
	c, _ := seedItem(t, s, "ccccccccccc")
	// 刪除順序 b、a；c 留在媒體庫。
	for _, id := range []string{b.ID, a.ID} {
		if _, _, err := s.Write().TrashItem(ctx, id); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Minute)
	}
	list, err := s.Read().ListTrash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != a.ID || list[1].ID != b.ID {
		t.Fatalf("垃圾桶 = %+v，預期 [a, b]（新到舊），且不含 %s", list, c.ID)
	}
	// 同一毫秒刪除的，後刪除的在前。
	d, _ := seedItem(t, s, "ddddddddddd")
	e, _ := seedItem(t, s, "eeeeeeeeeee")
	for _, id := range []string{d.ID, e.ID} {
		if _, _, err := s.Write().TrashItem(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	list, _ = s.Read().ListTrash(ctx)
	if len(list) != 4 || list[0].ID != e.ID || list[1].ID != d.ID {
		t.Errorf("同一毫秒刪除的順序不對：%+v", list)
	}
}

func TestDeleteTombstones(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	for _, sha := range []string{"", "x"} {
		if _, err := s.w.ExecContext(ctx, `INSERT INTO item_tombstones (platform, external_id, part, sha256, purged_at) VALUES ('youtube', 'abc123xxxxx', 0, ?, 1)`, sha); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO item_tombstones (platform, external_id, part, sha256, purged_at) VALUES ('youtube', 'other', 0, '', 1)`); err != nil {
		t.Fatal(err)
	}
	n, err := s.Write().DeleteTombstones(ctx, "youtube", "abc123xxxxx", 0)
	if err != nil || n != 2 {
		t.Fatalf("DeleteTombstones = %d, %v；預期 2（不論 sha256）", n, err)
	}
	var left int
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_tombstones`).Scan(&left); err != nil || left != 1 {
		t.Errorf("剩下 %d 筆（%v），預期只剩別部影音的 1 筆", left, err)
	}
}
