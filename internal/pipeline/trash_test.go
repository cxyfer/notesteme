package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func subtitleStub() subtitle.Transcript {
	return subtitle.Transcript{Lang: "en", Variant: subtitle.VariantSource, Cues: []subtitle.Cue{{StartMS: 0, EndMS: 1000, Text: "hi"}}}
}

// fakeCanceller 記錄被要求結束的任務；check 在 Cancel 被呼叫的當下執行，用來驗證順序。
type fakeCanceller struct {
	got   []string
	check func()
	err   error
}

func (f *fakeCanceller) Cancel(_ context.Context, ids []string) error {
	f.got = ids
	if f.check != nil {
		f.check()
	}
	return f.err
}

// recordEvents 記錄 store 發布的事件。
func recordEvents(st *store.Store) func() []events.Event {
	var mu sync.Mutex
	var got []events.Event
	st.SetEventSink(func(ev events.Event) {
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

// writeMedia 在媒體快取目錄寫一個檔案，回傳目錄。
func writeMedia(t *testing.T, dataDir, itemID string) string {
	t.Helper()
	dir := MediaCacheDir(dataDir, itemID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audio.m4a"), []byte("AUDIO"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// 移到垃圾桶：一個交易內設定 deleted_at、取消步驟（skipped、cancelled、動作「重試」）與任務，並寫入事件。
func TestTrashCancelsStagesAndTasks(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	got := recordEvents(r.st)
	it := r.ingest(watch, nil, "")
	r.run() // metadata 完成，transcript 停在 blocked
	got()

	res, err := r.p.Trash(ctx, it.ID, TrashOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != it.ID || res.Ref != "yt:abc123xxxxx" || !res.DeletedAt.Equal(r.clk.Now()) || len(res.KeptOutputs) != 0 {
		t.Errorf("結果 = %+v", res)
	}
	if want := res.DeletedAt.Add(DefaultTrashRetention); !res.PurgeAt.Equal(want) {
		t.Errorf("PurgeAt = %v，預期 %v（預設保留 30 天）", res.PurgeAt, want)
	}
	now, _ := r.st.Read().ItemByID(ctx, it.ID)
	if now.DeletedAt == nil {
		t.Fatal("影音應該有 deleted_at")
	}
	if run := r.stage(it, StageMetadata); run.Status != store.StatusDone {
		t.Errorf("已完成的 metadata 不應改動：%+v", run)
	}
	for _, s := range []string{StageTranscript, StageNormalize} {
		run := r.stage(it, s)
		if run.Status != store.StatusSkipped || run.BlockReason != "cancelled" || len(run.Actions) != 1 || run.Actions[0] != "重試" {
			t.Errorf("步驟 %s 應為 skipped（cancelled）並提供「重試」：%+v", s, run)
		}
	}
	var deleted []events.Event
	for _, ev := range got() {
		if ev.Type == events.TypeItemDeleted {
			deleted = append(deleted, ev)
		}
	}
	if len(deleted) != 1 || deleted[0].Item != "yt:abc123xxxxx" || deleted[0].ItemID != it.ID || deleted[0].Title != "T" {
		t.Errorf("item.deleted 事件 = %+v", deleted)
	}
}

// 排隊中的任務改成 cancelled，worker 之後不會再領取。
func TestTrashCancelsPendingTask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	task, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageMetadata).ID)
	if err != nil || task.State != store.TaskCancelled {
		t.Fatalf("任務 = %+v, %v，預期 cancelled", task, err)
	}
	r.run()
	if r.yt.probes != 0 {
		t.Errorf("垃圾桶中的影音不應再被處理，Probe 被呼叫 %d 次", r.yt.probes)
	}
}

// 執行中的任務：commit 之後才交給 canceller 結束（此時資料庫已經是垃圾桶），之後才刪媒體快取。
func TestTrashStopsRunningTaskThenFreesMedia(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	media := writeMedia(t, r.dir, it.ID)
	other := writeMedia(t, r.dir, "0192f3a0-0000-7000-8000-0000000000aa")
	artDir := filepath.Join(r.dir, artifactsDir, it.ID)
	if err := os.MkdirAll(artDir, 0o700); err != nil {
		t.Fatal(err)
	}

	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	fc := &fakeCanceller{}
	fc.check = func() {
		if cur, _ := r.st.Read().ItemByID(ctx, it.ID); cur.DeletedAt == nil {
			t.Error("Cancel 被呼叫時，交易應該已經 commit")
		}
		if !exists(media) {
			t.Error("要先結束任務，才能刪除媒體快取")
		}
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{Canceller: fc}); err != nil {
		t.Fatal(err)
	}
	if len(fc.got) != 1 || fc.got[0] != claimed[0].ID {
		t.Errorf("要結束的任務 = %v，預期 [%s]", fc.got, claimed[0].ID)
	}
	if exists(media) {
		t.Error("媒體快取應該已刪除")
	}
	if !exists(other) {
		t.Error("別部影音的媒體快取不應被刪")
	}
	if !exists(artDir) {
		t.Error("產出在垃圾桶期間要保留，直到永久刪除")
	}
	// 晚到的寫回被丟棄：步驟維持 skipped。
	if err := r.p.Handle(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}
	if run := r.stage(it, StageMetadata); run.Status != store.StatusSkipped {
		t.Errorf("晚到的結果不應蓋掉取消：%+v", run)
	}
}

// 等待任務結束失敗（逾時）不影響刪除：記錄後照常刪除媒體快取。
func TestTrashStillFreesMediaWhenCancelFails(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	media := writeMedia(t, r.dir, it.ID)
	if _, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{Canceller: &fakeCanceller{err: context.DeadlineExceeded}}); err != nil {
		t.Fatalf("Cancel 失敗不應讓刪除失敗：%v", err)
	}
	if exists(media) {
		t.Error("媒體快取應該已刪除")
	}
}

// 沒有任務要結束時不呼叫 canceller。
func TestTrashWithoutTasksSkipsCanceller(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	fc := &fakeCanceller{check: func() { t.Error("沒有要結束的任務，不該呼叫 Cancel") }}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{Canceller: fc}); err != nil {
		t.Fatal(err)
	}
	// 已完成的步驟不改動，連同它的產出。
	for _, s := range Stages {
		if run := r.stage(it, s); run.Status != store.StatusDone {
			t.Errorf("已完成的步驟 %s 不應改動：%+v", s, run)
		}
	}
}

func TestTrashTwiceAndUnknown(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := r.p.Trash(ctx, it.ID, TrashOptions{})
	var tr *InTrashError
	if !errors.Is(err, ErrInTrash) || !errors.As(err, &tr) || tr.ItemID != it.ID {
		t.Errorf("再刪一次 = %v，預期 *InTrashError", err)
	}
	if _, err := r.p.Trash(ctx, "no-such-item", TrashOptions{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的影音 = %v，預期 ErrNotFound", err)
	}
}

// 垃圾桶中的影音：所有讀取與處理的入口都回 *InTrashError（DEL-R1）。
func TestItemsInTrashAreRejectedEverywhere(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"Item": func() error { _, err := r.p.Item(ctx, it.ID); return err },
		"Runs": func() error { _, err := r.p.Runs(ctx, it.ID); return err },
		"Cues": func() error { _, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1}); return err },
		"CuesWithItem": func() error {
			_, _, err := r.p.CuesWithItem(ctx, it.ID, CuesQuery{ToMS: -1})
			return err
		},
		"Rerun":      func() error { _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize}); return err },
		"CancelItem": func() error { _, err := r.p.CancelItem(ctx, it.ID); return err },
	}
	for name, call := range calls {
		err := call()
		var tr *InTrashError
		if !errors.Is(err, ErrInTrash) || !errors.As(err, &tr) || tr.ItemID != it.ID {
			t.Errorf("%s = %v，預期 *InTrashError（item_id %s）", name, err, it.ID)
		}
	}
	items, err := r.p.Items(ctx)
	if err != nil || len(items) != 0 {
		t.Errorf("媒體庫列表 = %v, %v，不應含垃圾桶中的影音", items, err)
	}
}

