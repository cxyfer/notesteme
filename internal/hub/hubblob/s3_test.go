package hubblob

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fakeAKID   = "AKIDFAKE"
	fakeSecret = "fake-Secret-Value-123"
)

// fakeS3 是只支援 path-style 的 PutObject、GetObject、HeadObject、DeleteObject、HeadBucket 的假 S3，
// 可以注入故障。它只檢查 Authorization 帶的 access key（不驗簽名）與 Content-MD5。
type fakeS3 struct {
	bucket string
	mu     sync.Mutex
	objs   map[string][]byte
	// fault：""、"500"、"truncate"（宣告完整長度但只送一半）、"corrupt"（同長度、內容不同）、"slow"。
	fault    atomic.Value
	puts     atomic.Int64
	lastKeys []string
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	f := &fakeS3{bucket: "test-bucket", objs: map[string][]byte{}}
	f.fault.Store("")
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func s3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>`+code+`</Code><Message>`+code+`</Message></Error>`)
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Authorization"), "Credential="+fakeAKID+"/") {
		s3Error(w, http.StatusForbidden, "InvalidAccessKeyId")
		return
	}
	fault := f.fault.Load().(string)
	switch fault {
	case "500":
		s3Error(w, http.StatusInternalServerError, "InternalError")
		return
	case "slow":
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
			return
		}
	}
	bucket, key, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if bucket != f.bucket {
		s3Error(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	if key == "" {
		if r.Method == http.MethodHead {
			return
		}
		s3Error(w, http.StatusNotImplemented, "NotImplemented")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastKeys = append(f.lastKeys, key)
	switch r.Method {
	case http.MethodPut:
		b, err := io.ReadAll(r.Body)
		if err != nil {
			s3Error(w, http.StatusBadRequest, "IncompleteBody")
			return
		}
		if m := r.Header.Get("Content-MD5"); m != "" {
			sum := md5.Sum(b) //nolint:gosec
			if m != base64.StdEncoding.EncodeToString(sum[:]) {
				s3Error(w, http.StatusBadRequest, "BadDigest")
				return
			}
		}
		f.objs[key] = b
		f.puts.Add(1)
	case http.MethodGet, http.MethodHead:
		b, ok := f.objs[key]
		if !ok {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			s3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		if r.Method == http.MethodHead {
			return
		}
		switch fault {
		case "truncate":
			_, _ = w.Write(b[:len(b)/2])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler) // 中斷連線：client 收到不完整的 body
		case "corrupt":
			c := bytes.Clone(b)
			if len(c) > 0 {
				c[0] ^= 0xff
			}
			_, _ = w.Write(c)
			return
		}
		_, _ = w.Write(b)
	case http.MethodDelete:
		delete(f.objs, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		s3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func newFakeStore(t *testing.T, mod func(*S3Options)) (*S3, *fakeS3) {
	t.Helper()
	f, srv := newFakeS3(t)
	o := S3Options{Endpoint: srv.URL, Bucket: f.bucket, PathStyle: true, AccessKeyID: fakeAKID, SecretAccessKey: fakeSecret,
		Timeout: 5 * time.Second, HTTPClient: srv.Client()}
	if mod != nil {
		mod(&o)
	}
	s, err := NewS3(o)
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestS3FakeContract(t *testing.T) {
	runContract(t, func(t *testing.T) Store { s, _ := newFakeStore(t, nil); return s })
}

func TestNewS3Validation(t *testing.T) {
	for _, o := range []S3Options{{AccessKeyID: "a", SecretAccessKey: "b"}, {Bucket: "b"}, {Bucket: "b", AccessKeyID: "a"}} {
		if _, err := NewS3(o); err == nil {
			t.Errorf("NewS3(%+v) 應該失敗", o)
		}
	}
	s, err := NewS3(S3Options{Bucket: "b", AccessKeyID: "a", SecretAccessKey: "c", Prefix: "/p/q/"})
	if err != nil || s.timeout != 60*time.Second || s.max != DefaultS3MaxObjectBytes || s.key(strings.Repeat("a", 64)) != "p/q/"+strings.Repeat("a", 64) {
		t.Fatalf("預設值：%+v %v", s, err)
	}
}

// 物件鍵是 <prefix>/<sha256>，上傳時附 Content-MD5（假服務會檢查）。
func TestS3PrefixAndDedup(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeStore(t, func(o *S3Options) { o.Prefix = "hub/blobs" })
	sha, _, err := s.Put(ctx, strings.NewReader("x"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objs["hub/blobs/"+sha]; !ok {
		t.Fatalf("物件鍵 = %v", f.lastKeys)
	}
	if _, _, err := s.Put(ctx, strings.NewReader("x"), sha); err != nil || f.puts.Load() != 1 {
		t.Fatalf("重複上傳：%v，PUT 次數 %d，預期已存在時不重寫", err, f.puts.Load())
	}
}

// 錯誤的憑證：失敗，錯誤訊息不含 secret。
func TestS3WrongCredentialsDoNotLeak(t *testing.T) {
	ctx := context.Background()
	s, _ := newFakeStore(t, func(o *S3Options) { o.AccessKeyID = "WRONG" })
	_, _, err := s.Put(ctx, strings.NewReader("x"), "")
	if err == nil || strings.Contains(err.Error(), fakeSecret) {
		t.Fatalf("錯誤的憑證：err = %v", err)
	}
	for _, e := range []error{s.Ping(ctx), s.Delete(ctx, sum("x"))} {
		if e == nil || strings.Contains(e.Error(), fakeSecret) {
			t.Errorf("錯誤的憑證：err = %v", e)
		}
	}
	if _, err := s.Exists(ctx, sum("x")); err == nil {
		t.Error("錯誤的憑證時 Exists 要回錯誤，不是 false")
	}
	if _, err := s.Open(ctx, sum("x")); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("錯誤的憑證時 Open = %v，預期不是 ErrNotFound 的錯誤", err)
	}
	// wrap 會把出現在錯誤中的 secret 遮掉。
	if w := s.wrap("x", errors.New("boom "+fakeSecret)); strings.Contains(w.Error(), fakeSecret) {
		t.Errorf("wrap = %v", w)
	}
}

func TestS3Ping(t *testing.T) {
	ctx := context.Background()
	s, _ := newFakeStore(t, nil)
	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	s2, _ := newFakeStore(t, func(o *S3Options) { o.Bucket = "missing" })
	if err := s2.Ping(ctx); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("bucket 不存在：%v", err)
	}
}

// 截斷或損壞的下載回 ErrCorrupt，不會把不完整的內容當成完整的。
func TestS3CorruptDownloads(t *testing.T) {
	ctx := context.Background()
	for _, fault := range []string{"truncate", "corrupt"} {
		s, f := newFakeStore(t, nil)
		content := strings.Repeat("z", 4096)
		sha, _, err := s.Put(ctx, strings.NewReader(content), "")
		if err != nil {
			t.Fatal(err)
		}
		f.fault.Store(fault)
		r, err := s.Open(ctx, sha)
		if err != nil {
			t.Fatalf("%s：Open = %v", fault, err)
		}
		_, err = io.ReadAll(r)
		_ = r.Close()
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s：讀取 = %v，預期 ErrCorrupt", fault, err)
		}
	}
}

func TestS3ServerErrorsAndTimeouts(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeStore(t, func(o *S3Options) { o.Timeout = 300 * time.Millisecond })
	f.fault.Store("500")
	if _, _, err := s.Put(ctx, strings.NewReader("x"), ""); err == nil || strings.Contains(err.Error(), fakeSecret) {
		t.Errorf("500：%v", err)
	}
	f.fault.Store("slow")
	start := time.Now()
	_, err := s.Exists(ctx, sum("x"))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 1500*time.Millisecond {
		t.Errorf("逾時：%v（%v），預期在逾時後回 DeadlineExceeded", err, time.Since(start))
	}
	if _, err := s.Open(ctx, sum("x")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Open 逾時：%v", err)
	}
	// 呼叫端取消也要停止。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Delete(cctx, sum("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("取消：%v", err)
	}
}

// Open 回傳之後才讀 body：逾時的 context 要到 Close 才釋放，不能在 Open 回傳時就取消。
func TestS3OpenStreamsAfterReturn(t *testing.T) {
	ctx := context.Background()
	s, _ := newFakeStore(t, nil)
	content := strings.Repeat("stream", 200000) // 1.2 MB，大於一次 read
	sha, _, err := s.Put(ctx, strings.NewReader(content), "")
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Open(ctx, sha)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	b, err := io.ReadAll(r)
	if err != nil || string(b) != content {
		t.Fatalf("讀取 = %d bytes, %v", len(b), err)
	}
	if err := r.Close(); err != nil {
		t.Error(err)
	}
}

// 超過上限的內容不上傳（不論 reader 能不能 seek）。
func TestS3TooLarge(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeStore(t, func(o *S3Options) { o.MaxObjectBytes = 10 })
	for _, r := range []io.Reader{strings.NewReader("01234567890"), io.MultiReader(strings.NewReader("01234567890"))} {
		if _, _, err := s.Put(ctx, r, ""); !errors.Is(err, ErrTooLarge) {
			t.Errorf("超過上限：%v", err)
		}
	}
	if _, _, err := s.Put(ctx, strings.NewReader("0123456789"), ""); err != nil {
		t.Errorf("剛好上限：%v", err)
	}
	if f.puts.Load() != 1 {
		t.Errorf("PUT 次數 = %d，預期 1", f.puts.Load())
	}
}

// reader 不在開頭時，只上傳目前位置之後的內容；SDK 重試時 seek 回同一個位置。
func TestS3SeekerOffset(t *testing.T) {
	ctx := context.Background()
	s, _ := newFakeStore(t, nil)
	r := strings.NewReader("skip-payload")
	_, _ = r.Seek(5, io.SeekStart)
	sha, n, err := s.Put(ctx, r, "")
	if err != nil || sha != sum("payload") || n != 7 {
		t.Fatalf("Put = %q %d %v", sha, n, err)
	}
	b := &boundedSeeker{rs: strings.NewReader("xxabcdef"), start: 2, n: 4}
	_, _ = b.Seek(0, io.SeekStart)
	got, _ := io.ReadAll(b)
	if string(got) != "abcd" {
		t.Errorf("boundedSeeker = %q", got)
	}
	for _, c := range []struct {
		off    int64
		whence int
		want   int64
	}{{1, io.SeekStart, 1}, {1, io.SeekCurrent, 2}, {-1, io.SeekEnd, 3}} {
		if p, err := b.Seek(c.off, c.whence); err != nil || p != c.want {
			t.Errorf("Seek(%d, %d) = %d %v", c.off, c.whence, p, err)
		}
	}
	rest, _ := io.ReadAll(b)
	if string(rest) != "d" {
		t.Errorf("Seek 之後讀到 %q", rest)
	}
	if _, err := b.Seek(-10, io.SeekStart); err == nil {
		t.Error("負的位置要失敗")
	}
	if _, err := b.Seek(0, 9); err == nil {
		t.Error("不認得的 whence 要失敗")
	}
}

// 真的 S3 相容服務（MinIO）：設定 NOTESTEME_TEST_S3_ENDPOINT 等環境變數時執行共用案例；bucket 要已經存在。
func TestS3RealContract(t *testing.T) {
	endpoint := os.Getenv("NOTESTEME_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("沒有設定 NOTESTEME_TEST_S3_ENDPOINT，略過真的 S3 相容服務")
	}
	o := S3Options{
		Endpoint: endpoint, Region: os.Getenv("NOTESTEME_TEST_S3_REGION"), Bucket: os.Getenv("NOTESTEME_TEST_S3_BUCKET"), PathStyle: true,
		AccessKeyID: os.Getenv("NOTESTEME_TEST_S3_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("NOTESTEME_TEST_S3_SECRET_ACCESS_KEY"),
		Timeout: 30 * time.Second,
	}
	open := func(t *testing.T) Store {
		// 每個案例用自己的前綴，互不影響。
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		oo := o
		oo.Prefix = "test-" + hex.EncodeToString(b)
		s, err := NewS3(oo)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if err := open(t).(*S3).Ping(context.Background()); err != nil {
		t.Fatalf("Ping：%v", err)
	}
	runContract(t, open)
	// 錯誤的 secret：簽名不符，錯誤訊息不含 secret。
	bad := o
	bad.SecretAccessKey = "definitely-Wrong-Secret-42"
	s, _ := NewS3(bad)
	if _, _, err := s.Put(context.Background(), strings.NewReader("x"), ""); err == nil || strings.Contains(err.Error(), bad.SecretAccessKey) {
		t.Errorf("錯誤的 secret：%v", err)
	}
}
