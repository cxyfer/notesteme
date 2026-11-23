package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subscription"
)

// 這個檔案是訂閱的 REST 介面（SUB-R13，M4-S2）：讀取用 read；建立、修改、刪除與立即檢查用 admin
// （owner 的 session 有全部 scope）。試跑預覽與匯入（SUB-R6、R7）仍是 m4_stubs.go 的 501，由 M4-S3 實作。

// SubscriptionService 是訂閱 handler 需要的 service；*subscription.Service 實作它。
type SubscriptionService interface {
	Create(ctx context.Context, in subscription.CreateInput) (subscription.View, error)
	List(ctx context.Context) ([]subscription.View, error)
	Get(ctx context.Context, id string) (subscription.View, error)
	Patch(ctx context.Context, id string, in subscription.PatchInput) (subscription.View, error)
	Delete(ctx context.Context, id string) error
	Entries(ctx context.Context, id, decision string, limit int) ([]subscription.EntryView, error)
	CheckNow(ctx context.Context, id string) (time.Time, error)
}

var _ SubscriptionService = (*subscription.Service)(nil)

// 訂閱的錯誤碼（SUB-R1）。
const (
	CodeUnsupportedSubscriptionURL = subscription.CodeUnsupportedURL
	CodeNotACollection             = subscription.CodeNotACollection
	CodeDuplicateSubscription      = subscription.CodeDuplicate
)

type subscriptionsAPI struct {
	rt  *routes
	svc SubscriptionService
}

func (rt *routes) registerSubscriptions(api huma.API) {
	h := &subscriptionsAPI{rt: rt, svc: rt.d.Subscriptions}
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-list", Method: http.MethodGet, Path: "/api/subscriptions",
		Summary: "列出訂閱（依建立順序）",
	}, auth.ScopeRead, h.list)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-create", Method: http.MethodPost, Path: "/api/subscriptions",
		Summary:       "建立訂閱（YouTube 頻道或播放清單、Bilibili 合集、Podcast feed）；只依網址的格式辨識，不連外",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, h.create)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-get", Method: http.MethodGet, Path: "/api/subscriptions/{id}",
		Summary: "取得一個訂閱，含上次與下次檢查的時間、最近一次的錯誤",
	}, auth.ScopeRead, h.get)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-patch", Method: http.MethodPatch, Path: "/api/subscriptions/{id}",
		Summary: "修改訂閱的處理方案、檢查頻率、回補範圍、過濾條件或啟用狀態",
	}, auth.ScopeAdmin, h.patch)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-delete", Method: http.MethodDelete, Path: "/api/subscriptions/{id}",
		Summary: "刪除訂閱與它的條目；已建立的影音保留", DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, h.delete)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-entries", Method: http.MethodGet, Path: "/api/subscriptions/{id}/entries",
		Summary: "列出訂閱探索到的條目與略過原因（新的在前）",
	}, auth.ScopeRead, h.entries)
	registerScoped(api, huma.Operation{
		OperationID: "subscriptions-check", Method: http.MethodPost, Path: "/api/subscriptions/{id}/check",
		Summary: "把下一次檢查改成現在（下一輪排程器執行，不在請求中探索）", DefaultStatus: http.StatusAccepted,
	}, auth.ScopeAdmin, h.check)
}

func (h *subscriptionsAPI) service() (SubscriptionService, error) {
	if h.svc == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "訂閱尚未啟用。")
	}
	return h.svc, nil
}

// mapErr 對應訂閱的錯誤；其他交給 routes.mapErr（處理方案、覆寫的 422）。
func (h *subscriptionsAPI) mapErr(ctx context.Context, err error) error {
	var (
		ue  *subscription.URLError
		fe  *subscription.FieldsError
		dup *subscription.DuplicateError
	)
	switch {
	case errors.As(err, &ue):
		return newError(http.StatusUnprocessableEntity, ue.Code, ue.Message)
	case errors.As(err, &fe):
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, fe.Error())
		e.Errors = fe.Fields
		return e
	case errors.As(err, &dup):
		e := newError(http.StatusConflict, CodeDuplicateSubscription, dup.Error())
		e.SubscriptionID = dup.SubscriptionID
		return e
	case errors.Is(err, store.ErrNotFound):
		return newError(http.StatusNotFound, CodeNotFound, "找不到這個訂閱。")
	}
	return h.rt.mapErr(ctx, err)
}

