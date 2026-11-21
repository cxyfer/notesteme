package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

const mediaContent = "VIDEO-0123456789abcdefghijklmnopqrstuvwxyz"

// ingestMediaFile 在 media_roots 寫一個檔案並以 path 新增，回傳影音 ID。
func (r *localRig) ingestMediaFile(name, content string) string {
	r.t.Helper()
	p := filepath.Join(r.root, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	rec := r.do("POST", "/api/items", `{"path":"`+p+`"}`, r.owner(func(req *http.Request) { req.Header.Set("Content-Type", "application/json") }))
	if rec.Code != 201 {
		r.t.Fatalf("新增 %s：%d %s", name, rec.Code, rec.Body)
	}
	return decode[itemsBody](r.t, rec).Items[0].ID
}

func (r *localRig) media(id string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return r.do("GET", "/api/items/"+id+"/media", "", r.owner(mod))
}

func TestMediaServesFileWithHeaders(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	rec := r.media(id, nil)
	if rec.Code != 200 || rec.Body.String() != mediaContent {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	for k, want := range map[string]string{
		"Content-Type":            "video/mp4",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'",
		"Accept-Ranges":           "bytes",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q，要 %q", k, got, want)
		}
	}
	// 用路徑新增時還沒有雜湊：沒有 ETag。
	if h.Get("ETag") != "" {
		t.Errorf("ETag = %q", h.Get("ETag"))
	}
}

func TestMediaRange(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	rec := r.media(id, func(req *http.Request) { req.Header.Set("Range", "bytes=6-15") })
	if rec.Code != 206 || rec.Body.String() != mediaContent[6:16] {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 6-15/"+strconv.Itoa(len(mediaContent)) {
		t.Errorf("Content-Range = %q", cr)
	}
	// 超出範圍：416。
	rec = r.media(id, func(req *http.Request) { req.Header.Set("Range", "bytes=9999-") })
	if rec.Code != 416 {
		t.Errorf("超出範圍 status = %d", rec.Code)
	}
}

func TestMediaHead(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	rec := r.do("HEAD", "/api/items/"+id+"/media", "", r.owner(nil))
	if rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != strconv.Itoa(len(mediaContent)) ||
		rec.Header().Get("Content-Type") != "video/mp4" {
		t.Errorf("status = %d len = %d headers = %v", rec.Code, rec.Body.Len(), rec.Header())
	}
}

// 上傳的檔案有雜湊：ETag 是 "<sha256>"，If-None-Match 命中回 304，If-Range 不符回完整內容。
func TestMediaETagAndConditional(t *testing.T) {
	r := newLocalRig(t, nil)
	id := decode[itemsBody](t, r.upload("talk.m4a", "AUDIO-uploaded", nil)).Items[0].ID
	rec := r.media(id, nil)
	etag := `"` + sha("AUDIO-uploaded") + `"`
	if rec.Code != 200 || rec.Header().Get("ETag") != etag || rec.Header().Get("Content-Type") != "audio/mp4" || rec.Body.String() != "AUDIO-uploaded" {
		t.Fatalf("status = %d etag = %q type = %q body = %q", rec.Code, rec.Header().Get("ETag"), rec.Header().Get("Content-Type"), rec.Body)
	}
	if rec := r.media(id, func(req *http.Request) { req.Header.Set("If-None-Match", etag) }); rec.Code != 304 {
		t.Errorf("If-None-Match：%d", rec.Code)
	}
	rec = r.media(id, func(req *http.Request) { req.Header.Set("Range", "bytes=0-3"); req.Header.Set("If-Range", etag) })
	if rec.Code != 206 || rec.Body.String() != "AUDI" {
		t.Errorf("If-Range 相符：%d %q", rec.Code, rec.Body)
	}
	rec = r.media(id, func(req *http.Request) { req.Header.Set("Range", "bytes=0-3"); req.Header.Set("If-Range", `"other"`) })
	if rec.Code != 200 || rec.Body.String() != "AUDIO-uploaded" {
		t.Errorf("If-Range 不符：%d %q", rec.Code, rec.Body)
	}
}

// 用路徑加入的檔案：雜湊與記錄的 size、mtime 都和目前的檔案相同時才用雜湊當 ETag；
// 同一路徑的檔案被替換之後，不再送出舊的雜湊，舊的 If-None-Match 也不會得到 304。
func TestMediaETagForReplacedPathFile(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	p := filepath.Join(r.root, "talk.mp4")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	old := sha(mediaContent)
	meta := fmt.Sprintf(`{"local":{"size":%d,"mtime_ns":%d}}`, fi.Size(), fi.ModTime().UnixNano())
	if err := r.st.Write().UpdateItemMeta(context.Background(), id, store.ItemMeta{Title: "talk", Metadata: []byte(meta), ContentSHA256: old}); err != nil {
		t.Fatal(err)
	}
	if rec := r.media(id, nil); rec.Header().Get("ETag") != `"`+old+`"` {
		t.Fatalf("檔案沒變時 ETag = %q", rec.Header().Get("ETag"))
	}

	if err := os.WriteFile(p, []byte("REPLACED-"+mediaContent), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := r.media(id, func(req *http.Request) { req.Header.Set("If-None-Match", `"`+old+`"`) })
	if rec.Code != 200 || rec.Header().Get("ETag") != "" || rec.Body.String() != "REPLACED-"+mediaContent {
		t.Errorf("替換後 status = %d、ETag = %q、body = %q", rec.Code, rec.Header().Get("ETag"), rec.Body)
	}
}

// Content-Type 只認白名單；其他副檔名（例如 .html）一律 octet-stream。
func TestMediaContentTypeWhitelist(t *testing.T) {
	r := newLocalRig(t, nil)
	for name, want := range map[string]string{
		"a.mp4": "video/mp4", "a.m4a": "audio/mp4", "a.mp3": "audio/mpeg", "a.mkv": "video/x-matroska", "a.webm": "video/webm",
		"a.ogg": "audio/ogg", "a.opus": "audio/ogg", "a.wav": "audio/wav", "a.flac": "audio/flac", "a.mov": "video/quicktime",
		"a.aac": "audio/aac", "A.MP4": "video/mp4", "a.html": "application/octet-stream", "a.svg": "application/octet-stream",
		"noext": "application/octet-stream",
	} {
		id := r.ingestMediaFile(name, "VIDEO")
		rec := r.media(id, nil)
		if got := rec.Header().Get("Content-Type"); rec.Code != 200 || got != want {
			t.Errorf("%s：status = %d Content-Type = %q，要 %q", name, rec.Code, got, want)
		}
	}
}

func TestMediaErrors(t *testing.T) {
	r := newLocalRig(t, nil)
	ctx := context.Background()

	// 垃圾桶：404 in_trash，帶 item_id。
	trashID := r.ingestMediaFile("trashed.mp4", "VIDEO")
	if _, err := r.p.Trash(ctx, trashID, pipeline.TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	rec := r.media(trashID, nil)
	if e := decode[Error](t, rec); rec.Code != 404 || e.Code != CodeInTrash || e.ItemID != trashID {
		t.Errorf("垃圾桶：%d %+v", rec.Code, e)
	}

	// 非本機影音沒有媒體：404 no_media。
	rec = r.do("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`, r.owner(func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
	}))
	ytID := decode[itemsBody](t, rec).Items[0].ID
	rec = r.media(ytID, nil)
	if e := decode[Error](t, rec); rec.Code != 404 || e.Code != CodeNoMedia {
		t.Errorf("no_media：%d %+v", rec.Code, e)
	}

	// 檔案不見：404 media_missing。
	missingID := r.ingestMediaFile("gone.mp4", "VIDEO")
	if err := os.Remove(filepath.Join(r.root, "gone.mp4")); err != nil {
		t.Fatal(err)
	}
	rec = r.media(missingID, nil)
	if e := decode[Error](t, rec); rec.Code != 404 || e.Code != CodeMediaMissing {
		t.Errorf("media_missing：%d %+v", rec.Code, e)
	}

	// 影音的檔案在 root 之外（例如 root 設定改過）：403。
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("VIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := r.p.IngestLocal(ctx, pipeline.LocalTarget{Key: outside, SourcePath: outside, Origin: store.OriginManual})
	if err != nil {
		t.Fatal(err)
	}
	rec = r.media(out.Item.ID, nil)
	if e := decode[Error](t, rec); rec.Code != 403 || e.Code != CodePathOutsideRoots {
		t.Errorf("root 之外：%d %+v", rec.Code, e)
	}

	// 沒有這部影音：404 not_found。
	rec = r.media("00000000-0000-0000-0000-000000000000", nil)
	if e := decode[Error](t, rec); rec.Code != 404 || e.Code != CodeNotFound {
		t.Errorf("不存在：%d %+v", rec.Code, e)
	}
}

func TestMediaScope(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)
	get := func(who func(*http.Request)) int {
		return r.do("GET", "/api/items/"+id+"/media", "", who).Code
	}
	if c := get(r.token(auth.ScopeRead)); c != 200 {
		t.Errorf("read scope：%d", c)
	}
	if c := get(r.token(auth.ScopeIngest)); c != 403 {
		t.Errorf("只有 ingest scope：%d", c)
	}
	if c := get(func(*http.Request) {}); c != 401 {
		t.Errorf("未登入：%d", c)
	}
}
