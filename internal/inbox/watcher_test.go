package inbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/inbox/fakemedia"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

type rig struct {
	t     *testing.T
	clk   *clock.Fake
	dir   string // 資料目錄
	inbox string
	st    *store.Store
	p     *pipeline.Pipeline
	w     *Watcher
	probe *fakemedia.Runner
}

func newRig(t *testing.T) *rig {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC))
	dir := filepath.Join(base, "data")
	in := filepath.Join(base, "inbox")
	for _, d := range []string{dir, in} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	tool, probe := fakemedia.New(t)
	files := pathguard.New([]string{in}).With(filepath.Join(dir, "uploads"))
	p := pipeline.New(pipeline.Deps{Store: st, Clock: clk, DataDir: dir, Media: tool, Files: files})
	w := &Watcher{Clock: clk, Inbox: in, Files: files, Media: tool, Store: st, Ingester: p}
	return &rig{t: t, clk: clk, dir: dir, inbox: in, st: st, p: p, w: w, probe: probe}
}

// put 寫入 inbox 中的檔案，修改時間設為假時鐘「現在」往前 age。
func (r *rig) put(rel, content string, age time.Duration) {
	r.t.Helper()
	p := filepath.Join(r.inbox, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	mt := r.clk.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) run() {
	r.t.Helper()
	if err := r.w.Run(context.Background()); err != nil {
		r.t.Fatalf("Run：%v", err)
	}
}

func (r *rig) item(rel string) (store.Item, bool) {
	r.t.Helper()
	it, err := r.st.Read().ItemByKey(context.Background(), "local", rel, 0)
	if errors.Is(err, store.ErrNotFound) {
		return store.Item{}, false
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return it, true
}

func (r *rig) entry(rel string) (store.InboxEntry, bool) {
	r.t.Helper()
	m, err := r.st.Read().InboxEntries(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	e, ok := m[rel]
	return e, ok
}

func (r *rig) itemCount() int {
	r.t.Helper()
	items, err := r.p.Items(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return len(items)
}

const video = "VIDEO-content-A"

// 每輪的處理上限只算還沒判定的檔案：已經新增過、仍留在 inbox 中的檔案不佔名額，
// 超過上限的檔案在之後的掃描輪到，不會永遠被前面的檔案擋住。
func TestScanCapSkipsDecidedFiles(t *testing.T) {
	oldFiles := maxFiles
	maxFiles = 2
	t.Cleanup(func() { maxFiles = oldFiles })
	r := newRig(t)
	for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		r.put(name, video+name, time.Minute)
	}
	r.run()
	if n := r.itemCount(); n != 2 {
		t.Fatalf("第一輪應該只處理 2 個：%d", n)
	}
	r.run()
	if n := r.itemCount(); n != 3 {
		t.Fatalf("第二輪應該處理剩下的檔案：%d", n)
	}
	// 已經判定的檔案仍在 inbox 中：紀錄保留。
	for _, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		if _, ok := r.entry(name); !ok {
			t.Errorf("%s 的紀錄不見了", name)
		}
	}
}

// 走訪上限：超過時清單不完整，消失檔案的紀錄不刪（避免誤刪沒走訪到的檔案）。
func TestScanWalkCapKeepsEntries(t *testing.T) {
	r := newRig(t)
	r.put("a.mp4", video+"a", time.Minute)
	r.put("b.mp4", video+"b", time.Minute)
	r.run()
	oldWalk := maxWalk
	maxWalk = 1
	t.Cleanup(func() { maxWalk = oldWalk })
	r.run()
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if _, ok := r.entry(name); !ok {
			t.Errorf("%s 的紀錄不應被刪除", name)
		}
	}
}

// 第一次看到時修改時間已經很久以前：馬上穩定，直接新增，origin 為 inbox。
func TestStableOnFirstSight(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, time.Minute)
	r.run()
	it, ok := r.item("talk.mp4")
	if !ok {
		t.Fatal("應該新增影音")
	}
	if it.Origin != store.OriginInbox || it.SourcePath != filepath.Join(r.inbox, "talk.mp4") || it.ContentSHA256 == "" {
		t.Errorf("item = %+v", it)
	}
	e, ok := r.entry("talk.mp4")
	if !ok || e.State != store.InboxIngested || e.ItemID != it.ID || e.SHA256 != it.ContentSHA256 {
		t.Errorf("entry = %+v", e)
	}
}