// 還原：回到媒體庫，被取消的步驟維持 skipped 並提供「重試」，字幕仍然讀得到。
func TestRestoreBringsItemBackWithItsTranscript(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	got := recordEvents(r.st)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	got()

	back, err := r.p.Restore(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != it.ID || back.Ref != "yt:abc123xxxxx" || back.Title != "T" {
		t.Errorf("還原結果 = %+v", back)
	}
	if _, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1}); err != nil {
		t.Errorf("還原後應該讀得到字幕：%v", err)
	}
	if items, _ := r.p.Items(ctx); len(items) != 1 {
		t.Errorf("還原後媒體庫應有 1 部影音：%v", items)
	}
	var restored int
	for _, ev := range got() {
		if ev.Type == events.TypeItemRestored && ev.Item == "yt:abc123xxxxx" && ev.ItemID == it.ID {
			restored++
		}
	}
	if restored != 1 {
		t.Errorf("item.restored 事件 %d 筆，預期 1", restored)
	}
}

func TestRestoreKeepsCancelledStagesRetryable(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Restore(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	for _, s := range Stages {
		run := r.stage(it, s)
		if run.Status != store.StatusSkipped || len(run.Actions) != 1 || run.Actions[0] != "重試" {
			t.Errorf("步驟 %s 應維持 skipped 並提供「重試」：%+v", s, run)
		}
	}
	// 「重試」就是重跑：還原後可以從 metadata 重新處理。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	r.run()
	runs, _ := r.st.Read().StageRunsByItem(ctx, it.ID)
	if run := latestRuns(runs)[StageMetadata]; run.Status != store.StatusDone {
		t.Errorf("重試後 metadata 應完成：%+v", run)
	}
}

func TestRestoreNotInTrash(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Restore(ctx, it.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("還原不在垃圾桶中的影音 = %v，預期 ErrNotFound", err)
	}
	if _, err := r.p.Restore(ctx, "no-such-item"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("還原不存在的影音 = %v，預期 ErrNotFound", err)
	}
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Restore(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Restore(ctx, it.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("還原第二次 = %v，預期 ErrNotFound", err)
	}
}

// 垃圾桶列表：刪除時間由新到舊；purge_at = deleted_at + 保留時間（可注入，預設 30 天）。
func TestTrashListOrderAndPurgeAt(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  func(*Deps)
		keep time.Duration
	}{
		{"預設", func(*Deps) {}, 30 * 24 * time.Hour},
		{"注入", func(d *Deps) { d.TrashRetention = 36 * time.Hour }, 36 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.opt)
			ctx := context.Background()
			var ids []string
			for _, id := range []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc"} {
				res, err := r.p.Ingest(ctx, IngestRequest{URL: "https://www.youtube.com/watch?v=" + id})
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, res[0].Item.ID)
			}
			var deletedAt []time.Time
			// 刪除順序 c、a；b 留在媒體庫。
			for _, i := range []int{2, 0} {
				res, err := r.p.Trash(ctx, ids[i], TrashOptions{})
				if err != nil {
					t.Fatal(err)
				}
				deletedAt = append(deletedAt, res.DeletedAt)
				r.clk.Advance(time.Hour)
			}
			list, err := r.p.TrashList(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 || list[0].ID != ids[0] || list[1].ID != ids[2] {
				t.Fatalf("垃圾桶 = %+v，預期 a 在 c 之前（新到舊）", list)
			}
			if list[0].Ref != "yt:aaaaaaaaaaa" || !list[0].DeletedAt.Equal(deletedAt[1]) || !list[0].PurgeAt.Equal(deletedAt[1].Add(tc.keep)) {
				t.Errorf("第一筆 = %+v，預期 deleted_at %v、purge_at +%v", list[0], deletedAt[1], tc.keep)
			}
		})
	}
}