// SubscriptionBackfill 是請求中的回補範圍。
type SubscriptionBackfill struct {
	Mode  string `json:"mode" doc:"none（只抓之後的新內容）、recent（最近幾部）或 all（全部，有上限）"`
	Count *int   `json:"count,omitempty" doc:"recent 的部數，1～200"`
}

// SubscriptionFilters 是請求中的過濾條件；省略的欄位用預設值（排除 Shorts、直播等到有錄影）。
type SubscriptionFilters struct {
	MinDurationS  *int    `json:"min_duration_s,omitempty" doc:"最短片長（秒）"`
	MaxDurationS  *int    `json:"max_duration_s,omitempty" doc:"最長片長（秒），必須大於最短片長"`
	ExcludeShorts *bool   `json:"exclude_shorts,omitempty" doc:"排除 Shorts（預設 true）"`
	Live          *string `json:"live,omitempty" doc:"wait（預設，等到有錄影）或 skip"`
	TitleInclude  *string `json:"title_include,omitempty" doc:"標題必須符合的 RE2 正規表示式，最長 200 個字元"`
	TitleExclude  *string `json:"title_exclude,omitempty" doc:"標題符合就略過的 RE2 正規表示式，最長 200 個字元"`
}

func (b *SubscriptionBackfill) input() *subscription.BackfillInput {
	if b == nil {
		return nil
	}
	return &subscription.BackfillInput{Mode: b.Mode, Count: b.Count}
}

func (f *SubscriptionFilters) input() *subscription.FiltersInput {
	if f == nil {
		return nil
	}
	return &subscription.FiltersInput{
		MinDurationS: f.MinDurationS, MaxDurationS: f.MaxDurationS, ExcludeShorts: f.ExcludeShorts,
		Live: f.Live, TitleInclude: f.TitleInclude, TitleExclude: f.TitleExclude,
	}
}

type subscriptionOutput struct{ Body subscription.View }

type subscriptionsListOutput struct {
	Body struct {
		Subscriptions []subscription.View `json:"subscriptions"`
	}
}

func (h *subscriptionsAPI) list(ctx context.Context, _ *struct{}) (*subscriptionsListOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	vs, err := svc.List(ctx)
	if err != nil {
		return nil, h.mapErr(ctx, err)
	}
	out := &subscriptionsListOutput{}
	out.Body.Subscriptions = vs
	return out, nil
}

type createSubscriptionInput struct {
	// 欄位都不設必填與格式：缺少或不合法時由 service 回 422 與 errors（SUB-R1、R3、R4、R9）。
	Body *struct {
		URL       string                `json:"url,omitempty" doc:"頻道、播放清單、Bilibili 合集或 Podcast feed 的網址"`
		Profile   string                `json:"profile,omitempty" doc:"處理方案；省略時用預設處理方案"`
		Overrides map[string]any        `json:"overrides,omitempty" doc:"建立影音時的單次覆寫（同 POST /api/items）"`
		Schedule  *string               `json:"schedule,omitempty" doc:"檢查頻率（Go duration，1h～168h，預設 6h）"`
		Backfill  *SubscriptionBackfill `json:"backfill,omitempty"`
		Filters   *SubscriptionFilters  `json:"filters,omitempty"`
		Enabled   *bool                 `json:"enabled,omitempty" doc:"預設 true"`
	} `required:"false"`
}

