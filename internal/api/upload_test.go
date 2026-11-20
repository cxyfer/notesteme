package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestUploadCreated(t *testing.T) {
	r := newLocalRig(t, nil)
	rec := r.upload("talk.m4a", "AUDIO-one", nil)
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	body := decode[itemsBody](t, rec)
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v", body.Items)
	}
	it := body.Items[0]
	if it.Ref != "local:talk.m4a" || it.Platform != "local" || it.ExternalID != "talk.m4a" || it.AlreadyInLibrary || it.InTrash {
		t.Errorf("item = %+v", it)
	}
	got, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != "upload" || got.ContentSHA256 != sha("AUDIO-one") || got.SourcePath != "uploads/"+it.ID+"/talk.m4a" {
		t.Errorf("stored = %+v", got)
	}
	if b, err := os.ReadFile(filepath.Join(r.dir, got.SourcePath)); err != nil || string(b) != "AUDIO-one" {
		t.Errorf("上傳檔 = %q %v", b, err)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}

// 撞名且內容不同：ref 是 local:talk-2.m4a（插在副檔名前）。
func TestUploadNameCollision(t *testing.T) {
	r := newLocalRig(t, nil)
	r.upload("talk.m4a", "AUDIO-one", nil)
	rec := r.upload("talk.m4a", "AUDIO-two", nil)
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if ref := decode[itemsBody](t, rec).Items[0].Ref; ref != "local:talk-2.m4a" {
		t.Errorf("ref = %q", ref)
	}
}