func TestTrashListEmpty(t *testing.T) {
	r := newRig(t)
	list, err := r.p.TrashList(context.Background())
	if err != nil || list == nil || len(list) != 0 {
		t.Errorf("空的垃圾桶 = %v, %v，預期空切片", list, err)
	}
}

// 新增垃圾桶中的影音：不重新建立、不建立新任務，標示 InTrash（DEL-R10）。
func TestIngestOfTrashedItemIsMarkedAndNotRecreated(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	before, _ := r.st.Read().CountTasks(ctx, store.TaskPending)

	res, err := r.p.Ingest(ctx, IngestRequest{URL: watch})
	if err != nil || len(res) != 1 {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if !res[0].InTrash || res[0].AlreadyInLibrary || res[0].Item.ID != it.ID {
		t.Errorf("結果 = %+v，預期 InTrash 且是同一部影音", res[0])
	}
	if after, _ := r.st.Read().CountTasks(ctx, store.TaskPending); after != before {
		t.Errorf("不應建立新任務：%d → %d", before, after)
	}
	if now, _ := r.st.Read().ItemByID(ctx, it.ID); now.DeletedAt == nil {
		t.Error("新增不應順便還原")
	}
}

// 手動新增時，在建立影音的同一個交易中移除 tombstone（DEL-R11）；已存在而沒有新增時不動。
func TestIngestRemovesTombstoneOfNewItem(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.dir, store.DBFileName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	insert := func(ext string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO item_tombstones (platform, external_id, part, sha256, purged_at) VALUES ('youtube', ?, 0, '', 1)`, ext); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_tombstones`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	insert("abc123xxxxx")
	insert("zzzzzzzzzzz")

	res, err := r.p.Ingest(ctx, IngestRequest{URL: watch})
	if err != nil || len(res) != 1 || res[0].AlreadyInLibrary || res[0].InTrash {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if n := count(); n != 1 {
		t.Errorf("tombstone 剩 %d 筆，預期只移除這部影音的，剩 1 筆", n)
	}
}

// 引擎在寫出產出檔之前確認影音還在媒體庫中；不在時丟棄，不寫檔也不寫回狀態（DEL-R2）。
func TestWriteCuesArtifactDiscardsWhenItemGone(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	if _, err := r.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.writeCuesArtifact(ctx, it.ID, subtitleStub(), nil, nil); !errors.Is(err, errItemGone) {
		t.Errorf("垃圾桶中的影音 = %v，預期 errItemGone", err)
	}
	if _, err := r.p.writeCuesArtifact(ctx, "no-such-item", subtitleStub(), nil, nil); !errors.Is(err, errItemGone) {
		t.Errorf("不存在的影音 = %v，預期 errItemGone", err)
	}
	if exists(filepath.Join(r.dir, artifactsDir)) {
		t.Error("不應寫出任何檔案")
	}
	if _, err := r.p.Restore(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if art, err := r.p.writeCuesArtifact(ctx, it.ID, subtitleStub(), nil, nil); err != nil || art == nil {
		t.Errorf("還原後應該寫得出來：%v", err)
	}
}

// Handle 遇到 errItemGone 時不寫回結果，也沒有產出檔；任務與步驟依垃圾桶的規則結束。
func TestHandleDropsResultOfItemTrashedMidRun(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	q := queue.New(r.st, r.clk, "w1")
	claimOne := func() store.Task {
		t.Helper()
		claimed, err := q.Claim(ctx, 5)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim = %+v, %v", claimed, err)
		}
		return claimed[0]
	}
	if err := r.p.Handle(ctx, claimOne()); err != nil { // metadata
		t.Fatal(err)
	}
	transcript := claimOne()
	// 直接在資料庫標記刪除、不取消任務，模擬「檢查與取消之間」的空隙。
	if _, _, err := r.st.Write().TrashItem(ctx, it.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.p.Handle(ctx, transcript); err != nil {
		t.Fatalf("Handle = %v，影音不在媒體庫中不是錯誤", err)
	}
	if arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, ""); len(arts) != 0 {
		t.Errorf("不應新增產出：%+v", arts)
	}
	if exists(filepath.Join(r.dir, artifactsDir)) {
		t.Error("不應寫出任何產出檔")
	}
	// 不寫回結果，但任務與步驟要結束：skipped（cancelled），不能留下 running／leased。
	if run := r.stage(it, StageTranscript); run.Status != store.StatusSkipped || run.BlockReason != "cancelled" {
		t.Errorf("步驟應為 skipped（cancelled）：%+v", run)
	}
	if tk, err := r.st.Read().Task(ctx, transcript.ID); err != nil || tk.State != store.TaskCancelled {
		t.Errorf("任務 = %+v, %v，預期 cancelled", tk, err)
	}
}