func (h *subscriptionsAPI) create(ctx context.Context, in *createSubscriptionInput) (*subscriptionOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	var req subscription.CreateInput
	if b := in.Body; b != nil {
		req = subscription.CreateInput{
			URL: b.URL, Profile: b.Profile, Overrides: b.Overrides, Schedule: b.Schedule,
			Backfill: b.Backfill.input(), Filters: b.Filters.input(), Enabled: b.Enabled,
		}
	}
	v, err := svc.Create(ctx, req)
	if err != nil {
		return nil, h.mapErr(ctx, err)
	}
	return &subscriptionOutput{Body: v}, nil
}

type subscriptionIDInput struct {
	ID string `path:"id" doc:"訂閱的 id"`
}

func (h *subscriptionsAPI) get(ctx context.Context, in *subscriptionIDInput) (*subscriptionOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	v, err := svc.Get(ctx, in.ID)
	if err != nil {
		return nil, h.mapErr(ctx, err)
	}
	return &subscriptionOutput{Body: v}, nil
}

type patchSubscriptionInput struct {
	ID   string `path:"id" doc:"訂閱的 id"`
	Body *struct {
		Profile   *string               `json:"profile,omitempty" doc:"處理方案；空字串改回預設處理方案"`
		Overrides map[string]any        `json:"overrides,omitempty" doc:"整個取代單次覆寫"`
		Schedule  *string               `json:"schedule,omitempty"`
		Backfill  *SubscriptionBackfill `json:"backfill,omitempty" doc:"修改時，下一次檢查重新評估因回補範圍略過的條目（只評估一次）"`
		Filters   *SubscriptionFilters  `json:"filters,omitempty" doc:"只影響之後的條目，不重新評估已略過的"`
		Enabled   *bool                 `json:"enabled,omitempty"`
	} `required:"false"`
}

func (h *subscriptionsAPI) patch(ctx context.Context, in *patchSubscriptionInput) (*subscriptionOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	var req subscription.PatchInput
	if b := in.Body; b != nil {
		req = subscription.PatchInput{
			Profile: b.Profile, Overrides: b.Overrides, Schedule: b.Schedule,
			Backfill: b.Backfill.input(), Filters: b.Filters.input(), Enabled: b.Enabled,
		}
	}
	v, err := svc.Patch(ctx, in.ID, req)
	if err != nil {
		return nil, h.mapErr(ctx, err)
	}
	return &subscriptionOutput{Body: v}, nil
}

func (h *subscriptionsAPI) delete(ctx context.Context, in *subscriptionIDInput) (*struct{}, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	if err := svc.Delete(ctx, in.ID); err != nil {
		return nil, h.mapErr(ctx, err)
	}
	return nil, nil
}

type subscriptionEntriesInput struct {
	ID       string `path:"id" doc:"訂閱的 id"`
	Decision string `query:"decision" enum:"created,skipped,deferred," doc:"只列這一種決定"`
	Limit    int    `query:"limit" minimum:"0" maximum:"5000" doc:"最多幾筆；0 代表上限 5000"`
}

type subscriptionEntriesOutput struct {
	Body struct {
		Entries []subscription.EntryView `json:"entries"`
	}
}

func (h *subscriptionsAPI) entries(ctx context.Context, in *subscriptionEntriesInput) (*subscriptionEntriesOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	es, err := svc.Entries(ctx, in.ID, in.Decision, in.Limit)
	if err != nil {
		return nil, h.mapErr(ctx, err)
	}
	out := &subscriptionEntriesOutput{}
	out.Body.Entries = es
	return out, nil
}

type subscriptionCheckOutput struct {
	Body struct {
		NextCheckAt time.Time `json:"next_check_at"`
	}
}

func (h *subscriptionsAPI) check(ctx context.Context, in *subscriptionIDInput) (*subscriptionCheckOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service()
	if err != nil {
		return nil, err
	}
	at, err := svc.CheckNow(ctx, in.ID)
	if err != nil {
		return nil, h.mapErr(ctx, err)
	}
	out := &subscriptionCheckOutput{}
	out.Body.NextCheckAt = at
	return out, nil
}