// 相同內容：200 already_in_library，指向既有影音；暫存檔清乾淨。
func TestUploadSameContent(t *testing.T) {
	r := newLocalRig(t, nil)
	first := decode[itemsBody](t, r.upload("talk.m4a", "AUDIO-one", nil)).Items[0]
	rec := r.upload("copy.m4a", "AUDIO-one", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	it := decode[itemsBody](t, rec).Items[0]
	if it.ID != first.ID || !it.AlreadyInLibrary || it.Ref != "local:talk.m4a" {
		t.Errorf("item = %+v", it)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
	if items, _ := r.p.Items(context.Background()); len(items) != 1 {
		t.Errorf("影音數 = %d", len(items))
	}
}

// 相同內容在垃圾桶中：409 in_trash，帶 item_id 與「還原」。
func TestUploadSameContentInTrash(t *testing.T) {
	r := newLocalRig(t, nil)
	first := decode[itemsBody](t, r.upload("talk.m4a", "AUDIO-one", nil)).Items[0]
	if _, err := r.p.Trash(context.Background(), first.ID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	rec := r.upload("copy.m4a", "AUDIO-one", nil)
	if rec.Code != 409 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	e := decode[Error](t, rec)
	if e.Code != CodeInTrash || e.ItemID != first.ID || len(e.Actions) != 1 || e.Actions[0] != "還原" {
		t.Errorf("error = %+v", e)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}

// 不是影音：422 unsupported_media，暫存檔與 uploads 都沒有留下。
func TestUploadNotMedia(t *testing.T) {
	r := newLocalRig(t, nil)
	rec := r.upload("notes.pdf", "just text", nil)
	if rec.Code != 422 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if e := decode[Error](t, rec); e.Code != CodeUnsupportedMedia || e.Message != "這個檔案沒有音訊或影片，無法新增。" {
		t.Errorf("error = %+v", e)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "uploads")); !os.IsNotExist(err) {
		t.Errorf("uploads 不該存在：%v", err)
	}
	if items, _ := r.p.Items(context.Background()); len(items) != 0 {
		t.Errorf("不該建立影音：%d", len(items))
	}
}

// 超過上限：宣告的長度太大時 413，而且完全不讀本體。
func TestUploadTooLargeByContentLength(t *testing.T) {
	r := newLocalRig(t, func(d *Deps) { d.UploadMaxBytes = 1 << 20 })
	rc := &readCounter{}
	rec := r.uploadBody(rc, "multipart/form-data; boundary=x", func(req *http.Request) {
		r.owner(nil)(req)
		req.ContentLength = 1<<20 + uploadFormOverhead + 1
	})
	if rec.Code != 413 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if e := decode[Error](t, rec); e.Code != CodeUploadTooLarge || e.Message != "上傳的檔案超過大小上限（1 MiB）。" {
		t.Errorf("error = %+v", e)
	}
	if rc.n != 0 {
		t.Errorf("本體被讀取了 %d 次", rc.n)
	}
}

// 預設上限的訊息是 4 GiB。
func TestUploadTooLargeDefaultMessage(t *testing.T) {
	r := newLocalRig(t, nil)
	rc := &readCounter{}
	rec := r.uploadBody(rc, "multipart/form-data; boundary=x", func(req *http.Request) {
		r.owner(nil)(req)
		req.ContentLength = 5 << 30
	})
	if rec.Code != 413 || !strings.Contains(rec.Body.String(), "4 GiB") || rc.n != 0 {
		t.Fatalf("status = %d reads = %d：%s", rec.Code, rc.n, rec.Body)
	}
}

// 沒有宣告長度（chunked）時，串流寫入超過上限就 413，暫存檔清乾淨。
func TestUploadTooLargeWhileStreaming(t *testing.T) {
	r := newLocalRig(t, func(d *Deps) { d.UploadMaxBytes = 100 })
	body, ct := multipartBody(t, nil, multipartFile{"file", "big.m4a", "AUDIO" + strings.Repeat("x", 500)})
	rec := r.uploadBody(strings.NewReader(body.String()), ct, func(req *http.Request) {
		r.owner(nil)(req)
		req.ContentLength = -1
	})
	if rec.Code != 413 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}

// scope 不足：在讀取本體之前就 403。
func TestUploadScopeBeforeBody(t *testing.T) {
	r := newLocalRig(t, nil)
	rc := &readCounter{}
	rec := r.uploadBody(rc, "multipart/form-data; boundary=x", r.token(auth.ScopeRead))
	if rec.Code != 403 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if e := decode[Error](t, rec); e.RequiredScope != auth.ScopeIngest {
		t.Errorf("error = %+v", e)
	}
	if rc.n != 0 {
		t.Errorf("本體被讀取了 %d 次", rc.n)
	}
	// 有 ingest scope 的 token 可以上傳。
	if rec := r.upload("talk.m4a", "AUDIO-one", r.token(auth.ScopeIngest)); rec.Code != 201 {
		t.Errorf("ingest token：%d %s", rec.Code, rec.Body)
	}
}

// 沒有登入：401；CSRF 仍然生效：session 缺少或錯誤的 CSRF 標頭是 403，本體不讀。
func TestUploadAuthAndCSRF(t *testing.T) {
	r := newLocalRig(t, nil)
	rc := &readCounter{}
	rec := r.uploadBody(rc, "multipart/form-data; boundary=x", func(req *http.Request) {})
	if rec.Code != 401 || rc.n != 0 {
		t.Errorf("未登入：%d reads=%d", rec.Code, rc.n)
	}
	for name, header := range map[string]string{"缺少": "", "錯誤": "wrong"} {
		rc := &readCounter{}
		rec := r.uploadBody(rc, "multipart/form-data; boundary=x", func(req *http.Request) {
			withCookies(r.sess, r.csrf, header)(req)
		})
		if rec.Code != 403 || decode[Error](t, rec).Code != CodeCSRF || rc.n != 0 {
			t.Errorf("CSRF %s：%d reads=%d %s", name, rec.Code, rc.n, rec.Body)
		}
	}
	if items, _ := r.p.Items(context.Background()); len(items) != 0 {
		t.Errorf("不該建立影音：%d", len(items))
	}
}

// 欄位檢查：其他欄位、重複的 file、沒有 file、不正確的檔名都是 422，暫存檔清乾淨。
func TestUploadFieldValidation(t *testing.T) {
	r := newLocalRig(t, nil)
	file := multipartFile{"file", "talk.m4a", "AUDIO-one"}
	for name, c := range map[string]struct {
		fields [][2]string
		files  []multipartFile
	}{
		"其他欄位":              {[][2]string{{"extra", "1"}}, []multipartFile{file}},
		"兩個檔案":              {nil, []multipartFile{file, file}},
		"沒有檔案":              {[][2]string{{"until", "metadata"}}, nil},
		"檔名含反斜線":            {nil, []multipartFile{{"file", `a\b.m4a`, "AUDIO"}}},
		"檔名是點點":             {nil, []multipartFile{{"file", "..", "AUDIO"}}},
		"檔名空白":              {nil, []multipartFile{{"file", "", "AUDIO"}}},
		"overrides 不是 JSON": {[][2]string{{"overrides", "nope"}}, []multipartFile{file}},
		"until 不合法":         {[][2]string{{"until", "bogus"}}, []multipartFile{file}},
	} {
		body, ct := multipartBody(t, c.fields, c.files...)
		rec := r.uploadBody(body, ct, nil)
		if rec.Code != 422 {
			t.Errorf("%s：status = %d：%s", name, rec.Code, rec.Body)
		}
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
	if items, _ := r.p.Items(context.Background()); len(items) != 0 {
		t.Errorf("不該建立影音：%d", len(items))
	}
}

// until 與 profile：until 記在影音上，profile 回 501（同 JSON 新增）。
func TestUploadUntilAndProfile(t *testing.T) {
	r := newLocalRig(t, nil)
	body, ct := multipartBody(t, [][2]string{{"until", "metadata"}, {"overrides", `{}`}}, multipartFile{"file", "talk.m4a", "AUDIO-one"})
	rec := r.uploadBody(body, ct, nil)
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if u := decode[itemsBody](t, rec).Items[0].Until; u != "metadata" {
		t.Errorf("until = %q", u)
	}
	body, ct = multipartBody(t, [][2]string{{"profile", "fast"}}, multipartFile{"file", "other.m4a", "AUDIO-two"})
	if rec := r.uploadBody(body, ct, nil); rec.Code != 501 {
		t.Errorf("profile：%d %s", rec.Code, rec.Body)
	}
	if left := r.tmpEntries(); len(left) != 0 {
		t.Errorf("暫存檔沒有清乾淨：%v", left)
	}
}

// JSON 新增不受上傳攔截影響。
func TestJSONCreateStillWorksWithUploadIntercept(t *testing.T) {
	r := newLocalRig(t, nil)
	rec := r.do("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`, r.owner(func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
	}))
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
}

// path 新增：201、檔案不存在仍 201、同鍵 200、422、403。
func TestCreateByPath(t *testing.T) {
	r := newLocalRig(t, nil)
	post := func(body string) *httptest.ResponseRecorder {
		return r.do("POST", "/api/items", body, r.owner(func(req *http.Request) { req.Header.Set("Content-Type", "application/json") }))
	}
	// inbox 中的檔案：ref 是相對 inbox 的路徑。
	if err := os.WriteFile(filepath.Join(r.inbox, "talk.mp4"), []byte("VIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := post(`{"path":"` + filepath.Join(r.inbox, "talk.mp4") + `"}`)
	if rec.Code != 201 {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body)
	}
	if it := decode[itemsBody](t, rec).Items[0]; it.Ref != "local:talk.mp4" || it.URL != "" || it.Platform != "local" {
		t.Errorf("item = %+v", it)
	}
	// 同一個檔案再加一次：200 already_in_library。
	rec = post(`{"path":"` + filepath.Join(r.inbox, "talk.mp4") + `"}`)
	if rec.Code != 200 || !decode[itemsBody](t, rec).Items[0].AlreadyInLibrary {
		t.Errorf("again：%d %s", rec.Code, rec.Body)
	}
	// 檔案不存在：延遲驗證，仍是 201；media_roots 中 inbox 之外的檔案，ref 是絕對路徑。
	missing := filepath.Join(r.root, "nowhere", "ghost.mp4")
	rec = post(`{"path":"` + missing + `"}`)
	if rec.Code != 201 || decode[itemsBody](t, rec).Items[0].Ref != "local:"+missing {
		t.Errorf("不存在：%d %s", rec.Code, rec.Body)
	}
	// 目錄：422 not_a_file。
	rec = post(`{"path":"` + r.inbox + `"}`)
	if rec.Code != 422 || decode[Error](t, rec).Code != CodeNotAFile {
		t.Errorf("目錄：%d %s", rec.Code, rec.Body)
	}
	// 同時給 url 與 path：422。
	rec = post(`{"path":"` + missing + `","url":"https://www.youtube.com/watch?v=abc123xxxxx"}`)
	if rec.Code != 422 {
		t.Errorf("url 與 path：%d %s", rec.Code, rec.Body)
	}
	// root 之外：403。
	if rec = post(`{"path":"/etc/passwd"}`); rec.Code != 403 {
		t.Errorf("root 之外：%d %s", rec.Code, rec.Body)
	}
	// 垃圾桶中：409。
	first := decode[itemsBody](t, post(`{"path":"`+filepath.Join(r.inbox, "talk.mp4")+`"}`)).Items[0]
	if _, err := r.p.Trash(context.Background(), first.ID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if rec = post(`{"path":"` + filepath.Join(r.inbox, "talk.mp4") + `"}`); rec.Code != 409 {
		t.Errorf("垃圾桶：%d %s", rec.Code, rec.Body)
	}
}
