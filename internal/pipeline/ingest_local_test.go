package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/inbox/fakemedia"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// ingestRig 是本機影音新增的測試環境：root 是 inbox 與 media_roots 的替身，uploads 在資料目錄底下。
type ingestRig struct {
	*rig
	root string
	wake int
	mu   sync.Mutex
	evs  []events.Event
}

func newIngestRig(t *testing.T) *ingestRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lr := &ingestRig{root: root}
	tool, _ := fakemedia.New(t)
	lr.rig = newRig(t, func(d *Deps) {
		d.Media = tool
		d.Files = pathguard.New([]string{root}).With(filepath.Join(d.DataDir, "uploads"))
		orig := d.Wake
		d.Wake = func() { lr.mu.Lock(); lr.wake++; lr.mu.Unlock(); orig() }
	})
	lr.st.SetEventSink(func(ev events.Event) { lr.mu.Lock(); lr.evs = append(lr.evs, ev); lr.mu.Unlock() })
	return lr
}

func (lr *ingestRig) created() int {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	n := 0
	for _, e := range lr.evs {
		if e.Type == events.TypeItemCreated {
			n++
		}
	}
	return n
}

func (lr *ingestRig) ingestLocal(t LocalTarget) LocalOutcome {
	lr.t.Helper()
	out, err := lr.p.IngestLocal(context.Background(), t)
	if err != nil {
		lr.t.Fatal(err)
	}
	return out
}

// tmpUpload 在資料目錄的 tmp 下寫一個上傳暫存檔，回傳路徑。
func (lr *ingestRig) tmpUpload(content string) string {
	lr.t.Helper()
	dir := filepath.Join(lr.dir, "tmp", "upload-x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		lr.t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, "u-*.m4a")
	if err != nil {
		lr.t.Fatal(err)
	}
	_, _ = f.WriteString(content)
	_ = f.Close()
	return f.Name()
}

func (lr *ingestRig) upload(name, content, sha string) (LocalOutcome, string, error) {
	lr.t.Helper()
	tmp := lr.tmpUpload(content)
	out, err := lr.p.IngestUpload(context.Background(), UploadRequest{Name: name, TmpPath: tmp, SHA256: sha})
	return out, tmp, err
}

func (lr *ingestRig) purge(it store.Item) {
	lr.t.Helper()
	ctx := context.Background()
	if _, err := lr.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		lr.t.Fatal(err)
	}
	if err := lr.p.Purge(ctx, it.ID); err != nil {
		lr.t.Fatal(err)
	}
}

func (lr *ingestRig) tombstoned(key, sha string) bool {
	lr.t.Helper()
	ok, err := lr.st.Read().LocalTombstoneBlocks(context.Background(), key, sha)
	if err != nil {
		lr.t.Fatal(err)
	}
	return ok
}

func TestIngestLocalCreated(t *testing.T) {
	lr := newIngestRig(t)
	out := lr.ingestLocal(LocalTarget{Key: "talk.mp4", SourcePath: filepath.Join(lr.root, "talk.mp4"), SHA256: "aa", Origin: store.OriginInbox})
	if out.State != store.InboxIngested || !out.Created {
		t.Fatalf("out = %+v", out)
	}
	it := out.Item
	if it.Platform != "local" || it.ExternalID != "talk.mp4" || it.URL != "" || it.Origin != store.OriginInbox || it.ContentSHA256 != "aa" {
		t.Errorf("item = %+v", it)
	}
	// metadata 任務：手動新增的優先順序（ING-R12），其他步驟都是 pending。
	run := lr.stage(it, StageMetadata)
	task, err := lr.st.Read().TaskByStageRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Priority != queue.PriorityManual || task.Kind != StageMetadata {
		t.Errorf("task = %+v", task)
	}
	for _, s := range Stages {
		if lr.stage(it, s).Status != store.StatusPending {
			t.Errorf("%s 應該是 pending", s)
		}
	}
	if lr.wake == 0 {
		t.Error("建立之後應該喚醒 worker")
	}
	if lr.created() != 1 {
		t.Errorf("item.created 事件 = %d，要 1", lr.created())
	}
}