// 剛寫入的檔案：5 秒還不穩定，15 秒才新增（ING-R10，穩定時間 10 秒）。
func TestStabilityWindow(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, 0)
	r.run()
	r.clk.Advance(5 * time.Second)
	r.run()
	if _, ok := r.item("talk.mp4"); ok {
		t.Fatal("5 秒時不應新增")
	}
	if n := len(r.probe.Calls()); n != 0 {
		t.Errorf("不穩定的檔案不該探測，探測了 %d 次", n)
	}
	r.clk.Advance(10 * time.Second)
	r.run()
	if _, ok := r.item("talk.mp4"); !ok {
		t.Fatal("15 秒時應該新增")
	}
}

// 剛好 10 秒就穩定。
func TestStableAtExactlyTenSeconds(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, 0)
	r.run()
	r.clk.Advance(StableFor - time.Nanosecond)
	r.run()
	if _, ok := r.item("talk.mp4"); ok {
		t.Fatal("差一點 10 秒不應新增")
	}
	r.clk.Advance(time.Nanosecond)
	r.run()
	if _, ok := r.item("talk.mp4"); !ok {
		t.Fatal("滿 10 秒應該新增")
	}
}

// 大小改變之後重新計時。
func TestSizeChangeRestartsTimer(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, 0)
	r.run()
	r.clk.Advance(8 * time.Second)
	r.put("talk.mp4", video+"-more", 0) // 還在複製：大小與修改時間都變
	r.run()
	r.clk.Advance(8 * time.Second) // 距離上次改變只有 8 秒
	r.run()
	if _, ok := r.item("talk.mp4"); ok {
		t.Fatal("大小改變後重新計時，8 秒時不應新增")
	}
	r.clk.Advance(3 * time.Second)
	r.run()
	if _, ok := r.item("talk.mp4"); !ok {
		t.Fatal("距離上次改變 11 秒，應該新增")
	}
}

// 內容相同的檔案只新增一次，另一個記為 duplicate。
func TestDuplicateContentIngestedOnce(t *testing.T) {
	r := newRig(t)
	r.put("a.mp4", video, 2*time.Minute)
	r.put("b.mp4", video, time.Minute)
	r.run()
	if n := r.itemCount(); n != 1 {
		t.Fatalf("影音數 = %d，要 1", n)
	}
	a, _ := r.entry("a.mp4")
	b, _ := r.entry("b.mp4")
	if a.State != store.InboxIngested || b.State != store.InboxDuplicate || b.ItemID != a.ItemID || b.Reason == "" {
		t.Errorf("a = %+v\nb = %+v", a, b)
	}
}

// 不是影音：留在原處、記錄原因、不建立影音，之後不重複探測。
func TestNotMediaStaysInPlace(t *testing.T) {
	r := newRig(t)
	r.put("notes.pdf", "just some text", time.Minute)
	r.run()
	if r.itemCount() != 0 {
		t.Fatal("不應建立影音")
	}
	e, ok := r.entry("notes.pdf")
	if !ok || e.State != store.InboxNotMedia || !strings.HasPrefix(e.Reason, "沒有音訊或影片：") {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(r.inbox, "notes.pdf")); err != nil {
		t.Errorf("檔案應該留在原處：%v", err)
	}
	r.clk.Advance(time.Minute)
	r.run()
	if n := len(r.probe.Calls()); n != 1 {
		t.Errorf("同樣的 (size, mtime) 不該重複探測，共探測 %d 次", n)
	}
}

