package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/bundle"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是 bundle 的匯出與匯入（BND-R1、R3、R9、R10）。
// 匯入在進入 Huma 之前由 bundleImportIntercept 處理：先檢查 scope，再依 Content-Length 與實際讀到的大小回 413 bundle_too_large
// （Huma 的 MaxBodyBytes 只會回通用的 413）；Huma 的 operation 只用來產生 OpenAPI。

const bundleImportPath = "/api/bundles/import"

// CodeBundleTooLarge 是上傳超過 bundles.import.max_upload_bytes（BND-R3）。
const CodeBundleTooLarge = "bundle_too_large"

// bundleFormOverhead 是 multipart 本體在檔案之外的餘裕（邊界、標頭）。
const bundleFormOverhead = 64 << 10

// bundleDrainBytes 是回 413 之前最多丟棄的剩餘本體（同 importDrainBytes 的理由）。
const bundleDrainBytes = 8 << 20

func (rt *routes) registerBundles(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-bundle", Method: http.MethodGet, Path: "/api/items/{id}/bundle",
		Summary: "匯出影音的 bundle（.notesteme.zip，BND-R1）：現行版本的字幕、筆記、章節、贊助片段與選中的畫面，不含媒體檔與 secrets",
	}, auth.ScopeRead, rt.exportBundle)
	registerScoped(api, huma.Operation{
		OperationID: "bundles-import", Method: http.MethodPost, Path: bundleImportPath,
		Summary: "匯入 bundle（multipart 的 file，BND-R3）：全部驗證通過才寫入；成功回 201 {item, imported, skipped}，" +
			"不通過回 422 bundle_invalid／bundle_unsupported_version，已存在或在垃圾桶中回 409，超過上傳上限回 413 bundle_too_large",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.importBundleDoc)
}

type bundleExportInput struct {
	ID string `path:"id" doc:"影音的 ID 或短代號"`
}

func (rt *routes) exportBundle(ctx context.Context, in *bundleExportInput) (*huma.StreamResponse, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	ex, err := rt.d.Pipeline.ExportBundle(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", "application/zip")
		hc.SetHeader("Content-Disposition", `attachment; filename="`+ex.Name+`"`)
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetStatus(http.StatusOK)
		if _, err := hc.BodyWriter().Write(ex.Data); err != nil {
			rt.d.Log.WarnContext(hc.Context(), "輸出 bundle 時中斷", "error", err.Error())
		}
	}}, nil
}

// BundleImportItem 是匯入建立（或加上版本）的影音。
type BundleImportItem struct {
	ID    string `json:"id"`
	Ref   string `json:"ref" doc:"影音的短代號"`
	Title string `json:"title"`
}

// BundleImportResult 是匯入的結果（BND-R3）。
type BundleImportResult struct {
	Item     BundleImportItem `json:"item"`
	Imported int              `json:"imported" doc:"匯入的產出數"`
	Skipped  int              `json:"skipped" doc:"內容與既有版本相同而略過的產出數（on_existing=add_versions）"`
}

type bundleImportDocInput struct {
	OnExisting string `query:"on_existing" enum:"add_versions" required:"false" doc:"影音已經存在時：add_versions 把 bundle 的產出加成新版本；省略時回 409"`
	RawBody    huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" doc:"bundle（.notesteme.zip）"`
	}]
}

type bundleImportOutput struct{ Body BundleImportResult }

// importBundleDoc 只用來產生 OpenAPI：實際的請求由 bundleImportIntercept 處理，不會到這裡。
func (rt *routes) importBundleDoc(ctx context.Context, _ *bundleImportDocInput) (*bundleImportOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	return nil, newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}

// bundleLimits 把設定轉成驗證的上限；零值用預設值。
func bundleLimits(c config.BundleImport) (maxUpload int64, lim bundle.Limits) {
	maxUpload = c.MaxUploadBytes
	if maxUpload <= 0 {
		maxUpload = 64 << 20
	}
	return maxUpload, bundle.Limits{MaxFiles: c.MaxFiles, MaxFileBytes: c.MaxFileBytes, MaxTotalBytes: c.MaxTotalBytes, MaxRatio: c.MaxRatio}
}

