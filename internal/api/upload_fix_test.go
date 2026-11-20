package api

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// 同一個 sha 有一筆在垃圾桶、一筆正常：上傳相同內容回 200，指向正常的那筆。
func TestUploadSameContentPrefersLiveItem(t *testing.T) {
	r := newLocalRig(t, nil)
	ctx := context.Background()
	first := decode[itemsBody](t, r.upload("talk.m4a", "AUDIO-one", nil)).Items[0]
	if _, err := r.p.Trash(ctx, first.ID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	var live store.Item
	err := r.st.Tx(ctx, func(rp *store.Repo) error {
		var err error
		live, _, err = rp.InsertItemIfAbsent(ctx, store.NewItem{
			Platform: "local", ExternalID: "other.m4a", SourcePath: "x", ContentSHA256: sha("AUDIO-one"), Origin: store.OriginUpload,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := r.upload("again.m4a", "AUDIO-one", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if it := decode[itemsBody](t, rec).Items[0]; it.ID != live.ID || !it.AlreadyInLibrary {
		t.Errorf("item = %+v，要指向 %s", it, live.ID)
	}
}

// inbox 頂層已有同名檔案：上傳的 ref 避開它。
func TestUploadAvoidsInboxName(t *testing.T) {
	r := newLocalRig(t, nil)
	if err := os.WriteFile(filepath.Join(r.inbox, "talk.m4a"), []byte("AUDIO-inbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := r.upload("talk.m4a", "AUDIO-one", nil)
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if ref := decode[itemsBody](t, rec).Items[0].Ref; ref != "local:talk-2.m4a" {
		t.Errorf("ref = %q", ref)
	}
}

// deadlineWriter 記錄 handler 對讀取期限的設定（ResponseRecorder 不支援 SetReadDeadline）。
type deadlineWriter struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	deadlines []time.Time
}

func (w *deadlineWriter) SetReadDeadline(t time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadlines = append(w.deadlines, t)
	return nil
}

// 閒置逾時：每次讀取本體前把期限重設為「現在 + 2 分鐘」，讀完後清除（不能留著讓連線的背景讀取逾時）。
func TestUploadIdleReadDeadline(t *testing.T) {
	r := newLocalRig(t, nil)
	body, ct := multipartBody(t, nil, multipartFile{"file", "talk.m4a", "AUDIO-" + strings.Repeat("x", 100<<10)})
	req := httptest.NewRequest("POST", "/api/items", body)
	req.Header.Set("Content-Type", ct)
	r.owner(nil)(req)
	w := &deadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	r.h.ServeHTTP(w, req)
	after := time.Now()
	if w.Code != 201 {
		t.Fatalf("status = %d：%s", w.Code, w.Body)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.deadlines) < 3 {
		t.Fatalf("期限只設了 %d 次，每次讀取前都要重設", len(w.deadlines))
	}
	for i, d := range w.deadlines[:len(w.deadlines)-1] {
		if d.Before(before.Add(uploadIdleTimeout)) || d.After(after.Add(uploadIdleTimeout)) {
			t.Fatalf("第 %d 次的期限 %v 不是現在 + %v", i, d, uploadIdleTimeout)
		}
	}
	if last := w.deadlines[len(w.deadlines)-1]; !last.IsZero() {
		t.Errorf("讀完之後要清除期限：%v", last)
	}
}

// blockingBody 的第一次讀取會通知 entered，然後阻塞到 release 關閉（之後回 EOF）。
type blockingBody struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return 0, io.EOF
}

// 並行上限：同時進行的上傳最多 2 個，第 3 個回 429 且不讀取本體；有人結束後又可以上傳。
func TestUploadConcurrencyLimit(t *testing.T) {
	r := newLocalRig(t, nil)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range maxConcurrentUploads {
		b := &blockingBody{entered: make(chan struct{}), release: release}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.uploadBody(b, "multipart/form-data; boundary=x", nil)
		}()
		<-b.entered
	}
	rc := &readCounter{}
	rec := r.uploadBody(rc, "multipart/form-data; boundary=x", nil)
	if rec.Code != 429 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if e := decode[Error](t, rec); e.Code != CodeTooManyUploads || e.Message != "目前有其他上傳正在進行，請稍後再試。" {
		t.Errorf("error = %+v", e)
	}
	if rc.n != 0 {
		t.Errorf("本體被讀取了 %d 次", rc.n)
	}
	close(release)
	wg.Wait()
	if rec := r.upload("talk.m4a", "AUDIO-one", nil); rec.Code != 201 {
		t.Errorf("釋放之後：%d %s", rec.Code, rec.Body)
	}
}

// part 數上限：16 個，超過回 422。
func TestUploadPartLimit(t *testing.T) {
	r := newLocalRig(t, nil)
	var fields [][2]string
	for range maxUploadParts - 1 {
		fields = append(fields, [2]string{"overrides", "{}"})
	}
	body, ct := multipartBody(t, fields, multipartFile{"file", "talk.m4a", "AUDIO-one"}) // 剛好 16 個
	if rec := r.uploadBody(body, ct, nil); rec.Code != 201 {
		t.Fatalf("16 個 part：%d %s", rec.Code, rec.Body)
	}
	fields = append(fields, [2]string{"overrides", "{}"})
	body, ct = multipartBody(t, fields, multipartFile{"file", "other.m4a", "AUDIO-two"}) // 17 個
	if rec := r.uploadBody(body, ct, nil); rec.Code != 422 {
		t.Fatalf("17 個 part：%d %s", rec.Code, rec.Body)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}

// countingReader 記錄被讀走的位元組數。
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// until 在 file 之前：不合法時讀到 until 就 422，不讀 file 的內容。
func TestUploadInvalidUntilRejectedEarly(t *testing.T) {
	r := newLocalRig(t, nil)
	body, ct := multipartBody(t, [][2]string{{"until", "bogus"}}, multipartFile{"file", "big.m4a", "AUDIO" + strings.Repeat("x", 4<<20)})
	total := body.Len()
	cr := &countingReader{r: body}
	rec := r.uploadBody(cr, ct, nil)
	if rec.Code != 422 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if cr.n > total/4 {
		t.Errorf("讀了 %d / %d 位元組，應該在讀到 until 時就拒絕", cr.n, total)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}

// until 在 file 之後：寫入完成後、ffprobe 之前驗證；不合法時不會搬到 uploads。
func TestUploadInvalidUntilAfterFileRejectedBeforeProbe(t *testing.T) {
	r := newLocalRig(t, nil)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "talk.m4a")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(fw, "AUDIO-one")
	if err := mw.WriteField("until", "bogus"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	rec := r.uploadBody(&buf, mw.FormDataContentType(), nil)
	if rec.Code != 422 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "uploads")); !os.IsNotExist(err) {
		t.Errorf("uploads 不該存在：%v", err)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}
