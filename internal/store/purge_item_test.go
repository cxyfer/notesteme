package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
)

// purgeFixture 是一部帶著各種關聯資料的影音（A），加上一部不相干的影音（B）。
// A 與 B 共用一個設定快照；A 另有只有它用的快照。
type purgeFixture struct {
	s        *Store
	clk      interface{ Advance(time.Duration) }
	a, b     Item
	artA     []Artifact
	sharedSn string
	onlyASn  string
	taskA    Task
}

func (f *purgeFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.s.r.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s：%v", query, err)
	}
	return n
}

func (f *purgeFixture) trash(t *testing.T, id string, at time.Time) {
	t.Helper()
	if _, err := f.s.w.ExecContext(context.Background(), `UPDATE items SET deleted_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
}

func newPurgeFixture(t *testing.T) *purgeFixture {
	t.Helper()
	ctx := context.Background()
	s, clk := openTest(t)
	f := &purgeFixture{s: s, clk: clk}
	err := s.Tx(ctx, func(r *Repo) error {
		var err error
		if f.a, _, err = r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "aaaaaaaaaaa", URL: "u"}); err != nil {
			return err
		}
		if f.b, _, err = r.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "bbbbbbbbbbb", URL: "u"}); err != nil {
			return err
		}
		if err := r.UpdateItemMeta(ctx, f.a.ID, ItemMeta{Title: "PostgreSQL in 100 Seconds"}); err != nil {
			return err
		}
		if f.onlyASn, err = r.EnsureSnapshot(ctx, "sha-only-a", `{"a":1}`); err != nil {
			return err
		}
		if f.sharedSn, err = r.EnsureSnapshot(ctx, "sha-shared", `{}`); err != nil {
			return err
		}
		meta, err := r.InsertStageRun(ctx, NewStageRun{ItemID: f.a.ID, Stage: "metadata", Status: StatusPending, ConfigSnapshotID: f.onlyASn})
		if err != nil {
			return err
		}
		tr, err := r.InsertStageRun(ctx, NewStageRun{ItemID: f.a.ID, Stage: "transcript", Status: StatusPending, ConfigSnapshotID: f.sharedSn})
		if err != nil {
			return err
		}
		if _, err := r.InsertStageRun(ctx, NewStageRun{ItemID: f.b.ID, Stage: "metadata", Status: StatusPending, ConfigSnapshotID: f.sharedSn}); err != nil {
			return err
		}
		// 兩個版本的產出，stage_runs.artifact_id 指向現行版本：cascade 刪除時兩邊同時消失。
		for i := range 2 {
			a, err := r.InsertArtifact(ctx, NewArtifact{
				ItemID: f.a.ID, Kind: "transcript", Variant: "source", Lang: "en", Format: "notesteme-cues+json",
				Path: "artifacts/" + f.a.ID + "/x" + string(rune('0'+i)) + ".json", SHA256: "s", Size: int64(100 * (i + 1)),
				Provenance: json.RawMessage(`{"source":"x"}`),
			})
			if err != nil {
				return err
			}
			f.artA = append(f.artA, a)
		}
		if err := r.SetStageResult(ctx, tr.ID, StageResult{Executor: "local", CacheKey: "k", ImplVersion: 1, ArtifactID: f.artA[1].ID}); err != nil {
			return err
		}
		if err := r.InsertProbes(ctx, f.a.ID, []NewProbe{{SourceID: "youtube-manual", Lang: "en", Kind: "manual", Available: true, State: "used"}}); err != nil {
			return err
		}
		if f.taskA, err = r.InsertTask(ctx, NewTask{StageRunID: meta.ID, Kind: "metadata", Executor: "builtin", ResourceClass: "download"}); err != nil {
			return err
		}
		// A 的事件（item.created 在新增時已寫入）、不屬於任何影音的事件，以及 B 的事件。
		if err := r.InsertEvent(ctx, EventRecord{Type: "stage.status", ItemID: f.a.ID}); err != nil {
			return err
		}
		if err := r.InsertEvent(ctx, EventRecord{Type: "system.note"}); err != nil {
			return err
		}
		if err := r.InsertCostEntry(ctx, CostEntry{ItemID: f.a.ID, USD: 0.42, State: CostActual}); err != nil {
			return err
		}
		return r.InsertCostEntry(ctx, CostEntry{ItemID: f.b.ID, USD: 1, State: CostActual})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// 永久刪除之後，這部影音在資料庫中沒有任何殘留；別部影音與共用的資料不受影響。
func TestPurgeItemLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	f.trash(t, f.a.ID, f.s.clock.Now())
	var published []events.Event
	f.s.SetEventSink(func(ev events.Event) { published = append(published, ev) })
	// 事前確認：A 有事件可以清（新增與步驟狀態都會寫事件），B 的事件要原封不動。
	if n := f.count(t, `SELECT COUNT(*) FROM events WHERE item_id = ?`, f.a.ID); n < 2 {
		t.Fatalf("前置條件：A 應該有事件，實際 %d 筆", n)
	}
	bEvents := f.count(t, `SELECT COUNT(*) FROM events WHERE item_id = ?`, f.b.ID)

	got, err := f.s.Write().PurgeItem(ctx, f.a.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != f.a.ID || got.Ref != "yt:aaaaaaaaaaa" || got.Title != "PostgreSQL in 100 Seconds" {
		t.Errorf("PurgedItem = %+v", got)
	}

	for _, c := range []struct {
		what, query string
		args        []any
		want        int
	}{
		{"items", `SELECT COUNT(*) FROM items WHERE id = ?`, []any{f.a.ID}, 0},
		{"artifacts", `SELECT COUNT(*) FROM artifacts WHERE item_id = ?`, []any{f.a.ID}, 0},
		{"stage_runs（含指向產出的）", `SELECT COUNT(*) FROM stage_runs WHERE item_id = ?`, []any{f.a.ID}, 0},
		{"source_probes", `SELECT COUNT(*) FROM source_probes WHERE item_id = ?`, []any{f.a.ID}, 0},
		{"tasks", `SELECT COUNT(*) FROM tasks WHERE id = ?`, []any{f.taskA.ID}, 0},
		{"帶 item_id 的 events", `SELECT COUNT(*) FROM events WHERE item_id = ?`, []any{f.a.ID}, 0},
		{"只有 A 用的設定快照", `SELECT COUNT(*) FROM config_snapshots WHERE id = ?`, []any{f.onlyASn}, 0},
		// 其他資料不受影響。
		{"共用的設定快照", `SELECT COUNT(*) FROM config_snapshots WHERE id = ?`, []any{f.sharedSn}, 1},
		{"B 的影音", `SELECT COUNT(*) FROM items WHERE id = ?`, []any{f.b.ID}, 1},
		{"B 的步驟", `SELECT COUNT(*) FROM stage_runs WHERE item_id = ?`, []any{f.b.ID}, 1},
		{"B 的事件", `SELECT COUNT(*) FROM events WHERE item_id = ?`, []any{f.b.ID}, bEvents},
		{"不屬於影音的事件", `SELECT COUNT(*) FROM events WHERE type = 'system.note'`, nil, 1},
		{"成本帳本", `SELECT COUNT(*) FROM cost_ledger`, nil, 2},
	} {
		if n := f.count(t, c.query, c.args...); n != c.want {
			t.Errorf("%s：%d 筆，預期 %d", c.what, n, c.want)
		}
	}

	// item.purged：代號與標題寫在 payload，events 表的 item_id 留空；同時發布。
	var item, title string
	if err := f.s.r.QueryRowContext(ctx, `SELECT item_id, json_extract(payload_json, '$.item') || '|' || json_extract(payload_json, '$.title') FROM events WHERE type = 'item.purged'`).Scan(&item, &title); err != nil {
		t.Fatalf("item.purged 沒有寫入：%v", err)
	}
	if item != "" || title != "yt:aaaaaaaaaaa|PostgreSQL in 100 Seconds" {
		t.Errorf("item.purged：item_id = %q，payload = %q", item, title)
	}
	if len(published) != 1 || published[0].Type != events.TypeItemPurged || published[0].Item != "yt:aaaaaaaaaaa" ||
		published[0].Title != "PostgreSQL in 100 Seconds" || published[0].ItemID != "" {
		t.Errorf("發布的事件 = %+v", published)
	}
}

// 帳本保留：item_id 改為空，標題另存，金額不變。
func TestPurgeItemKeepsCostLedger(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	f.trash(t, f.a.ID, f.s.clock.Now())
	if _, err := f.s.Write().PurgeItem(ctx, f.a.ID, nil); err != nil {
		t.Fatal(err)
	}
	entries, err := f.s.Read().CostEntries(ctx)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if e := entries[0]; e.ItemID != "" || e.ItemTitle != "PostgreSQL in 100 Seconds" || e.USD != 0.42 || e.State != CostActual {
		t.Errorf("A 的帳本紀錄 = %+v", e)
	}
	if e := entries[1]; e.ItemID != f.b.ID || e.ItemTitle != "" || e.USD != 1 {
		t.Errorf("B 的帳本紀錄不該被改動：%+v", e)
	}
}

// tombstone 的鍵是 (platform, external_id, part, ”)；同一個鍵再次永久刪除只更新時間。
func TestPurgeItemWritesTombstoneOnce(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	f.trash(t, f.a.ID, f.s.clock.Now())
	purgedAt := f.s.clock.Now()
	if _, err := f.s.Write().PurgeItem(ctx, f.a.ID, nil); err != nil {
		t.Fatal(err)
	}
	var sha string
	var part, at int64
	if err := f.s.r.QueryRowContext(ctx, `SELECT part, sha256, purged_at FROM item_tombstones WHERE platform = 'youtube' AND external_id = 'aaaaaaaaaaa'`).Scan(&part, &sha, &at); err != nil {
		t.Fatalf("沒有 tombstone：%v", err)
	}
	if part != 0 || sha != "" || at != purgedAt.UnixMilli() {
		t.Errorf("tombstone = part %d, sha %q, at %d", part, sha, at)
	}

	// 同一部影音重新新增、再刪除、再永久刪除：仍然只有一列，時間更新。
	f.clk.Advance(time.Hour)
	again, _, err := f.s.Write().InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "aaaaaaaaaaa", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	f.trash(t, again.ID, f.s.clock.Now())
	if _, err := f.s.Write().PurgeItem(ctx, again.ID, nil); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM item_tombstones`); n != 1 {
		t.Errorf("tombstone 有 %d 列，預期 1", n)
	}
	if err := f.s.r.QueryRowContext(ctx, `SELECT purged_at FROM item_tombstones`).Scan(&at); err != nil || at != f.s.clock.Now().UnixMilli() {
		t.Errorf("purged_at = %d, %v", at, err)
	}
}

