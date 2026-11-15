package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// purgeCanceller 記下要結束的任務 ID。
type purgeCanceller struct {
	mu  sync.Mutex
	ids []string
	err error
}

func (c *purgeCanceller) Cancel(_ context.Context, ids []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, ids...)
	return c.err
}

// rawDB 另開一條連線直接讀寫資料庫：這個切片只負責永久刪除，移到垃圾桶由 DEL-B1 實作，
// 所以測試用 SQL 直接標記 deleted_at，並用它確認資料庫中沒有殘留。
func (r *rig) rawDB() *sql.DB {
	r.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (r *rig) trashDirect(id string, at time.Time) {
	r.t.Helper()
	if _, err := r.rawDB().Exec(`UPDATE items SET deleted_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) count(query string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.rawDB().QueryRow(query, args...).Scan(&n); err != nil {
		r.t.Fatalf("%s：%v", query, err)
	}
	return n
}

// processed 新增影音、處理到 normalize 完成，並在資料目錄放好各種檔案：
// 處理產生的 artifacts、media 與 uploads 目錄中的檔案。
func (r *rig) processed(t *testing.T, url string) store.Item {
	t.Helper()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(url, nil, "")
	r.run()
	for _, sub := range []string{"media", "uploads"} {
		d := filepath.Join(r.dir, sub, it.ID, "nested")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "file.bin"), bytes.Repeat([]byte("x"), 1000), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return it
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// 永久刪除之後，資料庫與資料目錄都沒有這部影音的任何東西；別部影音、inbox 與 media_roots 的檔案原封不動。
func TestPurgeLeavesNothingBehind(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.processed(t, watch)
	if exists(filepath.Join(r.dir, "artifacts", it.ID)) == false {
		t.Fatal("前置條件：處理之後應該有 artifacts 目錄")
	}
	var published []events.Event
	r.st.SetEventSink(func(ev events.Event) { published = append(published, ev) })

	// 使用者自己的檔案：inbox、media_roots 與指向它們的符號連結（放在影音的 media 目錄中）。
	inbox := filepath.Join(r.dir, "inbox")
	root := t.TempDir()
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	own := []string{filepath.Join(inbox, "lecture.mp4"), filepath.Join(root, "talk.m4a")}
	for _, p := range own {
		if err := os.WriteFile(p, []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(own[1], filepath.Join(r.dir, "media", it.ID, "link.m4a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(r.dir, "uploads", it.ID, "root-link")); err != nil {
		t.Fatal(err)
	}
	// 別部影音的資料。
	other := r.ingest("https://www.youtube.com/watch?v=zzzzzzzzzzz", nil, "")
	otherDir := filepath.Join(r.dir, "media", other.ID)
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}

	r.trashDirect(it.ID, r.clk.Now())
	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}

	// 資料庫：影音、產出、步驟（含指向產出的）、來源紀錄、任務、帶 item_id 的事件都沒有了。
	for _, c := range []struct{ what, query string }{
		{"items", `SELECT COUNT(*) FROM items WHERE id = ?`},
		{"artifacts", `SELECT COUNT(*) FROM artifacts WHERE item_id = ?`},
		{"stage_runs", `SELECT COUNT(*) FROM stage_runs WHERE item_id = ?`},
		{"source_probes", `SELECT COUNT(*) FROM source_probes WHERE item_id = ?`},
		{"tasks", `SELECT COUNT(*) FROM tasks WHERE stage_run_id NOT IN (SELECT id FROM stage_runs)`},
		{"events", `SELECT COUNT(*) FROM events WHERE item_id = ?`},
	} {
		args := []any{it.ID}
		if strings.Contains(c.query, "NOT IN") {
			args = nil
		}
		if n := r.count(c.query, args...); n != 0 {
			t.Errorf("%s 還剩 %d 筆", c.what, n)
		}
	}
	if n := r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, other.ID); n != 1 {
		t.Error("別部影音被刪除了")
	}
	if n := r.count(`SELECT COUNT(*) FROM stage_runs WHERE item_id = ?`, other.ID); n == 0 {
		t.Error("別部影音的步驟被刪除了")
	}
	if n := r.count(`SELECT COUNT(*) FROM item_tombstones WHERE platform = 'youtube' AND external_id = ?`, it.ExternalID); n != 1 {
		t.Errorf("tombstone = %d 筆，預期 1", n)
	}

	// 檔案：三個目錄連同目錄本身都刪除。
	for _, sub := range itemDataDirs {
		if exists(filepath.Join(r.dir, sub, it.ID)) {
			t.Errorf("%s/%s 還在", sub, it.ID)
		}
	}
	// 使用者的檔案與別部影音的目錄不受影響（符號連結只刪連結本身）。
	for _, p := range append(own, otherDir) {
		if !exists(p) {
			t.Errorf("%s 不該被刪除", p)
		}
	}
	if !exists(inbox) || !exists(root) {
		t.Error("inbox 或 media_roots 本身不該被刪除")
	}

	// item.purged：代號與標題，沒有內部 ID。
	var purged []events.Event
	for _, ev := range published {
		if ev.Type == events.TypeItemPurged {
			purged = append(purged, ev)
		}
	}
	if len(purged) != 1 || purged[0].Item != "yt:abc123xxxxx" || purged[0].Title != "T" || purged[0].ItemID != "" {
		t.Errorf("item.purged = %+v", purged)
	}
}

// PurgeItem 回傳被刪除的影音的 ID、代號與標題；Purge 是只回傳錯誤的簡化版。
func TestPurgeItemReturnsIdentity(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.processed(t, watch)
	r.trashDirect(it.ID, r.clk.Now())
	got, err := r.p.PurgeItem(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != it.ID || got.Ref != "yt:abc123xxxxx" || got.Title != "T" {
		t.Errorf("PurgeItem = %+v", got)
	}
	if got, err := r.p.PurgeItem(ctx, it.ID); !errors.Is(err, store.ErrNotFound) || got != (PurgedItem{}) {
		t.Errorf("已經刪除：%+v, %v，預期空結果與 ErrNotFound", got, err)
	}
}

// 帳本在永久刪除之後仍在：item_id 為空、標題另存。
func TestPurgeKeepsCostLedger(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.processed(t, watch)
	if err := r.st.Write().InsertCostEntry(ctx, store.CostEntry{ItemID: it.ID, USD: 0.42, State: store.CostActual}); err != nil {
		t.Fatal(err)
	}
	r.trashDirect(it.ID, r.clk.Now())
	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.st.Read().CostEntries(ctx)
	if err != nil || len(got) != 1 || got[0].ItemID != "" || got[0].ItemTitle != "T" || got[0].USD != 0.42 {
		t.Fatalf("帳本 = %+v, %v", got, err)
	}
}

// 不在垃圾桶中的影音不能永久刪除：回 ErrNotFound，資料與檔案都不動。
func TestPurgeRefusesItemOutsideTrash(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.processed(t, watch)
	if err := r.p.Purge(ctx, it.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v，預期 ErrNotFound", err)
	}
	if err := r.p.Purge(ctx, "no-such-item"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的影音：err = %v，預期 ErrNotFound", err)
	}
	if n := r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, it.ID); n != 1 {
		t.Error("影音被刪除了")
	}
	for _, sub := range itemDataDirs {
		if !exists(filepath.Join(r.dir, sub, it.ID)) {
			t.Errorf("%s/%s 被刪除了", sub, it.ID)
		}
	}
}

// 永久刪除前先取消殘留的任務，並請 worker 結束執行中的。
func TestPurgeStopsLeftoverTasks(t *testing.T) {
	c := &purgeCanceller{}
	r := newRig(t)
	r.p.SetTaskCanceller(c)
	ctx := context.Background()
	it := r.ingest(watch, nil, "") // 還沒處理：metadata 有一個 pending 任務
	var taskID string
	if err := r.rawDB().QueryRow(`SELECT t.id FROM tasks t JOIN stage_runs s ON s.id = t.stage_run_id WHERE s.item_id = ?`, it.ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	r.trashDirect(it.ID, r.clk.Now())

	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if len(c.ids) != 1 || c.ids[0] != taskID {
		t.Errorf("要結束的任務 = %v，預期 [%s]", c.ids, taskID)
	}
	if n := r.count(`SELECT COUNT(*) FROM tasks WHERE id = ?`, taskID); n != 0 {
		t.Error("任務沒有隨影音刪除")
	}
}

// canceller 失敗（例如 ctx 取消、等不到任務結束）時不刪除任何東西。
func TestPurgeStopsWhenTasksCannotBeCancelled(t *testing.T) {
	c := &purgeCanceller{err: errors.New("stuck")}
	r := newRig(t)
	r.p.SetTaskCanceller(c)
	it := r.ingest(watch, nil, "")
	r.trashDirect(it.ID, r.clk.Now())
	if err := r.p.Purge(context.Background(), it.ID); err == nil {
		t.Fatal("等不到任務結束時應該失敗")
	}
	if n := r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, it.ID); n != 1 {
		t.Error("影音不該被刪除")
	}
}

// 刪檔失敗只記 log：資料庫已經清掉，永久刪除仍算成功。
func TestPurgeFileRemovalFailureIsOnlyLogged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目錄權限限制")
	}
	var logs bytes.Buffer
	r := newRig(t, func(d *Deps) { d.Log = slog.New(slog.NewTextHandler(&logs, nil)) })
	it := r.processed(t, watch)
	r.trashDirect(it.ID, r.clk.Now())
	parent := filepath.Join(r.dir, "media")
	if err := os.Chmod(parent, 0o500); err != nil { // 不能刪除其中的項目
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	if err := r.p.Purge(context.Background(), it.ID); err != nil {
		t.Fatalf("刪檔失敗不該讓永久刪除失敗：%v", err)
	}
	if n := r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, it.ID); n != 0 {
		t.Error("資料庫應該已經清掉")
	}
	if !strings.Contains(logs.String(), "永久刪除後清除檔案失敗") {
		t.Errorf("沒有記錄刪檔失敗：%s", logs.String())
	}
	if exists(filepath.Join(r.dir, "artifacts", it.ID)) {
		t.Error("其他目錄仍然要繼續刪除")
	}
}

// 路徑只用標準形式的 UUID 組出：其他形式（路徑穿越、大小寫不同、分隔符號）一律拒絕。
func TestItemDirOnlyAcceptsCanonicalUUID(t *testing.T) {
	const id = "0192f3a0-0000-7000-8000-000000000001"
	got, err := itemDir("/data", "media", id)
	if err != nil || got != filepath.Join("/data", "media", id) {
		t.Fatalf("itemDir = %q, %v", got, err)
	}
	for _, bad := range []string{
		"", ".", "..", "../etc", "media/../../etc", id + "/..", id + "/", "/" + id,
		strings.ToUpper(id), "{" + id + "}", "urn:uuid:" + id, strings.ReplaceAll(id, "-", ""), id + "\x00",
	} {
		if p, err := itemDir("/data", "media", bad); err == nil {
			t.Errorf("itemDir(%q) = %q，預期拒絕", bad, p)
		}
	}
	dirs, err := itemDirs("/data", id)
	if err != nil || len(dirs) != 3 {
		t.Fatalf("itemDirs = %v, %v", dirs, err)
	}
	for i, sub := range []string{"artifacts", "media", "uploads"} {
		if dirs[i] != filepath.Join("/data", sub, id) {
			t.Errorf("itemDirs[%d] = %q", i, dirs[i])
		}
	}
}

// 清空垃圾桶：只刪垃圾桶中的，回傳筆數；媒體庫中的影音不受影響。
func TestPurgeAllOnlyPurgesTrash(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.processed(t, watch)
	b := r.ingest("https://www.youtube.com/watch?v=bbbbbbbbbbb", nil, "")
	live := r.ingest("https://www.youtube.com/watch?v=ccccccccccc", nil, "")
	r.trashDirect(a.ID, r.clk.Now())
	r.trashDirect(b.ID, r.clk.Now())

	n, err := r.p.PurgeAll(ctx)
	if err != nil || n != 2 {
		t.Fatalf("PurgeAll = %d, %v，預期 2", n, err)
	}
	if got := r.count(`SELECT COUNT(*) FROM items`); got != 1 || r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, live.ID) != 1 {
		t.Errorf("應該只剩媒體庫中的影音，items = %d", got)
	}
	if n, err := r.p.PurgeAll(ctx); err != nil || n != 0 {
		t.Errorf("垃圾桶是空的：PurgeAll = %d, %v", n, err)
	}
}

// 到期清理：deleted_at 不晚於 now - retention 的才刪，剛好等於也算。
func TestPurgeExpiredBoundary(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	retention := 30 * 24 * time.Hour
	deletedAt := r.clk.Now()
	old := r.processed(t, watch)
	r.trashDirect(old.ID, deletedAt)
	fresh := r.ingest("https://www.youtube.com/watch?v=bbbbbbbbbbb", nil, "")
	r.trashDirect(fresh.ID, deletedAt.Add(time.Hour))
	live := r.ingest("https://www.youtube.com/watch?v=ccccccccccc", nil, "")

	// 差 1 毫秒：還沒到期。
	if n, err := r.p.PurgeExpired(ctx, deletedAt.Add(retention-time.Millisecond), retention); err != nil || n != 0 {
		t.Fatalf("差 1 毫秒：PurgeExpired = %d, %v，預期 0", n, err)
	}
	if r.count(`SELECT COUNT(*) FROM items`) != 3 {
		t.Fatal("尚未到期不該刪除任何影音")
	}
	// 剛好滿 30 天：old 到期，fresh（晚一小時刪除）還沒。
	if n, err := r.p.PurgeExpired(ctx, deletedAt.Add(retention), retention); err != nil || n != 1 {
		t.Fatalf("剛好到期：PurgeExpired = %d, %v，預期 1", n, err)
	}
	if r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, old.ID) != 0 || exists(filepath.Join(r.dir, "media", old.ID)) {
		t.Error("到期的影音與檔案應該刪除")
	}
	if r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, fresh.ID) != 1 || r.count(`SELECT COUNT(*) FROM items WHERE id = ?`, live.ID) != 1 {
		t.Error("未到期的與媒體庫中的影音不該刪除")
	}
	// 保留天數短一點，fresh 也到期。
	if n, err := r.p.PurgeExpired(ctx, deletedAt.Add(2*time.Hour), time.Hour); err != nil || n != 1 {
		t.Fatalf("保留 1 小時：PurgeExpired = %d, %v，預期 1", n, err)
	}
}

// 預覽：所有版本的產出數、產出加 media 與 uploads 的大小、只加總 actual 的花費；而且不改變任何資料。
func TestDeletionPreviewIsAccurateAndReadOnly(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.processed(t, watch) // media 與 uploads 各 1000 位元組
	for _, e := range []store.CostEntry{
		{ItemID: it.ID, USD: 0.25, State: store.CostActual},
		{ItemID: it.ID, USD: 0.5, State: store.CostReserved},
		{ItemID: it.ID, USD: 0.125, State: store.CostActual},
		{ItemID: it.ID, USD: 9, State: store.CostUnknown},
	} {
		if err := r.st.Write().InsertCostEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	arts, err := r.st.Read().CurrentArtifacts(ctx, it.ID, "")
	if err != nil || len(arts) == 0 {
		t.Fatalf("前置條件：應該有產出 %v, %v", arts, err)
	}
	var artBytes int64
	for _, a := range arts {
		artBytes += a.Size
	}
	tables := []string{"items", "artifacts", "stage_runs", "tasks", "events", "cost_ledger", "item_tombstones", "config_snapshots", "source_probes"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = r.count(`SELECT COUNT(*) FROM ` + tb)
	}
	var published int
	r.st.SetEventSink(func(events.Event) { published++ })

	got, err := r.p.DeletionPreview(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Artifacts != len(arts) {
		t.Errorf("Artifacts = %d，預期 %d", got.Artifacts, len(arts))
	}
	if want := artBytes + 2000; got.Bytes != want {
		t.Errorf("Bytes = %d，預期 %d（產出 %d + media 1000 + uploads 1000）", got.Bytes, want, artBytes)
	}
	if got.SpentUSD != 0.375 {
		t.Errorf("SpentUSD = %v，預期 0.375（只算 actual）", got.SpentUSD)
	}
	if got.Outputs == nil || len(got.Outputs) != 0 {
		t.Errorf("Outputs = %#v，預期空的非 nil 清單", got.Outputs)
	}

	for _, tb := range tables {
		if n := r.count(`SELECT COUNT(*) FROM ` + tb); n != before[tb] {
			t.Errorf("預覽改變了 %s：%d → %d", tb, before[tb], n)
		}
	}
	if published != 0 {
		t.Errorf("預覽發布了 %d 個事件", published)
	}
	var deleted *int64
	if err := r.rawDB().QueryRow(`SELECT deleted_at FROM items WHERE id = ?`, it.ID).Scan(&deleted); err != nil || deleted != nil {
		t.Errorf("deleted_at = %v, %v", deleted, err)
	}
}

// 沒有產出、沒有檔案、沒有花費時全是 0。
func TestDeletionPreviewOfEmptyItem(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "")
	got, err := r.p.DeletionPreview(context.Background(), it.ID)
	if err != nil || got.Artifacts != 0 || got.Bytes != 0 || got.SpentUSD != 0 {
		t.Fatalf("preview = %+v, %v", got, err)
	}
}

// 影音在垃圾桶中時預覽回 *InTrashError，不存在時回 ErrNotFound。
func TestDeletionPreviewErrors(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	r.trashDirect(it.ID, r.clk.Now())
	_, err := r.p.DeletionPreview(ctx, it.ID)
	var tr *InTrashError
	if !errors.Is(err, ErrInTrash) || !errors.As(err, &tr) || tr.ItemID != it.ID {
		t.Errorf("垃圾桶中的影音：err = %v", err)
	}
	if _, err := r.p.DeletionPreview(ctx, "no-such-item"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音：err = %v", err)
	}
}
