package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// summaryAt 新增一部影音，summary 步驟在 clk 的現在以 status（與 reason）結束。
func summaryAt(t *testing.T, s *Store, ext, status, reason string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := s.Tx(ctx, func(r *Repo) error {
		it, _, err := r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: ext, URL: "u"})
		if err != nil {
			return err
		}
		id = it.ID
		snap, err := r.EnsureSnapshot(ctx, strings.Repeat("b", 64), `{}`)
		if err != nil {
			return err
		}
		run, err := r.InsertStageRun(ctx, NewStageRun{ItemID: id, Stage: "summary", Status: StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			return err
		}
		return r.SetStageState(ctx, run.ID, StageState{Status: status, BlockReason: reason})
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDigestsCRUDAndCandidates(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	d := Digest{ID: "d", Period: "daily", At: "08:00", Timezone: "UTC", Notifiers: []string{"a", "b"}, Scope: "all", Enabled: true}
	if err := w.InsertDigest(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertDigest(ctx, d); err == nil {
		t.Error("重複 id 要錯誤")
	}
	if err := w.InsertDigest(ctx, Digest{ID: "e", Period: "weekly", Weekday: "mon", At: "09:00", Timezone: "UTC", Scope: "subscriptions"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().Digest(ctx, "d")
	if err != nil || len(got.Notifiers) != 2 || !got.Enabled || !got.CreatedAt.Equal(clk.Now()) || got.LastRunAt != nil {
		t.Fatalf("Digest = %+v %v", got, err)
	}
	if e, _ := s.Read().Digest(ctx, "e"); e.Notifiers == nil || len(e.Notifiers) != 0 || e.Enabled {
		t.Errorf("沒有 notifier 時是空陣列：%+v", e)
	}
	if _, err := s.Read().Digest(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
	last := clk.Now().Add(-time.Hour)
	got.At, got.Enabled, got.LastRunAt = "10:00", false, &last
	if err := w.UpdateDigest(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Read().Digest(ctx, "d"); got.At != "10:00" || got.Enabled || got.LastRunAt == nil || !got.LastRunAt.Equal(last) {
		t.Errorf("Update = %+v", got)
	}
	got.LastRunAt = nil
	if err := w.UpdateDigest(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Read().Digest(ctx, "d"); got.LastRunAt != nil {
		t.Errorf("清除 last_run_at：%+v", got)
	}
	if list, err := s.Read().ListDigests(ctx); err != nil || len(list) != 2 || list[0].ID != "d" {
		t.Errorf("List = %+v %v", list, err)
	}

	// 候選：範圍 (start, end]、done 或 skipped（不含 cancelled）、不在垃圾桶、沒有列過。
	start := clk.Now()
	clk.Advance(time.Minute)
	done := summaryAt(t, s, "c1", StatusDone, "")
	off := summaryAt(t, s, "c2", StatusSkipped, "off")
	summaryAt(t, s, "c3", StatusSkipped, "cancelled")
	summaryAt(t, s, "c4", StatusFailed, "")
	trashed := summaryAt(t, s, "c5", StatusDone, "")
	if _, err := s.w.ExecContext(ctx, `UPDATE items SET deleted_at = 1 WHERE id = ?`, trashed); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute)
	end := clk.Now()
	clk.Advance(time.Minute)
	summaryAt(t, s, "c6", StatusDone, "") // 範圍之後
	cands, err := s.Read().DigestCandidates(ctx, "d", start, end, true)
	if err != nil || len(cands) != 2 {
		t.Fatalf("候選 = %+v %v", cands, err)
	}
	ids := map[string]bool{cands[0].ItemID: true, cands[1].ItemID: true}
	if !ids[done] || !ids[off] {
		t.Errorf("候選 = %+v", cands)
	}
	// subscriptions 範圍只有訂閱建立的影音。
	if cands, _ := s.Read().DigestCandidates(ctx, "d", start, end, false); len(cands) != 0 {
		t.Errorf("subscriptions：%+v", cands)
	}
	sub, err := w.InsertSubscription(ctx, Subscription{Kind: "channel", Platform: "youtube", ExternalID: "UCx", URL: "v", Schedule: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetItemSubscription(ctx, done, sub.ID); err != nil {
		t.Fatal(err)
	}
	if cands, _ := s.Read().DigestCandidates(ctx, "d", start, end, false); len(cands) != 1 || cands[0].ItemID != done {
		t.Errorf("subscriptions：%+v", cands)
	}
	// 列過的影音不再列出（同一個彙整）；別的彙整不受影響。重複寫入不錯誤。
	for range 2 {
		if err := w.InsertDigestEntry(ctx, "d", done, end); err != nil {
			t.Fatal(err)
		}
	}
	if cands, _ := s.Read().DigestCandidates(ctx, "d", start, end, true); len(cands) != 1 || cands[0].ItemID != off {
		t.Errorf("列過之後：%+v", cands)
	}
	if cands, _ := s.Read().DigestCandidates(ctx, "e", start, end, true); len(cands) != 2 {
		t.Errorf("別的彙整：%+v", cands)
	}
	if err := w.SetDigestLastRun(ctx, "d", end); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Read().Digest(ctx, "d"); got.LastRunAt == nil || !got.LastRunAt.Equal(end) {
		t.Errorf("last_run_at = %v", got.LastRunAt)
	}
	// 刪除：digest_entries 一起刪除（cascade）。
	if ok, err := w.DeleteDigest(ctx, "d"); !ok || err != nil {
		t.Fatalf("Delete = %v %v", ok, err)
	}
	if ok, _ := w.DeleteDigest(ctx, "d"); ok {
		t.Error("第二次刪除")
	}
	var n int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM digest_entries WHERE digest_id = 'd'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("entries 沒有一起刪除：%d %v", n, err)
	}
}
