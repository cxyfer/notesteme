package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/subscription"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是訂閱的試跑預覽與匯入（SUB-R6、R7，M4-S3）。兩者都要 admin：預覽會連外，匯入會建立大量物件（SUB-R13）。

// SubscriptionPreviewer 是預覽與匯入需要的 service；*subscription.Service 實作它。
type SubscriptionPreviewer interface {
	PreviewNew(ctx context.Context, url string, in subscription.PreviewInput) (subscription.Preview, error)
	PreviewExisting(ctx context.Context, id string, in subscription.PreviewInput) (subscription.Preview, error)
	Import(ctx context.Context, data []byte) (subscription.ImportResult, error)
}

var _ SubscriptionPreviewer = (*subscription.Service)(nil)

// 預覽與匯入的錯誤碼（SUB-R6、R7）。
const (
	CodeImportTooLarge = subscription.CodeImportTooLarge
	CodeImportTooMany  = subscription.CodeImportTooMany
	CodeImportFormat   = subscription.CodeImportFormat
)

// importFormOverhead 是 multipart 本體在檔案之外的餘裕（邊界、標頭）。
const importFormOverhead = 64 << 10

const importPath = "/api/subscriptions/import"

// importDrainBytes 是回 413 之前最多丟棄的剩餘本體。
const importDrainBytes = 8 << 20

func (h *subscriptionsAPI) registerPreviewImport(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-preview", Method: http.MethodPost, Path: "/api/subscriptions/{id}/preview",
		Summary: "試跑既有訂閱的過濾與回補（body 的設定覆蓋已存的設定），列出會處理與會略過的條目；不建立影音、不寫入條目、不改變排程",
	}, auth.ScopeAdmin, h.previewExisting)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-preview-new", Method: http.MethodPost, Path: "/api/subscriptions/preview",
		Summary: "建立訂閱之前試跑過濾與回補；不建立訂閱與影音",
	}, auth.ScopeAdmin, h.previewNew)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-import", Method: http.MethodPost, Path: importPath,
		Summary: "從 OPML 或 Google Takeout 的 subscriptions.csv 匯入訂閱（multipart 的 file，上限 1 MiB、500 個）；" +
			"一律 backfill: none、預設處理方案與過濾",
		MaxBodyBytes: subscription.MaxImportBytes + importFormOverhead,
	}, auth.ScopeAdmin, h.importFile)
}

func (h *subscriptionsAPI) previewer() (SubscriptionPreviewer, error) {
	p, ok := h.svc.(SubscriptionPreviewer)
	if h.svc == nil || !ok {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "訂閱尚未啟用。")
	}
	return p, nil
}

// mapPreviewErr 對應預覽與匯入的錯誤；其他交給 mapErr。
func (h *subscriptionsAPI) mapPreviewErr(ctx context.Context, err error) error {
	var (
		rl *subscription.RateLimitedError
		pe *subscription.PreviewError
		ie *subscription.ImportError
	)
	switch {
	case errors.As(err, &rl):
		secs := max(1, int(math.Ceil(rl.RetryAfter.Seconds())))
		e := newError(http.StatusTooManyRequests, CodeRateLimited, rl.Error())
		e.RetryAfterS = &secs
		return e
	case errors.As(err, &pe):
		return newError(http.StatusBadGateway, CodeUpstream, pe.Message)
	case errors.As(err, &ie):
		if ie.Code == CodeImportTooLarge {
			return newError(http.StatusRequestEntityTooLarge, ie.Code, ie.Message)
		}
		return newError(http.StatusUnprocessableEntity, ie.Code, ie.Message)
	}
	return h.mapErr(ctx, err)
}

type previewSettings struct {
	Backfill *SubscriptionBackfill `json:"backfill,omitempty" doc:"尚未儲存的回補範圍；省略時用已存的設定"`
	Filters  *SubscriptionFilters  `json:"filters,omitempty" doc:"尚未儲存的過濾條件，蓋在已存的設定上"`
}

func (p *previewSettings) input() subscription.PreviewInput {
	if p == nil {
		return subscription.PreviewInput{}
	}
	return subscription.PreviewInput{Backfill: p.Backfill.input(), Filters: p.Filters.input()}
}