// bundleImportIntercept 處理 POST /api/bundles/import（BND-R3）：scope 在讀取本體之前檢查。
func bundleImportIntercept(rt *routes, d Deps) func(http.Handler) http.Handler {
	maxUpload, lim := bundleLimits(d.BundleImport)
	tooLarge := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, bundleDrainBytes))
		writeError(w, newError(http.StatusRequestEntityTooLarge, CodeBundleTooLarge,
			"bundle 超過 "+bundle.FormatSize(maxUpload)+" 的上傳上限。"))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != bundleImportPath || r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()
			if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
				writeError(w, asError(err))
				return
			}
			if d.Pipeline == nil {
				writeError(w, asError(rt.noPipeline(ctx)))
				return
			}
			onExisting := r.URL.Query().Get("on_existing")
			if onExisting != "" && onExisting != "add_versions" {
				e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, "on_existing 只能是 add_versions。")
				e.Errors = []validate.FieldError{{Path: "on_existing", Message: "on_existing 只能是 add_versions。"}}
				writeError(w, e)
				return
			}
			if r.ContentLength > maxUpload+bundleFormOverhead {
				tooLarge(w, r)
				return
			}
			if !isMultipart(r) {
				writeError(w, missingBundle())
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxUpload+bundleFormOverhead)
			data, err := readBundlePart(r, maxUpload)
			var mbe *http.MaxBytesError
			switch {
			case errors.Is(err, errBundlePartTooLarge) || errors.As(err, &mbe):
				tooLarge(w, r)
				return
			case errors.Is(err, errNoBundlePart):
				writeError(w, missingBundle())
				return
			case err != nil:
				writeError(w, newError(http.StatusBadRequest, CodeInvalidInput, defaultMessages[http.StatusBadRequest].msg))
				return
			}
			res, err := d.Pipeline.ImportBundle(ctx, pipeline.BundleImportRequest{Data: data, Limits: lim, AddVersions: onExisting == "add_versions"})
			if err != nil {
				writeError(w, rt.mapBundleErr(ctx, err))
				return
			}
			writeJSON(w, http.StatusCreated, BundleImportResult{
				Item:     BundleImportItem{ID: res.Item.ID, Ref: res.Ref, Title: res.Item.Title},
				Imported: res.Imported, Skipped: res.Skipped,
			})
		})
	}
}

var (
	errBundlePartTooLarge = errors.New("bundle part too large")
	errNoBundlePart       = errors.New("no file part")
)

// maxBundleParts 是 multipart 中最多讀幾個 part（file 以外的 part 一律丟棄）。
const maxBundleParts = 8

// readBundlePart 讀出 multipart 的 file 欄位，最多 maxUpload bytes。
func readBundlePart(r *http.Request, maxUpload int64) ([]byte, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}
	for i := 0; i < maxBundleParts; i++ {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, errNoBundlePart
		}
		if err != nil {
			return nil, err
		}
		if part.FormName() != "file" {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxUpload+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > maxUpload {
			return nil, errBundlePartTooLarge
		}
		return data, nil
	}
	return nil, errNoBundlePart
}

func missingBundle() *Error {
	e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請上傳 bundle（multipart 的 file 欄位）。")
	e.Errors = []validate.FieldError{{Path: "file", Message: "請上傳 bundle（multipart 的 file 欄位）。"}}
	return e
}

// mapBundleErr 對應匯入的錯誤（BND-R9、R10）；其他交給 mapErr。
func (rt *routes) mapBundleErr(ctx context.Context, err error) *Error {
	var (
		be *bundle.Error
		ce *pipeline.BundleConflictError
	)
	switch {
	case errors.As(err, &be):
		e := newError(http.StatusUnprocessableEntity, be.Code, be.Message)
		e.Entry, e.ErrorClass = be.Entry, string(errclass.ClassPermanent)
		if be.Path != "" {
			e.Errors = []validate.FieldError{{Path: be.Path, Message: be.Message}}
		}
		return e
	case errors.As(err, &ce):
		return newError(http.StatusConflict, ce.Code, ce.Message)
	case errors.Is(err, context.Canceled):
		return newError(http.StatusServiceUnavailable, CodeInternal, "請求已取消。")
	}
	rt.d.Log.LogAttrs(ctx, slog.LevelError, "匯入 bundle 時發生錯誤", slog.String("error", err.Error()))
	return asError(rt.mapErr(ctx, err))
}
