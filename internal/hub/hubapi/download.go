package hubapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/apierr"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubrepo"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 取用（M5-S4，HREP-R18）：下載渲染後的內容（download）與原始的 cues JSON（regenerate）。
// 可見度與版本詳情相同：看不到與不存在回相同的 404，看得到的 removed 回 410。
// 內容一律以附件下載：Content-Disposition 的檔名只由清理過的 ASCII 組成（external_id 來自不可信的 manifest），
// 加上 nosniff、private no-store 與 sandbox 的 CSP，瀏覽器不會把上傳的內容當作網頁執行。不支援 Range（規格沒有要求）。

// downloadFormats 是各種類可以下載的格式，第一個是預設值。frames 沒有可下載的格式。
var downloadFormats = map[string][]string{
	"transcript": {subtitle.FormatSRT, subtitle.FormatVTT, subtitle.FormatTXT, subtitle.FormatCuesJSON},
	"note":       {"md"},
	"chapters":   {"json"},
}

const contentSecurityPolicy = "default-src 'none'; sandbox"

type downloadInput struct {
	ID     string `path:"id" doc:"版本 ID"`
	Format string `query:"format" doc:"字幕 srt（預設）、vtt、txt、cues-json；筆記 md；章節 json"`
}

type contentInput struct {
	ID string `path:"id" doc:"版本 ID"`
}

// fileOutput 是下載與 content 的回應（本體是原始的 bytes）。
type fileOutput struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	NoSniff            string `header:"X-Content-Type-Options"`
	CacheControl       string `header:"Cache-Control"`
	CSP                string `header:"Content-Security-Policy"`
	License            string `header:"X-Notesteme-License"`
	SHA256             string `header:"X-Notesteme-SHA256"`
	Body               []byte
}

func registerDownload(api huma.API, rt *routes, reg func(huma.Operation, Access, string) huma.Operation, authErrs []int) {
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-download", Method: http.MethodGet, Path: "/api/v1/versions/{id}/download",
		Summary: "下載渲染後的字幕、筆記或章節（附件）",
		Errors:  append(authErrs, http.StatusNotFound, http.StatusGone, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermDownload), "HREP-R18"), rt.download)
	huma.Register(api, reg(huma.Operation{
		OperationID: "versions-content", Method: http.MethodGet, Path: "/api/v1/versions/{id}/content",
		Summary: "原始的 cues JSON（Client 重新生成用，只限字幕）",
		Errors:  append(authErrs, http.StatusNotFound, http.StatusGone, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermRegenerate), "HREP-R18"), rt.content)
}

// seeVersion 回傳 viewer 看得到的版本與影片：不存在或看不到 404，removed 410（同 getVersion）。
func (rt *routes) seeVersion(ctx context.Context, id string) (hubstore.Version, hubstore.Video, error) {
	viewer, err := rt.viewerOf(ctx)
	if err != nil {
		return hubstore.Version{}, hubstore.Video{}, rt.apiError(ctx, err)
	}
	v, err := rt.d.Store.Version(ctx, id)
	if errors.Is(err, hubstore.ErrNotFound) {
		return v, hubstore.Video{}, versionNotFound()
	}
	if err != nil {
		return v, hubstore.Video{}, rt.apiError(ctx, err)
	}
	if ok, _ := rt.canSee(ctx, v, viewer); !ok {
		return v, hubstore.Video{}, versionNotFound()
	}
	if v.State == hubrepo.StateRemoved {
		return v, hubstore.Video{}, apierr.New(http.StatusGone, codeVersionRemoved, msgVersionRemoved)
	}
	video, err := rt.d.Store.VideoByID(ctx, v.VideoID)
	if err != nil {
		return v, video, rt.apiError(ctx, err)
	}
	return v, video, nil
}