type previewExistingInput struct {
	ID   string           `path:"id" doc:"訂閱的 id"`
	Body *previewSettings `required:"false"`
}

type previewOutput struct{ Body subscription.Preview }

func (h *subscriptionsAPI) previewExisting(ctx context.Context, in *previewExistingInput) (*previewOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	p, err := h.previewer()
	if err != nil {
		return nil, err
	}
	v, err := p.PreviewExisting(ctx, in.ID, in.Body.input())
	if err != nil {
		return nil, h.mapPreviewErr(ctx, err)
	}
	return &previewOutput{Body: v}, nil
}

type previewNewInput struct {
	// 欄位不設必填：缺少網址時由 service 回 422 與 errors（同 POST /api/subscriptions）。
	Body *struct {
		URL      string                `json:"url,omitempty" doc:"頻道、播放清單、Bilibili 合集或 Podcast feed 的網址"`
		Backfill *SubscriptionBackfill `json:"backfill,omitempty"`
		Filters  *SubscriptionFilters  `json:"filters,omitempty"`
	} `required:"false"`
}

func (h *subscriptionsAPI) previewNew(ctx context.Context, in *previewNewInput) (*previewOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	p, err := h.previewer()
	if err != nil {
		return nil, err
	}
	var (
		u  string
		ps subscription.PreviewInput
	)
	if b := in.Body; b != nil {
		u = b.URL
		ps = subscription.PreviewInput{Backfill: b.Backfill.input(), Filters: b.Filters.input()}
	}
	v, err := p.PreviewNew(ctx, u, ps)
	if err != nil {
		return nil, h.mapPreviewErr(ctx, err)
	}
	return &previewOutput{Body: v}, nil
}

type importFileInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File huma.FormFile `form:"file" doc:"OPML 或 Google Takeout 的 subscriptions.csv（上限 1 MiB）"`
	}]
}

type importOutput struct{ Body subscription.ImportResult }

func (h *subscriptionsAPI) importFile(ctx context.Context, in *importFileInput) (*importOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	p, err := h.previewer()
	if err != nil {
		return nil, err
	}
	f := in.RawBody.Data().File
	if !f.IsSet || f.File == nil {
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請上傳匯入檔。")
		e.Errors = []validate.FieldError{{Path: "file", Message: "請上傳匯入檔。"}}
		return nil, e
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f.File, subscription.MaxImportBytes+1))
	if err != nil {
		return nil, newError(http.StatusBadRequest, CodeInvalidInput, defaultMessages[http.StatusBadRequest].msg)
	}
	res, err := p.Import(ctx, data)
	if err != nil {
		return nil, h.mapPreviewErr(ctx, err)
	}
	return &importOutput{Body: res}, nil
}

// subscriptionImportLimit 在進入 Huma 之前限制匯入請求的大小（SUB-R7）：Huma 的 MaxBodyBytes 超過時只會回通用的 413，
// 而規格要求 too_large 與固定的訊息。先檢查 scope（沒有 admin 時不讀本體，回 403），再讀進記憶體。
func subscriptionImportLimit(next http.Handler) http.Handler {
	const limit = subscription.MaxImportBytes + importFormOverhead
	// tooLarge 先丟棄最多 importDrainBytes 的剩餘本體再回應：client 還在送出本體時直接回應並關閉連線，
	// client 會收到 connection reset 而看不到 413。更大的本體不再讀取，直接回應。
	tooLarge := func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, importDrainBytes))
		ie := subscription.ImportTooLarge()
		writeError(w, newError(http.StatusRequestEntityTooLarge, ie.Code, ie.Message))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != importPath || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		if err := requireScope(r.Context(), auth.ScopeAdmin); err != nil {
			writeError(w, asError(err))
			return
		}
		if r.ContentLength > limit {
			tooLarge(w, r)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil {
			writeError(w, newError(http.StatusBadRequest, CodeInvalidInput, defaultMessages[http.StatusBadRequest].msg))
			return
		}
		if len(body) > limit {
			tooLarge(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}
