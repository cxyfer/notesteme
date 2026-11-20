package api

import (
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 媒體端點的錯誤代碼（404）。
const (
	// CodeNoMedia：這部影音沒有可以串流的媒體（不是本機影音，也沒有快取）。
	CodeNoMedia = "no_media"
	// CodeMediaMissing：本機檔案已經不在原處。
	CodeMediaMissing = "media_missing"
)

// mediaTypes 是媒體端點回應的 Content-Type 白名單（依副檔名）；其他一律 application/octet-stream，
// 不讓使用者檔案的副檔名決定瀏覽器如何解讀它（例如 .html）。
var mediaTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4a":  "audio/mp4",
	".mp3":  "audio/mpeg",
	".mkv":  "video/x-matroska",
	".webm": "video/webm",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".mov":  "video/quicktime",
	".aac":  "audio/aac",
}

func mediaContentType(name string) string {
	if t, ok := mediaTypes[strings.ToLower(path.Ext(name))]; ok {
		return t
	}
	return "application/octet-stream"
}

// registerMedia 掛上 GET /api/items/{id}/media（HEAD 由 ServeMux 一併處理）：
// 串流本機影音的檔案，支援 Range（206、416、If-Range）。需要 read scope。
// 它是串流回應，不經過 Huma，直接掛在 mux 上，但仍在驗證 middleware 之後。
func (rt *routes) registerMedia(mux *http.ServeMux) {
	mux.Handle("GET /api/items/{id}/media", requireScopeHandler(auth.ScopeRead, http.HandlerFunc(rt.serveMedia)))
}

func (rt *routes) serveMedia(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if rt.d.Pipeline == nil {
		writeError(w, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。"))
		return
	}
	f, fi, it, err := rt.d.Pipeline.MediaFile(ctx, r.PathValue("id"))
	switch {
	case err == nil:
	case errors.Is(err, pipeline.ErrNoMedia):
		writeError(w, newError(http.StatusNotFound, CodeNoMedia, err.Error()))
		return
	case errors.Is(err, pipeline.ErrMediaMissing):
		writeError(w, newError(http.StatusNotFound, CodeMediaMissing, err.Error()))
		return
	case errors.Is(err, pathguard.ErrOutsideRoots):
		writeError(w, newError(http.StatusForbidden, CodePathOutsideRoots, "只能讀取媒體資料夾中的檔案。"))
		return
	default:
		writeError(w, asError(rt.mapErr(ctx, err)))
		return
	}
	defer f.Close()

	name := filepath.Base(it.SourcePath)
	h := w.Header()
	h.Set("Content-Type", mediaContentType(name))
	// 使用者的檔案不該被瀏覽器當成頁面執行。
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if it.ContentSHA256 != "" {
		h.Set("ETag", `"`+it.ContentSHA256+`"`)
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}