// readContent 讀出版本的內容 blob，並確認雜湊與版本相同（blob 被換掉或損壞時是 500，不送出）。
func (rt *routes) readContent(ctx context.Context, v hubstore.Version) ([]byte, error) {
	r, err := rt.d.Blobs.Open(ctx, v.SHA256)
	if err != nil {
		return nil, rt.apiError(ctx, fmt.Errorf("開啟版本內容：%w", err))
	}
	defer func() { _ = r.Close() }()
	limit := rt.d.Config.Bundles.MaxFileBytes
	d, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, rt.apiError(ctx, fmt.Errorf("讀取版本內容：%w", err))
	}
	if int64(len(d)) > limit || sha256Hex(d) != v.SHA256 {
		return nil, rt.apiError(ctx, errors.New("版本內容與雜湊不符"))
	}
	return d, nil
}

// safeName 把檔名的一段清理成 [A-Za-z0-9._-]，其他字元換成底線，最多 64 個字元，不以點開頭。
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 64 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.TrimLeft(b.String(), ".")
}

// downloadName 是附件的檔名：<platform>-<external_id>[-p<n>]-<kind>[-<variant>][-<lang>].<ext>。
func downloadName(v hubstore.Version, video hubstore.Video, ext string) string {
	parts := []string{safeName(video.Platform), safeName(video.ExternalID)}
	if video.Part > 0 {
		parts = append(parts, "p"+strconv.Itoa(video.Part))
	}
	parts = append(parts, safeName(v.Kind))
	for _, p := range []string{v.Variant, v.Lang} {
		if s := safeName(p); s != "" {
			parts = append(parts, s)
		}
	}
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "-") + "." + ext
}

func attachment(name string) string { return `attachment; filename="` + name + `"` }

func (rt *routes) download(ctx context.Context, in *downloadInput) (*fileOutput, error) {
	v, video, err := rt.seeVersion(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	formats := downloadFormats[v.Kind]
	format := in.Format
	var errs fieldErrs
	switch {
	case len(formats) == 0:
		errs.add("query.format", v.Kind+" 版本沒有可下載的格式。")
	case format == "":
		format = formats[0]
	case !slices.Contains(formats, format):
		errs.add("query.format", v.Kind+" 版本的 format "+oneOf(formats))
	}
	if err := errs.err(); err != nil {
		return nil, err
	}
	data, err := rt.readContent(ctx, v)
	if err != nil {
		return nil, err
	}
	out := &fileOutput{NoSniff: "nosniff", CacheControl: "private, no-store", CSP: contentSecurityPolicy, License: safeName(v.License)}
	switch v.Kind {
	case "transcript":
		t, err := subtitle.DecodeCuesJSON(bytes.NewReader(data))
		if err != nil {
			return nil, rt.apiError(ctx, fmt.Errorf("解碼版本的 cues JSON：%w", err))
		}
		var buf bytes.Buffer
		if err := subtitle.Render(&buf, format, t); err != nil {
			return nil, rt.apiError(ctx, err)
		}
		out.Body, out.ContentType = buf.Bytes(), subtitle.ContentType(format)
		out.ContentDisposition = attachment(downloadName(v, video, subtitle.FileExt(format)))
	case "note":
		out.Body, out.ContentType = data, "text/markdown; charset=utf-8"
		out.ContentDisposition = attachment(downloadName(v, video, "md"))
	default:
		out.Body, out.ContentType = data, "application/json; charset=utf-8"
		out.ContentDisposition = attachment(downloadName(v, video, "json"))
	}
	return out, nil
}

// content 回傳字幕版本原始的 cues JSON（bytes 與 X-Notesteme-SHA256 相符，Client 重新生成前可以自行驗證）。
func (rt *routes) content(ctx context.Context, in *contentInput) (*fileOutput, error) {
	v, video, err := rt.seeVersion(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if v.Kind != "transcript" {
		return nil, apierr.New(http.StatusUnprocessableEntity, apierr.CodeInvalidInput, "只有字幕版本有原始的 cues JSON。")
	}
	data, err := rt.readContent(ctx, v)
	if err != nil {
		return nil, err
	}
	return &fileOutput{
		ContentType: "application/json; charset=utf-8", NoSniff: "nosniff", CacheControl: "private, no-store",
		CSP: contentSecurityPolicy, License: safeName(v.License), SHA256: v.SHA256, Body: data,
		ContentDisposition: attachment(downloadName(v, video, "cues.json")),
	}, nil
}