func TestIngestLocalAlready(t *testing.T) {
	lr := newIngestRig(t)
	first := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	again := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	if again.State != store.InboxAlready || again.Created || again.Item.ID != first.Item.ID {
		t.Errorf("again = %+v", again)
	}
	if lr.created() != 1 {
		t.Errorf("不該重複發布事件：%d", lr.created())
	}
	// 對方還沒有雜湊（用路徑新增、metadata 尚未補上）也是 already。
	lr.ingestLocal(LocalTarget{Key: "/abs/b.mp4", Origin: store.OriginManual})
	if out := lr.ingestLocal(LocalTarget{Key: "/abs/b.mp4", SHA256: "bb", Origin: store.OriginInbox}); out.State != store.InboxAlready {
		t.Errorf("對方沒有雜湊：%+v", out)
	}
}

func TestIngestLocalConflict(t *testing.T) {
	lr := newIngestRig(t)
	lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	out := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "bb", Origin: store.OriginInbox})
	if out.State != store.InboxConflict || out.Created || out.Item.ContentSHA256 != "aa" {
		t.Errorf("out = %+v", out)
	}
}

func TestIngestLocalInTrash(t *testing.T) {
	lr := newIngestRig(t)
	first := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	if _, err := lr.p.Trash(context.Background(), first.Item.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	out := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	if out.State != store.InboxInTrash || out.Item.ID != first.Item.ID || out.Created {
		t.Errorf("out = %+v", out)
	}
}

func TestIngestLocalDuplicate(t *testing.T) {
	lr := newIngestRig(t)
	first := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	out := lr.ingestLocal(LocalTarget{Key: "b.mp4", SHA256: "aa", Origin: store.OriginInbox})
	if out.State != store.InboxDuplicate || out.Created || out.Item.ID != first.Item.ID {
		t.Errorf("out = %+v", out)
	}
	if items, _ := lr.p.Items(context.Background()); len(items) != 1 {
		t.Errorf("影音數 = %d", len(items))
	}
}

// inbox 遇到 tombstone：不新增，也不移除 tombstone；手動新增則移除並建立（DEL-R11）。
func TestIngestLocalTombstone(t *testing.T) {
	lr := newIngestRig(t)
	first := lr.ingestLocal(LocalTarget{Key: "a.mp4", SHA256: "aa", Origin: store.OriginInbox})
	lr.purge(first.Item)

	// 內容雜湊相同（即使檔名不同）就被擋，而且 tombstone 還在。
	out := lr.ingestLocal(LocalTarget{Key: "renamed.mp4", SHA256: "aa", Origin: store.OriginInbox})
	if out.State != store.InboxTombstoned || out.Created {
		t.Fatalf("out = %+v", out)
	}
	if !lr.tombstoned("x", "aa") {
		t.Fatal("inbox 不該移除 tombstone")
	}

	// 手動新增：依鍵移除 tombstone 並建立。
	res, err := lr.p.IngestPath(context.Background(), filepath.Join(lr.root, "a.mp4"), LocalOptions{Inbox: lr.root})
	if err != nil || res.AlreadyInLibrary || res.InTrash || res.Item.ExternalID != "a.mp4" {
		t.Fatalf("IngestPath = %+v %v", res, err)
	}
	if lr.tombstoned("x", "aa") {
		t.Error("手動新增應該移除同鍵的 tombstone")
	}
}

func TestIngestLocalValidation(t *testing.T) {
	lr := newIngestRig(t)
	if _, err := lr.p.IngestLocal(context.Background(), LocalTarget{}); err == nil {
		t.Error("沒有 Key 應該失敗")
	}
	var inv *InvalidError
	if _, err := lr.p.IngestLocal(context.Background(), LocalTarget{Key: "a", Until: "nope"}); !errors.As(err, &inv) {
		t.Errorf("until 不合法應是 InvalidError：%v", err)
	}
}

func TestIngestPath(t *testing.T) {
	lr := newIngestRig(t)
	ctx := context.Background()
	inbox := filepath.Join(lr.root, "inbox")
	path := filepath.Join(inbox, "talks", "keynote.mp4")

	// 檔案不存在也建立（延遲驗證）；inbox 內的檔案鍵是相對路徑。
	res, err := lr.p.IngestPath(ctx, path, LocalOptions{Inbox: inbox, Until: StageMetadata})
	if err != nil {
		t.Fatal(err)
	}
	it := res.Item
	if res.AlreadyInLibrary || res.InTrash || it.ExternalID != "talks/keynote.mp4" || it.SourcePath != path ||
		it.Origin != store.OriginManual || it.ContentSHA256 != "" || it.UntilStage != StageMetadata {
		t.Fatalf("res = %+v", res)
	}
	// 同一個檔案再加一次：已存在。
	if again, err := lr.p.IngestPath(ctx, path, LocalOptions{Inbox: inbox}); err != nil || !again.AlreadyInLibrary || again.Item.ID != it.ID {
		t.Errorf("again = %+v %v", again, err)
	}
	// inbox 之外：鍵是絕對路徑。
	other := filepath.Join(lr.root, "media", "x.mp4")
	if r2, err := lr.p.IngestPath(ctx, other, LocalOptions{Inbox: inbox}); err != nil || r2.Item.ExternalID != other {
		t.Errorf("r2 = %+v %v", r2, err)
	}
	// 垃圾桶中：InTrash。
	if _, err := lr.p.Trash(ctx, it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if r3, err := lr.p.IngestPath(ctx, path, LocalOptions{Inbox: inbox}); err != nil || !r3.InTrash {
		t.Errorf("r3 = %+v %v", r3, err)
	}
	// 相對路徑與處理方案被拒絕。
	if _, err := lr.p.IngestPath(ctx, "talk.mp4", LocalOptions{}); err == nil {
		t.Error("相對路徑應該失敗")
	}
	var ni *NotImplementedError
	if _, err := lr.p.IngestPath(ctx, path+"2", LocalOptions{Profile: "fast"}); !errors.As(err, &ni) {
		t.Errorf("profile 應是 NotImplementedError：%v", err)
	}
}

func TestLocalKey(t *testing.T) {
	for _, c := range []struct{ inbox, path, want string }{
		{"/in", "/in/a.mp4", "a.mp4"},
		{"/in", "/in/x/y.mp4", "x/y.mp4"},
		{"/in", "/media/a.mp4", "/media/a.mp4"},
		{"/in", "/in2/a.mp4", "/in2/a.mp4"},
		{"/in", "/in", "/in"},
		{"", "/a/b.mp4", "/a/b.mp4"},
	} {
		if got := LocalKey(c.inbox, c.path); got != c.want {
			t.Errorf("LocalKey(%q, %q) = %q，要 %q", c.inbox, c.path, got, c.want)
		}
	}
}

func TestValidUploadName(t *testing.T) {
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	for name, want := range map[string]bool{
		"talk.m4a": true, "我的 錄音.mp3": true, "": false, ".": false, "..": false, "a/b.mp4": false,
		`a\b.mp4`: false, "a\x00b.mp4": false, "a\nb.mp4": false, string(long): false, string(long[:255]): true,
	} {
		if got := ValidUploadName(name); got != want {
			t.Errorf("ValidUploadName(%q) = %v", name, got)
		}
	}
}

func TestIngestUploadCreatedAndCollision(t *testing.T) {
	lr := newIngestRig(t)
	out, tmp, err := lr.upload("talk.m4a", "AUDIO-one", "s1")
	if err != nil {
		t.Fatal(err)
	}
	it := out.Item
	if out.State != store.InboxIngested || !out.Created || it.ExternalID != "talk.m4a" || it.Origin != store.OriginUpload ||
		it.SourcePath != "uploads/"+it.ID+"/talk.m4a" || it.ContentSHA256 != "s1" {
		t.Fatalf("out = %+v", out)
	}
	if b, err := os.ReadFile(filepath.Join(lr.dir, it.SourcePath)); err != nil || string(b) != "AUDIO-one" {
		t.Errorf("上傳檔 = %q %v", b, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("成功後暫存檔應該被搬走：%v", err)
	}

	// 撞名、內容不同：加 -2、-3。
	out2, _, err := lr.upload("talk.m4a", "AUDIO-two", "s2")
	if err != nil || out2.Item.ExternalID != "talk-2.m4a" || !out2.Created {
		t.Fatalf("out2 = %+v %v", out2, err)
	}
	out3, _, err := lr.upload("talk.m4a", "AUDIO-three", "s3")
	if err != nil || out3.Item.ExternalID != "talk-3.m4a" {
		t.Fatalf("out3 = %+v %v", out3, err)
	}
	// 磁碟上維持原檔名（標題用）。
	if out3.Item.SourcePath != "uploads/"+out3.Item.ID+"/talk.m4a" {
		t.Errorf("SourcePath = %q", out3.Item.SourcePath)
	}
}

func TestIngestUploadDuplicate(t *testing.T) {
	lr := newIngestRig(t)
	first, _, err := lr.upload("talk.m4a", "AUDIO-one", "s1")
	if err != nil {
		t.Fatal(err)
	}
	out, tmp, err := lr.upload("copy.m4a", "AUDIO-one", "s1")
	if err != nil || out.State != store.InboxDuplicate || out.Created || out.Item.ID != first.Item.ID {
		t.Fatalf("out = %+v %v", out, err)
	}
	// 沒有建立影音：暫存檔還在（由呼叫端清理），uploads 底下沒有多出目錄。
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("暫存檔應該還在：%v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(lr.dir, "uploads"))
	if len(entries) != 1 {
		t.Errorf("uploads 底下有 %d 個目錄，要 1", len(entries))
	}
	// 相同內容在垃圾桶中：仍是 duplicate，Item.DeletedAt 讓呼叫端回 409。
	if _, err := lr.p.Trash(context.Background(), first.Item.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	out, _, err = lr.upload("again.m4a", "AUDIO-one", "s1")
	if err != nil || out.State != store.InboxDuplicate || out.Item.DeletedAt == nil {
		t.Fatalf("垃圾桶中：%+v %v", out, err)
	}
}

func TestIngestUploadNotMedia(t *testing.T) {
	lr := newIngestRig(t)
	out, tmp, err := lr.upload("notes.m4a", "plain text", "s9")
	if !errors.Is(err, media.ErrNotMedia) || out.Created {
		t.Fatalf("out = %+v err = %v", out, err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("暫存檔留給呼叫端清理：%v", err)
	}
	if _, err := os.Stat(filepath.Join(lr.dir, "uploads")); !os.IsNotExist(err) {
		t.Errorf("不是影音時不該建立 uploads：%v", err)
	}
	if lr.created() != 0 {
		t.Error("不該建立影音")
	}
}

// 上傳之後，相同雜湊與相同鍵的 tombstone 都被移除（DEL-R11）。
func TestIngestUploadRemovesTombstones(t *testing.T) {
	lr := newIngestRig(t)
	first, _, err := lr.upload("talk.m4a", "AUDIO-one", "s1")
	if err != nil {
		t.Fatal(err)
	}
	lr.purge(first.Item)
	if !lr.tombstoned("other.m4a", "s1") {
		t.Fatal("永久刪除後應該有 tombstone")
	}
	out, _, err := lr.upload("renamed.m4a", "AUDIO-one", "s1")
	if err != nil || !out.Created {
		t.Fatalf("out = %+v %v", out, err)
	}
	if lr.tombstoned("other.m4a", "s1") {
		t.Error("上傳相同內容後應該移除 tombstone")
	}
}

func TestIngestUploadInvalidName(t *testing.T) {
	lr := newIngestRig(t)
	var inv *InvalidError
	if _, _, err := lr.upload("a/b.m4a", "AUDIO", "s"); !errors.As(err, &inv) {
		t.Errorf("err = %v", err)
	}
}

// 沒有 media 工具（nil）：502，不是 panic。
func TestIngestUploadWithoutMedia(t *testing.T) {
	r := newRig(t)
	tmp := filepath.Join(t.TempDir(), "u.m4a")
	if err := os.WriteFile(tmp, []byte("AUDIO"), 0o600); err != nil {
		t.Fatal(err)
	}
	var up *UpstreamError
	if _, err := r.p.IngestUpload(context.Background(), UploadRequest{Name: "a.m4a", TmpPath: tmp, SHA256: "s"}); !errors.As(err, &up) {
		t.Errorf("err = %v", err)
	}
}

// 上傳與 inbox 共用命名空間：inbox 頂層已有同名項目（還沒新增）時，上傳要避開它。
func TestIngestUploadAvoidsInboxNames(t *testing.T) {
	lr := newIngestRig(t)
	if err := os.WriteFile(filepath.Join(lr.root, "talk.m4a"), []byte("AUDIO-inbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 目錄與壞掉的 symlink 也佔用名稱（Lstat，不論是否為一般檔案）。
	if err := os.Mkdir(filepath.Join(lr.root, "talk-2.m4a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(lr.root, "missing"), filepath.Join(lr.root, "talk-3.m4a")); err != nil {
		t.Fatal(err)
	}
	tmp := lr.tmpUpload("AUDIO-one")
	out, err := lr.p.IngestUpload(context.Background(), UploadRequest{Name: "talk.m4a", TmpPath: tmp, SHA256: "s1", Inbox: lr.root})
	if err != nil || !out.Created || out.Item.ExternalID != "talk-4.m4a" {
		t.Fatalf("out = %+v %v", out, err)
	}
	// 沒有指定 inbox 時只看資料庫。
	out, _, err = lr.upload("talk.m4a", "AUDIO-two", "s2")
	if err != nil || out.Item.ExternalID != "talk.m4a" {
		t.Fatalf("無 inbox：%+v %v", out, err)
	}
}
