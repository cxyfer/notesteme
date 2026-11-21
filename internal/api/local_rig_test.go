package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/inbox/fakemedia"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// localRig 是本機影音（上傳、path 新增、媒體端點）的 API 測試環境。
type localRig struct {
	t     *testing.T
	h     http.Handler
	svc   *auth.Service
	st    *store.Store
	p     *pipeline.Pipeline
	dir   string // 資料目錄
	root  string // media_roots 與 inbox 的替身
	inbox string
	sess  string
	csrf  string
}

func newLocalRig(t *testing.T, mod func(*Deps)) *localRig {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "data")
	root := filepath.Join(base, "media")
	inbox := filepath.Join(root, "inbox")
	for _, d := range []string{dir, inbox} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	clk := clock.NewFake(time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := auth.New(auth.Config{Store: st, Clock: clk, Params: auth.TestParams})
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	q := queue.New(st, clk, "w")
	tool, _ := fakemedia.New(t)
	guard := pathguard.New([]string{root})
	p := pipeline.New(pipeline.Deps{
		Store: st, Clock: clk, DataDir: dir, Log: log, Wake: q.Wake, Version: "test", YouTube: stubYT{},
		Media: tool, Files: guard.With(filepath.Join(dir, "uploads")),
	})
	d := Deps{
		Store: st, Auth: svc, Clock: clk, Log: log, Version: "test", Pipeline: p, Queue: q,
		Paths: guard, Inbox: inbox, DataDir: dir,
	}
	if mod != nil {
		mod(&d)
	}
	r := &localRig{t: t, h: New(d), svc: svc, st: st, p: p, dir: dir, root: root, inbox: inbox}
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader([]byte(`{"password":"pw"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookie:
			r.sess = c.Value
		case auth.CSRFCookie:
			r.csrf = c.Value
		}
	}
	if r.sess == "" {
		t.Fatalf("登入失敗：%d %s", rec.Code, rec.Body)
	}
	return r
}

func (r *localRig) do(method, path, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func (r *localRig) owner(mod func(*http.Request)) func(*http.Request) {
	return func(req *http.Request) {
		withCookies(r.sess, r.csrf, r.csrf)(req)
		if mod != nil {
			mod(req)
		}
	}
}

func (r *localRig) token(scopes ...string) func(*http.Request) {
	r.t.Helper()
	ct, err := r.svc.CreateToken(context.Background(), "t-"+scopes[0], scopes, nil)
	if err != nil {
		r.t.Fatal(err)
	}
	return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+ct.Token) }
}

// tmpEntries 回傳 <data>/tmp 底下剩下的項目（上傳的暫存檔必須都被清掉）。
func (r *localRig) tmpEntries() []string {
	r.t.Helper()
	ents, err := os.ReadDir(filepath.Join(r.dir, "tmp"))
	if err != nil && !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

type multipartFile struct {
	field, name, content string
}

// multipartBody 組出 multipart/form-data；files 依序寫入，fields 是文字欄位。
func multipartBody(t *testing.T, fields [][2]string, files ...multipartFile) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+f.field+`"; filename="`+f.name+`"`)
		h.Set("Content-Type", "application/octet-stream")
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, f.content)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func (r *localRig) upload(name, content string, who func(*http.Request)) *httptest.ResponseRecorder {
	r.t.Helper()
	body, ct := multipartBody(r.t, nil, multipartFile{"file", name, content})
	return r.uploadBody(body, ct, who)
}

func (r *localRig) uploadBody(body io.Reader, ct string, who func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/items", body)
	req.Header.Set("Content-Type", ct)
	if who == nil {
		who = r.owner(nil)
	}
	who(req)
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

// readCounter 記錄本體被讀取的次數，用來確認被拒絕的請求沒有讀取本體。
type readCounter struct {
	n    int
	size int
}

func (c *readCounter) Read(p []byte) (int, error) {
	c.n++
	return 0, io.EOF
}
