package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// columnsOf 回傳資料表的欄位名稱。
func columnsOf(t *testing.T, s *Store, table string) []string {
	t.Helper()
	rows, err := s.w.QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func objectExists(t *testing.T, s *Store, typ, name string) bool {
	t.Helper()
	var n int
	if err := s.w.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, typ, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// 00009 升級：舊的影音取得預設值；降級：欄位、索引與表都移除，原有資料保留；可以再次升級。
func TestMigration00009UpDown(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := openWith(t, dir, migrationsUpTo(t, 8, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.ExecContext(ctx, `INSERT INTO items (id, platform, external_id, url, created_at) VALUES ('old', 'youtube', 'x', 'u', 1)`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = openWith(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	it, err := s.Read().ItemByID(ctx, "old")
	if err != nil || it.SourcePath != "" || it.ContentSHA256 != "" || it.Origin != OriginManual {
		t.Fatalf("升級後的舊影音 = %+v, %v", it, err)
	}
	for _, c := range []string{"source_path", "content_sha256", "origin"} {
		if !slices.Contains(columnsOf(t, s, "items"), c) {
			t.Errorf("items 缺少欄位 %s", c)
		}
	}
	if !objectExists(t, s, "table", "inbox_entries") || !objectExists(t, s, "index", "items_local_sha") {
		t.Fatal("升級後應有 inbox_entries 與 items_local_sha")
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, s.w, MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("降級：%v", err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 8 {
		t.Fatalf("降級後的版本 = %d, %v；預期 8", v, err)
	}
	for _, c := range []string{"source_path", "content_sha256", "origin"} {
		if slices.Contains(columnsOf(t, s, "items"), c) {
			t.Errorf("降級後 items 仍有欄位 %s", c)
		}
	}
	if objectExists(t, s, "table", "inbox_entries") || objectExists(t, s, "index", "items_local_sha") {
		t.Error("降級後不該留下 inbox_entries 或 items_local_sha")
	}
	var n int
	if err := s.w.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE id = 'old'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("降級後原有影音 = %d, %v", n, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("再次升級：%v", err)
	}
}

func newLocal(key, sha string) NewItem {
	return NewItem{Platform: "local", ExternalID: key, SourcePath: "/data/inbox/" + key, ContentSHA256: sha, Origin: OriginInbox}
}

// NewItem 的新欄位寫入並讀回；ID 由呼叫端提供時照用，空白時自動產生；Origin 空白為 manual。
func TestInsertLocalItemFields(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()

	it, created, err := w.InsertItemIfAbsent(ctx, NewItem{ID: "fixed-id", Platform: "local", ExternalID: "talk.m4a", SourcePath: "uploads/fixed-id/talk.m4a", ContentSHA256: shaA, Origin: OriginUpload})
	if err != nil || !created {
		t.Fatalf("created = %v, %v", created, err)
	}
	if it.ID != "fixed-id" || it.SourcePath != "uploads/fixed-id/talk.m4a" || it.ContentSHA256 != shaA || it.Origin != OriginUpload || it.URL != "" {
		t.Errorf("item = %+v", it)
	}

	auto, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if auto.ID == "" || auto.Origin != OriginManual || auto.SourcePath != "" || auto.ContentSHA256 != "" {
		t.Errorf("auto = %+v", auto)
	}

	// 同鍵再新增：回傳既有的影音，created 為 false，不改寫欄位。
	dup, created, err := w.InsertItemIfAbsent(ctx, NewItem{ID: "other", Platform: "local", ExternalID: "talk.m4a", ContentSHA256: shaB, Origin: OriginInbox})
	if err != nil || created || dup.ID != "fixed-id" || dup.ContentSHA256 != shaA || dup.Origin != OriginUpload {
		t.Errorf("dup = %+v, created = %v, %v", dup, created, err)
	}
}

// UpdateItemMeta 的 MediaKind 與 ContentSHA256：空字串保留原值，有值就寫入。
func TestUpdateItemMetaMediaKindAndSHA(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	it, _, err := w.InsertItemIfAbsent(ctx, newLocal("a.mp4", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateItemMeta(ctx, it.ID, ItemMeta{Title: "a", MediaKind: "video", ContentSHA256: shaA}); err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateItemMeta(ctx, it.ID, ItemMeta{Title: "a2"}); err != nil {
		t.Fatal(err)
	}
	got, _ := w.ItemByID(ctx, it.ID)
	if got.MediaKind != "video" || got.ContentSHA256 != shaA || got.Title != "a2" {
		t.Errorf("item = %+v", got)
	}
}

// PurgeItem 把影音的內容雜湊寫進 tombstone；平台影音仍是空字串。
func TestPurgeItemWritesSHAToTombstone(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	loc, _, err := w.InsertItemIfAbsent(ctx, newLocal("a.mp4", shaA))
	if err != nil {
		t.Fatal(err)
	}
	yt, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{loc.ID, yt.ID} {
		if _, _, err := w.TrashItem(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := w.PurgeItem(ctx, id, nil); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]string{}
	rows, err := s.r.QueryContext(ctx, `SELECT platform, sha256 FROM item_tombstones`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p, sha string
		if err := rows.Scan(&p, &sha); err != nil {
			t.Fatal(err)
		}
		got[p] = sha
	}
	if len(got) != 2 || got["local"] != shaA || got["youtube"] != "" {
		t.Errorf("tombstones = %v", got)
	}
}

// SnapshotUploadFiles 讀的是快照：快照之後才永久刪除或新增的上傳影音不影響結果。
func TestSnapshotUploadFilesReadsSnapshot(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	dir := t.TempDir()
	w := s.Write()
	up := func(id, name, sha string) {
		t.Helper()
		if _, _, err := w.InsertItemIfAbsent(ctx, NewItem{ID: id, Platform: "local", ExternalID: name, SourcePath: "uploads/" + id + "/" + name, ContentSHA256: sha, Origin: OriginUpload}); err != nil {
			t.Fatal(err)
		}
	}
	up("up-1", "a.m4a", shaA)
	if _, _, err := w.InsertItemIfAbsent(ctx, newLocal("inbox.mp4", shaB)); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "snap", "db.sqlite")
	if err := s.SnapshotTo(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.TrashItem(ctx, "up-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PurgeItem(ctx, "up-1", nil); err != nil {
		t.Fatal(err)
	}
	up("up-2", "b.m4a", shaB)

	got, err := SnapshotUploadFiles(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	want := []UploadFile{{ItemID: "up-1", SourcePath: "uploads/up-1/a.m4a", SHA256: shaA}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SnapshotUploadFiles = %+v，想要 %+v", got, want)
	}
	live, err := s.Read().UploadFiles(ctx)
	if err != nil || len(live) != 1 || live[0].ItemID != "up-2" {
		t.Errorf("UploadFiles = %+v, %v", live, err)
	}
}

func addTombstone(t *testing.T, s *Store, platform, externalID string, part int, sha string) {
	t.Helper()
	if _, err := s.w.ExecContext(context.Background(),
		`INSERT INTO item_tombstones (platform, external_id, part, sha256, purged_at) VALUES (?, ?, ?, ?, 1)`, platform, externalID, part, sha); err != nil {
		t.Fatal(err)
	}
}

// LocalTombstoneBlocks：sha 相同；同 external_id 且 tombstone 的 sha 為空；都不符。
func TestLocalTombstoneBlocks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	addTombstone(t, s, "local", "hashed.mp4", 0, shaA)
	addTombstone(t, s, "local", "unhashed.mp4", 0, "")
	addTombstone(t, s, "youtube", "yt-id", 0, "")
	r := s.Read()

	for _, c := range []struct {
		name, key, sha string
		want           bool
	}{
		{"sha 相同（換了檔名）", "renamed.mp4", shaA, true},
		{"同鍵但 tombstone 有 sha、這次 sha 不同", "hashed.mp4", shaB, false},
		{"同鍵且 tombstone 的 sha 為空", "unhashed.mp4", shaB, true},
		{"同鍵且 tombstone 的 sha 為空、這次沒有 sha", "unhashed.mp4", "", true},
		{"都不符", "fresh.mp4", shaB, false},
		{"沒有 sha 也沒有同鍵", "fresh.mp4", "", false},
		{"別的平台的 tombstone 不算", "yt-id", shaB, false},
	} {
		got, err := r.LocalTombstoneBlocks(ctx, c.key, c.sha)
		if err != nil || got != c.want {
			t.Errorf("%s：= %v, %v；預期 %v", c.name, got, err, c.want)
		}
	}
}

// ItemByLocalSHA 只找 platform 為 local 且 sha 非空的影音（含垃圾桶中的），多筆時取最早的。
func TestItemByLocalSHA(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	first, _, _ := w.InsertItemIfAbsent(ctx, newLocal("a.mp4", shaA))
	clk.Advance(time.Second)
	second, _, err := w.InsertItemIfAbsent(ctx, newLocal("copy.mp4", shaA))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.InsertItemIfAbsent(ctx, newLocal("nohash.mp4", "")); err != nil {
		t.Fatal(err)
	}
	// 別的平台即使 sha 相同也不算。
	other, _, _ := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "abc", URL: "u", ContentSHA256: shaB})
	_ = other

	got, err := w.ItemByLocalSHA(ctx, shaA)
	if err != nil || got.ID != first.ID {
		t.Errorf("= %+v, %v；預期最早的 %s", got, err, first.ID)
	}
	if _, _, err := w.TrashItem(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	// 沒有被刪的優先：最早的那筆在垃圾桶時，改取正常的那筆。
	if got, err := w.ItemByLocalSHA(ctx, shaA); err != nil || got.ID != second.ID || got.DeletedAt != nil {
		t.Errorf("沒有被刪的優先：%+v, %v；預期 %s", got, err, second.ID)
	}
	if _, _, err := w.TrashItem(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	// 都在垃圾桶時仍然找得到（最早的）。
	if got, err := w.ItemByLocalSHA(ctx, shaA); err != nil || got.ID != first.ID || got.DeletedAt == nil {
		t.Errorf("垃圾桶中的影音也要找到：%+v, %v", got, err)
	}
	for _, sha := range []string{shaB, "", "nope"} {
		if _, err := w.ItemByLocalSHA(ctx, sha); !errors.Is(err, ErrNotFound) {
			t.Errorf("sha %q：err = %v，預期 ErrNotFound", sha, err)
		}
	}
}

// SetItemContentSHA 只在雜湊為空時寫入。
func TestSetItemContentSHAWritesOnce(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	w := s.Write()
	it, _, _ := w.InsertItemIfAbsent(ctx, newLocal("a.mp4", ""))
	if err := w.SetItemContentSHA(ctx, it.ID, shaA); err != nil {
		t.Fatal(err)
	}
	if err := w.SetItemContentSHA(ctx, it.ID, shaB); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.ItemByID(ctx, it.ID); got.ContentSHA256 != shaA {
		t.Errorf("ContentSHA256 = %q，預期維持第一次寫入的值", got.ContentSHA256)
	}
}

// inbox_entries 的新增、更新（upsert）、讀取、刪除；影音永久刪除後 item_id 變成 NULL。
func TestInboxEntriesCRUD(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	it, _, _ := w.InsertItemIfAbsent(ctx, newLocal("talk.mp4", shaA))

	if m, err := w.InboxEntries(ctx); err != nil || len(m) != 0 {
		t.Fatalf("空的 inbox：%v, %v", m, err)
	}
	now := clk.Now()
	e := InboxEntry{Path: "talk.mp4", Size: 100, MtimeNS: 12345, SHA256: shaA, State: InboxIngested, ItemID: it.ID}
	if err := w.UpsertInboxEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	// 沒有 item 的紀錄：item_id 存 NULL，DecidedAt 可以指定。
	at := now.Add(-time.Hour)
	if err := w.UpsertInboxEntry(ctx, InboxEntry{Path: "sub/notes.pdf", Size: 5, MtimeNS: 1, State: InboxNotMedia, Reason: "沒有音訊或影片", DecidedAt: at}); err != nil {
		t.Fatal(err)
	}
	m, err := w.InboxEntries(ctx)
	if err != nil || len(m) != 2 {
		t.Fatalf("entries = %v, %v", m, err)
	}
	e.DecidedAt = now
	if m["talk.mp4"] != e {
		t.Errorf("talk.mp4 = %+v，預期 %+v", m["talk.mp4"], e)
	}
	if n := m["sub/notes.pdf"]; n.ItemID != "" || n.State != InboxNotMedia || n.Reason != "沒有音訊或影片" || !n.DecidedAt.Equal(at) {
		t.Errorf("notes.pdf = %+v", n)
	}
	var nulls int
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_entries WHERE item_id IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("item_id 為 NULL 的紀錄 = %d, %v", nulls, err)
	}

	// 同一個路徑再寫入 = 更新，不是新增。
	e2 := InboxEntry{Path: "talk.mp4", Size: 200, MtimeNS: 99, SHA256: shaB, State: InboxConflict, Reason: "內容改變", ItemID: it.ID}
	if err := w.UpsertInboxEntry(ctx, e2); err != nil {
		t.Fatal(err)
	}
	if m, _ = w.InboxEntries(ctx); len(m) != 2 || m["talk.mp4"].State != InboxConflict || m["talk.mp4"].Size != 200 || m["talk.mp4"].Reason != "內容改變" {
		t.Errorf("更新後 = %+v", m)
	}

	// 影音永久刪除：紀錄留著，item_id 變成 NULL。
	if _, _, err := w.TrashItem(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PurgeItem(ctx, it.ID, nil); err != nil {
		t.Fatal(err)
	}
	m, _ = w.InboxEntries(ctx)
	if got, ok := m["talk.mp4"]; !ok || got.ItemID != "" || got.State != InboxConflict {
		t.Errorf("永久刪除後 = %+v, %v", got, ok)
	}

	if err := w.DeleteInboxEntry(ctx, "talk.mp4"); err != nil {
		t.Fatal(err)
	}
	if err := w.DeleteInboxEntry(ctx, "missing"); err != nil {
		t.Errorf("刪除不存在的紀錄應該什麼都不做：%v", err)
	}
	if m, _ = w.InboxEntries(ctx); len(m) != 1 || m["sub/notes.pdf"].Path == "" {
		t.Errorf("刪除後 = %+v", m)
	}
}

// IngestLocal 的交易用法：同一個 Tx 內依序查同鍵、tombstone、sha，再新增；任何一步失敗全部回復。
func TestLocalLookupsInsideOneTx(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	addTombstone(t, s, "local", "gone.mp4", 0, shaB)

	try := func(key, sha string) (state string, created bool, err error) {
		err = s.Tx(ctx, func(r *Repo) error {
			if _, err := r.ItemByKey(ctx, "local", key, 0); err == nil {
				state = "same-key"
				return nil
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			if blocked, err := r.LocalTombstoneBlocks(ctx, key, sha); err != nil {
				return err
			} else if blocked {
				state = "tombstoned"
				return nil
			}
			if _, err := r.ItemByLocalSHA(ctx, sha); err == nil {
				state = "duplicate"
				return nil
			} else if !errors.Is(err, ErrNotFound) {
				return err
			}
			_, created, err = r.InsertItemIfAbsent(ctx, newLocal(key, sha))
			state = "ingested"
			return err
		})
		return
	}

	for _, c := range []struct {
		key, sha, want string
	}{
		{"a.mp4", shaA, "ingested"},
		{"a.mp4", shaA, "same-key"},
		{"b.mp4", shaA, "duplicate"},
		{"renamed.mp4", shaB, "tombstoned"},
	} {
		got, _, err := try(c.key, c.sha)
		if err != nil || got != c.want {
			t.Errorf("%s/%s = %q, %v；預期 %q", c.key, c.sha[:1], got, err, c.want)
		}
	}
	var n int
	if err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE platform = 'local'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("local 影音 = %d, %v；預期 1", n, err)
	}
}