// 不在垃圾桶中的影音不能永久刪除，也不會留下任何改動。
func TestPurgeItemRefusesItemOutsideTrash(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	if _, err := f.s.Write().PurgeItem(ctx, f.a.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v，預期 ErrNotFound", err)
	}
	if _, err := f.s.Write().PurgeItem(ctx, "no-such-item", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的影音：err = %v，預期 ErrNotFound", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM items`); n != 2 {
		t.Errorf("items = %d", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM item_tombstones`); n != 0 {
		t.Errorf("tombstone = %d", n)
	}
}

// notAfter：影音在清單產生之後又被刪除（deleted_at 較新），不能照舊的名單永久刪除。
func TestPurgeItemRespectsNotAfter(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	deletedAt := f.s.clock.Now()
	f.trash(t, f.a.ID, deletedAt)

	before := deletedAt.Add(-time.Second)
	if _, err := f.s.Write().PurgeItem(ctx, f.a.ID, &before); !errors.Is(err, ErrNotFound) {
		t.Fatalf("尚未到期：err = %v，預期 ErrNotFound", err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM items WHERE id = ?`, f.a.ID); n != 1 {
		t.Fatal("尚未到期的影音被刪除了")
	}
	if _, err := f.s.Write().PurgeItem(ctx, f.a.ID, &deletedAt); err != nil { // 剛好等於：到期
		t.Fatalf("剛好到期：%v", err)
	}
}

// 交易中途失敗時全部回復：帳本、tombstone、事件、影音都維持原狀，也不發布 item.purged。
func TestPurgeItemRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	f.trash(t, f.a.ID, f.s.clock.Now())
	// 最後一個清除步驟（刪除沒有引用的快照）失敗，前面的步驟都已經執行過。
	if _, err := f.s.w.ExecContext(ctx, `CREATE TRIGGER fail_snapshot_delete BEFORE DELETE ON config_snapshots BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	var published []events.Event
	f.s.SetEventSink(func(ev events.Event) { published = append(published, ev) })
	aEvents := f.count(t, `SELECT COUNT(*) FROM events WHERE item_id = ?`, f.a.ID)

	if _, err := f.s.Write().PurgeItem(ctx, f.a.ID, nil); err == nil {
		t.Fatal("應該失敗")
	}
	for what, c := range map[string]struct {
		query string
		want  int
	}{
		"影音":          {`SELECT COUNT(*) FROM items WHERE id = '` + f.a.ID + `'`, 1},
		"產出":          {`SELECT COUNT(*) FROM artifacts WHERE item_id = '` + f.a.ID + `'`, 2},
		"事件":          {`SELECT COUNT(*) FROM events WHERE item_id = '` + f.a.ID + `'`, aEvents},
		"item.purged": {`SELECT COUNT(*) FROM events WHERE type = 'item.purged'`, 0},
		"tombstone":   {`SELECT COUNT(*) FROM item_tombstones`, 0},
		"仍連著影音的帳本紀錄":  {`SELECT COUNT(*) FROM cost_ledger WHERE item_id = '` + f.a.ID + `'`, 1},
		"已寫入標題的帳本紀錄（無）": {`SELECT COUNT(*) FROM cost_ledger WHERE item_title <> ''`, 0},
	} {
		if n := f.count(t, c.query); n != c.want {
			t.Errorf("回復後 %s：%d，預期 %d", what, n, c.want)
		}
	}
	if len(published) != 0 {
		t.Errorf("失敗時不該發布事件：%+v", published)
	}
}

func TestTrashIDsAndExpiredTrashIDs(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	now := f.s.clock.Now()
	f.trash(t, f.a.ID, now.Add(-30*24*time.Hour))
	f.trash(t, f.b.ID, now.Add(-time.Hour))

	all, err := f.s.Read().TrashIDs(ctx)
	if err != nil || len(all) != 2 || all[0] != f.a.ID || all[1] != f.b.ID {
		t.Fatalf("TrashIDs = %v, %v（刪除時間早的在前）", all, err)
	}
	// 剛好等於 cutoff 的算到期。
	exp, err := f.s.Read().ExpiredTrashIDs(ctx, now.Add(-30*24*time.Hour))
	if err != nil || len(exp) != 1 || exp[0] != f.a.ID {
		t.Fatalf("ExpiredTrashIDs = %v, %v", exp, err)
	}
	if exp, _ = f.s.Read().ExpiredTrashIDs(ctx, now.Add(-30*24*time.Hour-time.Millisecond)); len(exp) != 0 {
		t.Errorf("早 1 毫秒不該到期：%v", exp)
	}
	// 不在垃圾桶中的影音不列出。
	if _, err := f.s.w.ExecContext(ctx, `UPDATE items SET deleted_at = NULL`); err != nil {
		t.Fatal(err)
	}
	if all, _ = f.s.Read().TrashIDs(ctx); len(all) != 0 {
		t.Errorf("TrashIDs = %v", all)
	}
}

// 預覽統計：所有版本的產出數與大小，只加總 actual 的花費。
func TestDeletionStats(t *testing.T) {
	ctx := context.Background()
	f := newPurgeFixture(t)
	for _, e := range []CostEntry{
		{ItemID: f.a.ID, USD: 0.5, State: CostReserved},
		{ItemID: f.a.ID, USD: 0.25, State: CostUnknown},
		{ItemID: f.a.ID, USD: 0.08, State: CostActual},
	} {
		if err := f.s.Write().InsertCostEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.s.Read().DeletionStats(ctx, f.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Artifacts != 2 || got.Bytes != 300 || got.SpentUSD < 0.4999 || got.SpentUSD > 0.5001 {
		t.Errorf("DeletionStats = %+v，預期 2 個產出、300 位元組、0.5 美元（0.42 + 0.08）", got)
	}
	empty, err := f.s.Read().DeletionStats(ctx, f.b.ID)
	if err != nil || empty.Artifacts != 0 || empty.Bytes != 0 || empty.SpentUSD != 1 {
		t.Errorf("B = %+v, %v", empty, err)
	}
}

func TestCostEntriesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	if err := s.Write().InsertCostEntry(ctx, CostEntry{USD: 1, State: "bogus"}); !errors.Is(err, ErrInvalidCostState) {
		t.Fatalf("不認得的狀態：err = %v", err)
	}
	if err := s.Write().InsertCostEntry(ctx, CostEntry{ItemTitle: "x", USD: 2, State: CostActual}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute)
	if err := s.Write().InsertCostEntry(ctx, CostEntry{ItemTitle: "y", USD: 3, State: CostReserved}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().CostEntries(ctx)
	if err != nil || len(got) != 2 || got[0].ItemTitle != "x" || got[1].ItemTitle != "y" || got[0].ItemID != "" {
		t.Fatalf("entries = %+v, %v", got, err)
	}
	if !got[1].At.Equal(got[0].At.Add(time.Minute)) {
		t.Errorf("時間來自 clock：%v、%v", got[0].At, got[1].At)
	}
}
