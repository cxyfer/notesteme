package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

const (
	frameFull  = "FULL-JPEG-0123456789abcdefghij"
	frameSmall = "THUMB-JPEG-0123"
	frameVer   = "frmver0123456789"
)

// seedFrames 直接寫入一個 frames 產出（manifest 與兩張候選的圖片），不經過 pipeline。
func (r *localRig) seedFrames(id string) {
	r.t.Helper()
	man := map[string]any{
		"width": 1920, "height": 1080, "target": 1, "range_min": 5, "range_max": 8, "select": "rules",
		"candidates": []map[string]any{
			{"n": 1, "at_ms": 1000, "score": 0.5, "file": "001.jpg", "thumb": "001.thumb.jpg", "selected": true, "caption": "c1"},
			{"n": 2, "at_ms": 9000, "score": 0.4, "file": "002.jpg", "thumb": "002.thumb.jpg", "excluded": "sponsor"},
		},
	}
	body, err := json.Marshal(man)
	if err != nil {
		r.t.Fatal(err)
	}
	rel := filepath.ToSlash(filepath.Join("artifacts", id, frameVer+".json"))
	dir := filepath.Join(r.dir, "artifacts", id, "frames", frameVer)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(r.dir, filepath.FromSlash(rel)): string(body),
		filepath.Join(dir, "001.jpg"):                 frameFull,
		filepath.Join(dir, "001.thumb.jpg"):           frameSmall,
		filepath.Join(dir, "002.jpg"):                 frameFull + "-2",
	}
	for p, c := range files {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	sum := sha256.Sum256(body)
	_, err = r.st.Write().InsertArtifact(context.Background(), store.NewArtifact{
		ID: frameVer, ItemID: id, Kind: "frames", Variant: "frames", Format: "notesteme-frames+json",
		Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Provenance: json.RawMessage(`{"stage":"frames"}`),
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func (r *localRig) frame(id, n, query string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return r.do("GET", "/api/items/"+id+"/frames/"+n+query, "", r.owner(mod))
}

func TestFrameImageHeadersAndSizes(t *testing.T) {
	r := newLocalRig(t, nil)
	id := r.ingestMediaFile("talk.mp4", mediaContent)

	// 還沒有 frames 產出：404 no_frames。
	if rec := r.frame(id, "1", "", nil); rec.Code != 404 || decode[Error](t, rec).Code != CodeNoFrames {
		t.Fatalf("沒有產出：%d %s", rec.Code, rec.Body)
	}
	r.seedFrames(id)

	rec := r.frame(id, "1", "", nil)
	if rec.Code != 200 || rec.Body.String() != frameFull {
		t.Fatalf("full：%d %q", rec.Code, rec.Body)
	}
	h := rec.Header()
	for k, want := range map[string]string{
		"Content-Type":            "image/jpeg",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'",
		"Cache-Control":           "private, max-age=0, must-revalidate",
		"Accept-Ranges":           "bytes",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q，要 %q", k, got, want)
		}
	}
	etag := h.Get("ETag")
	if etag == "" || etag[0] != '"' {
		t.Errorf("ETag = %q", etag)
	}

	// thumb 與 full 是不同的檔案，ETag 也不同。
	th := r.frame(id, "1", "?size=thumb", nil)
	if th.Code != 200 || th.Body.String() != frameSmall {
		t.Fatalf("thumb：%d %q", th.Code, th.Body)
	}
	if th.Header().Get("ETag") == etag {
		t.Error("thumb 與 full 的 ETag 相同")
	}
	if rec := r.frame(id, "1", "?size=full", nil); rec.Body.String() != frameFull {
		t.Errorf("size=full：%q", rec.Body)
	}

	// Range 與條件請求。
	rec = r.frame(id, "1", "", func(req *http.Request) { req.Header.Set("Range", "bytes=5-8") })
	if rec.Code != 206 || rec.Body.String() != frameFull[5:9] {
		t.Errorf("Range：%d %q", rec.Code, rec.Body)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 5-8/"+strconv.Itoa(len(frameFull)) {
		t.Errorf("Content-Range = %q", cr)
	}
	rec = r.frame(id, "1", "", func(req *http.Request) { req.Header.Set("If-None-Match", etag) })
	if rec.Code != 304 {
		t.Errorf("If-None-Match：%d", rec.Code)
	}

	// 錯誤：n 超出範圍、檔案不存在是 404 frame_not_found；n 與 size 不合法是 422。
	for _, tc := range []struct{ n, q string }{{"3", ""}, {"2", "?size=thumb"}} {
		rec := r.frame(id, tc.n, tc.q, nil)
		if rec.Code != 404 || decode[Error](t, rec).Code != CodeFrameNotFound {
			t.Errorf("n=%s%s：%d %s", tc.n, tc.q, rec.Code, rec.Body)
		}
	}
	for _, tc := range []struct{ n, q string }{{"0", ""}, {"x", ""}, {"-1", ""}, {"1", "?size=bogus"}} {
		if rec := r.frame(id, tc.n, tc.q, nil); rec.Code != 422 {
			t.Errorf("n=%s%s：%d，要 422", tc.n, tc.q, rec.Code)
		}
	}

	// 列表端點與圖片端點看到同一份 manifest。
	list := r.do("GET", "/api/items/"+id+"/frames", "", r.owner(nil))
	fl := decode[FramesList](t, list)
	if list.Code != 200 || fl.CandidateCount != 2 || fl.SelectedCount != 1 || fl.Frames[1].Excluded == nil || *fl.Frames[1].Excluded != "sponsor" {
		t.Errorf("列表：%d %s", list.Code, list.Body)
	}
}