// 工具失敗（逾時、無法執行）不寫紀錄，下一輪重試。
func TestProbeFailureRetriesNextRound(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, time.Minute)
	r.probe.SetErr(errors.New("ffprobe 逾時"))
	r.run()
	if _, ok := r.entry("talk.mp4"); ok || r.itemCount() != 0 {
		t.Fatal("工具失敗時不該寫紀錄或建立影音")
	}
	r.probe.SetErr(nil)
	r.run()
	if _, ok := r.item("talk.mp4"); !ok {
		t.Fatal("工具恢復後應該新增")
	}
}

// 永久刪除過的檔案再出現在 inbox：不新增，而且不移除 tombstone（DEL-R11）。
func TestTombstonedDoesNotRemoveTombstone(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.put("talk.mp4", video, time.Minute)
	r.run()
	it, _ := r.item("talk.mp4")
	if _, err := r.p.Trash(ctx, it.ID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Purge(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	// 檔案消失 → 紀錄被清掉；同樣內容再出現。
	if err := os.Remove(filepath.Join(r.inbox, "talk.mp4")); err != nil {
		t.Fatal(err)
	}
	r.run()
	if _, ok := r.entry("talk.mp4"); ok {
		t.Fatal("檔案消失後應該刪除紀錄")
	}
	r.put("talk.mp4", video, time.Minute)
	r.run()
	if _, ok := r.item("talk.mp4"); ok {
		t.Fatal("tombstone 擋住，不應重新新增")
	}
	e, _ := r.entry("talk.mp4")
	if e.State != store.InboxTombstoned || e.Reason == "" {
		t.Errorf("entry = %+v", e)
	}
	blocked, err := r.st.Read().LocalTombstoneBlocks(ctx, "talk.mp4", e.SHA256)
	if err != nil || !blocked {
		t.Errorf("tombstone 應該還在：%v %v", blocked, err)
	}
}

// 檔案消失：刪除 entry 與記憶體狀態，影音保留。
func TestVanishedFileDeletesEntry(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, time.Minute)
	r.run()
	if err := os.Remove(filepath.Join(r.inbox, "talk.mp4")); err != nil {
		t.Fatal(err)
	}
	r.run()
	if _, ok := r.entry("talk.mp4"); ok {
		t.Error("紀錄應該刪除")
	}
	if _, ok := r.item("talk.mp4"); !ok {
		t.Error("影音應該保留")
	}
	if _, ok := r.w.seen["talk.mp4"]; ok {
		t.Error("記憶體狀態應該刪除")
	}
}

// 探測與雜湊期間檔案被改動：不新增，重新計時。
func TestChangeDuringProcessingRestartsTimer(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, time.Minute)
	changed := false
	r.probe.OnProbe = func(path string) {
		if changed {
			return
		}
		changed = true
		mt := r.clk.Now().Add(-time.Second)
		if err := os.WriteFile(path, []byte(video+"-changed"), 0o644); err != nil {
			t.Error(err)
		}
		_ = os.Chtimes(path, mt, mt)
	}
	r.run()
	if _, ok := r.item("talk.mp4"); ok {
		t.Fatal("處理期間檔案被改動，不應新增")
	}
	if _, ok := r.entry("talk.mp4"); ok {
		t.Fatal("不應寫入紀錄")
	}
	// 重新計時：10 秒後穩定。
	r.run() // 檔案改動時已重設起算點（現在），還不穩定
	if _, ok := r.item("talk.mp4"); ok {
		t.Fatal("剛改動，尚未穩定")
	}
	r.clk.Advance(StableFor)
	r.run()
	if _, ok := r.item("talk.mp4"); !ok {
		t.Fatal("穩定後應該新增")
	}
}

// 略過隱藏檔、下載中與暫存檔、字幕附屬檔、隱藏目錄與 symlink；子目錄的鍵是相對路徑。
func TestSkippedNames(t *testing.T) {
	r := newRig(t)
	for _, rel := range []string{".hidden.mp4", "a.mp4.part", "b.crdownload", "c.tmp", "d.download", "e.mp4~",
		"f.srt", "g.vtt", "h.ass", "i.ssa", "j.sub", "k.idx", ".stfolder/l.mp4", "talks/.m.mp4"} {
		r.put(rel, video, time.Minute)
	}
	if err := os.Symlink(filepath.Join(r.inbox, "talks", ".m.mp4"), filepath.Join(r.inbox, "link.mp4")); err != nil {
		t.Skipf("無法建立 symlink：%v", err)
	}
	r.put("talks/keynote.mp4", video+"-keynote", time.Minute)
	r.run()
	if n := r.itemCount(); n != 1 {
		t.Fatalf("影音數 = %d，要 1（只有 talks/keynote.mp4）", n)
	}
	it, ok := r.item("talks/keynote.mp4")
	if !ok || it.SourcePath != filepath.Join(r.inbox, "talks", "keynote.mp4") {
		t.Errorf("item = %+v %v", it, ok)
	}
	if n := len(r.probe.Calls()); n != 1 {
		t.Errorf("只該探測 1 個檔案，探測了 %v", r.probe.Calls())
	}
}

// inbox 目錄不存在（例如還沒掛載）：不是錯誤，也不刪除既有紀錄。
func TestMissingInboxKeepsEntries(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, time.Minute)
	r.run()
	if err := os.RemoveAll(r.inbox); err != nil {
		t.Fatal(err)
	}
	r.run()
	if _, ok := r.entry("talk.mp4"); !ok {
		t.Error("inbox 不存在時不該刪除紀錄")
	}
}

// 同一個路徑的內容被換掉：記為 conflict，不自動重新處理；之後不重複判定。
func TestReplacedContentIsConflict(t *testing.T) {
	r := newRig(t)
	r.put("talk.mp4", video, time.Hour)
	r.run()
	r.put("talk.mp4", "VIDEO-something-else", time.Minute)
	r.run() // 內容改變：重新計時
	r.clk.Advance(StableFor)
	r.run()
	e, _ := r.entry("talk.mp4")
	if e.State != store.InboxConflict {
		t.Fatalf("entry = %+v", e)
	}
	if n := r.itemCount(); n != 1 {
		t.Errorf("影音數 = %d，要 1", n)
	}
	it, _ := r.item("talk.mp4")
	if e.SHA256 == it.ContentSHA256 {
		t.Error("紀錄應該是新內容的雜湊")
	}
}

// 垃圾桶中的影音：記為 in_trash，不新增也不還原。
func TestInTrashRecorded(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.put("talk.mp4", video, time.Hour)
	r.run()
	it, _ := r.item("talk.mp4")
	if _, err := r.p.Trash(ctx, it.ID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	// 同路徑換成同內容但新的修改時間：重新判定。
	r.put("talk.mp4", video, time.Minute)
	r.run() // 修改時間改變：重新計時
	r.clk.Advance(StableFor)
	r.run()
	e, _ := r.entry("talk.mp4")
	if e.State != store.InboxInTrash {
		t.Fatalf("entry = %+v", e)
	}
}

// NextAt：掃描之後 5 秒；沒掃描過時沒有排定。
func TestNextAt(t *testing.T) {
	r := newRig(t)
	if _, ok, _ := r.w.NextAt(context.Background()); ok {
		t.Fatal("還沒掃描時不該排定")
	}
	r.run()
	at, ok, err := r.w.NextAt(context.Background())
	if err != nil || !ok || !at.Equal(r.clk.Now().Add(PollInterval)) {
		t.Errorf("NextAt = %v %v %v", at, ok, err)
	}
}

// Loop：立刻掃描一次，之後每 5 秒（假時鐘）掃描，ctx 取消時結束。
func TestLoop(t *testing.T) {
	r := newRig(t)
	r.put("a.mp4", video, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.w.Loop(ctx) }()
	waitFor(t, func() bool { _, ok := r.item("a.mp4"); return ok && r.clk.PendingTimers() == 1 })

	r.put("b.mp4", "VIDEO-b", time.Minute)
	r.clk.Advance(PollInterval)
	waitFor(t, func() bool { _, ok := r.item("b.mp4"); return ok })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消後 Loop 應該結束")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second) // 只用來等 goroutine 排程，不影響被測的時間邏輯
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("等待條件逾時")
		}
		time.Sleep(time.Millisecond)
	}
}
