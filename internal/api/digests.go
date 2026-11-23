package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/digest"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 摘要與彙整的錯誤代碼（DIG-R7、R8）。
const (
	CodeNotAwaitingUser = "not_awaiting_user"
	CodeDigestExists    = "digest_exists"
)

// maxContinueIDs 是批次繼續的上限（DIG-R7）。
const maxContinueIDs = 100

// registerDigests 註冊 DIG 的路由：繼續處理（scope ingest，DIG-R7）與彙整設定（scope admin，DIG-R8、R11）。
func (rt *routes) registerDigests(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-continue", Method: http.MethodPost, Path: "/api/items/{id}/continue",
		Summary:       "繼續處理摘要後暫停的影音：awaiting_user 的步驟改回 pending 並建立任務（DIG-R7）",
		DefaultStatus: http.StatusAccepted,
	}, auth.ScopeIngest, rt.continueItem)
	registerScoped(api, huma.Operation{
		OperationID: "items-continue-batch", Method: http.MethodPost, Path: "/api/items/continue",
		Summary: "一次繼續處理多部摘要後暫停的影音（1～100 部，個別失敗不影響其他影音，DIG-R7）",
	}, auth.ScopeIngest, rt.continueItems)
	registerScoped(api, huma.Operation{
		OperationID: "digests-list", Method: http.MethodGet, Path: "/api/digests",
		Summary: "列出彙整設定（DIG-R8）",
	}, auth.ScopeAdmin, rt.listDigests)
	registerScoped(api, huma.Operation{
		OperationID: "digests-create", Method: http.MethodPost, Path: "/api/digests",
		Summary: "建立彙整設定並排定第一次執行（DIG-R8）", DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createDigest)
	registerScoped(api, huma.Operation{
		OperationID: "digests-patch", Method: http.MethodPatch, Path: "/api/digests/{id}",
		Summary: "修改彙整設定：只更新給定的欄位（DIG-R8）",
	}, auth.ScopeAdmin, rt.patchDigest)
	registerScoped(api, huma.Operation{
		OperationID: "digests-delete", Method: http.MethodDelete, Path: "/api/digests/{id}",
		Summary: "刪除彙整設定", DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, rt.deleteDigest)
	registerScoped(api, huma.Operation{
		OperationID: "digests-preview", Method: http.MethodPost, Path: "/api/digests/{id}/preview",
		Summary: "預覽彙整的內容：與現在執行相同，但不送出、不寫入紀錄、不改變下次執行時間（DIG-R11）",
	}, auth.ScopeAdmin, rt.previewDigest)
}

type continueOutput struct {
	Body struct {
		Continued string `json:"continued" doc:"改回 pending 的步驟"`
	}
}

type continueBatchInput struct {
	Body struct {
		IDs []string `json:"ids" doc:"影音的 ID 或代號（1～100 個）"`
	}
}

// ContinueSkipped 是批次繼續中沒有繼續的一部影音。
type ContinueSkipped struct {
	ID   string `json:"id" doc:"請求中 ids 的原始寫法"`
	Code string `json:"code" doc:"not_found、in_trash 或 not_awaiting_user"`
}

type continueBatchOutput struct {
	Body struct {
		Continued []string          `json:"continued" doc:"已繼續的影音（請求中 ids 的原始寫法）"`
		Skipped   []ContinueSkipped `json:"skipped"`
	}
}

func (rt *routes) continueItem(ctx context.Context, in *itemInput) (*continueOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	stage, err := rt.d.Pipeline.Continue(ctx, id)
	if errors.Is(err, pipeline.ErrNotAwaitingUser) {
		return nil, newError(http.StatusConflict, CodeNotAwaitingUser, err.Error())
	}
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &continueOutput{}
	out.Body.Continued = stage
	return out, nil
}

// continueItems 逐一繼續；每部影音各自一個交易，個別失敗只記在 skipped（DIG-R7）。
func (rt *routes) continueItems(ctx context.Context, in *continueBatchInput) (*continueBatchOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	if n := len(in.Body.IDs); n < 1 || n > maxContinueIDs {
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請求的內容不正確。")
		e.Errors = []validate.FieldError{{Path: "ids", Message: "ids 必須有 1～100 部影音。"}}
		return nil, e
	}
	out := &continueBatchOutput{}
	out.Body.Continued, out.Body.Skipped = []string{}, []ContinueSkipped{}
	for _, raw := range in.Body.IDs {
		code, err := rt.continueOne(ctx, raw)
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		if code == "" {
			out.Body.Continued = append(out.Body.Continued, raw)
		} else {
			out.Body.Skipped = append(out.Body.Skipped, ContinueSkipped{ID: raw, Code: code})
		}
	}
	return out, nil
}

// continueOne 繼續一部影音；回傳 skipped 的代碼（成功時是空字串）。只有非預期的錯誤才回傳 error。
func (rt *routes) continueOne(ctx context.Context, raw string) (string, error) {
	id := raw
	if _, _, _, ok := source.SplitRef(raw); ok {
		it, err := rt.d.Pipeline.ResolveItem(ctx, raw)
		if err != nil {
			return continueCode(err)
		}
		id = it.ID
	}
	_, err := rt.d.Pipeline.Continue(ctx, id)
	return continueCode(err)
}

func continueCode(err error) (string, error) {
	switch {
	case err == nil:
		return "", nil
	case errors.Is(err, store.ErrNotFound):
		return CodeNotFound, nil
	case errors.Is(err, pipeline.ErrInTrash):
		return CodeInTrash, nil
	case errors.Is(err, pipeline.ErrNotAwaitingUser):
		return CodeNotAwaitingUser, nil
	}
	return "", err
}

// ---- 彙整設定 ----

type digestListOutput struct {
	Body struct {
		Digests []digest.DigestInfo `json:"digests"`
	}
}

type digestOutput struct{ Body digest.DigestInfo }

type digestIDInput struct {
	ID string `path:"id" doc:"彙整的 id"`
}

// digestBody 是 POST 與 PATCH 的欄位；沒有給的欄位建立時用預設值、修改時不變。
type digestBody struct {
	Period    *string   `json:"period,omitempty" doc:"daily 或 weekly"`
	At        *string   `json:"at,omitempty" doc:"HH:MM（預設 08:00）"`
	Weekday   *string   `json:"weekday,omitempty" doc:"mon～sun，只在 weekly 時需要"`
	Timezone  *string   `json:"timezone,omitempty" doc:"IANA 時區名稱（預設 UTC）"`
	Notifiers *[]string `json:"notifiers,omitempty" doc:"1～5 個勾選了 digest 事件的 notifier id"`
	Scope     *string   `json:"scope,omitempty" doc:"subscriptions（預設，只有訂閱建立的影音）或 all"`
	Enabled   *bool     `json:"enabled,omitempty" doc:"預設 true"`
}

func (b digestBody) input(id string) digest.Input {
	return digest.Input{ID: id, Period: b.Period, At: b.At, Weekday: b.Weekday, Timezone: b.Timezone, Notifiers: b.Notifiers, Scope: b.Scope, Enabled: b.Enabled}
}

type createDigestInput struct {
	Body struct {
		ID        string    `json:"id" doc:"彙整的 id（小寫英文字母、數字與 -，最多 40 字元）"`
		Period    *string   `json:"period,omitempty" doc:"daily 或 weekly"`
		At        *string   `json:"at,omitempty" doc:"HH:MM（預設 08:00）"`
		Weekday   *string   `json:"weekday,omitempty" doc:"mon～sun，只在 weekly 時需要"`
		Timezone  *string   `json:"timezone,omitempty" doc:"IANA 時區名稱（預設 UTC）"`
		Notifiers *[]string `json:"notifiers,omitempty" doc:"1～5 個勾選了 digest 事件的 notifier id"`
		Scope     *string   `json:"scope,omitempty" doc:"subscriptions（預設，只有訂閱建立的影音）或 all"`
		Enabled   *bool     `json:"enabled,omitempty" doc:"預設 true"`
	}
}

type patchDigestInput struct {
	ID   string `path:"id" doc:"彙整的 id"`
	Body digestBody
}

type previewDigestOutput struct{ Body digest.DigestPreview }

func (rt *routes) digestService(ctx context.Context) (*digest.Service, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Digests == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, defaultMessages[http.StatusNotImplemented].msg)
	}
	return rt.d.Digests, nil
}

func (rt *routes) digestErr(ctx context.Context, err error) error {
	var inv *digest.InvalidError
	switch {
	case errors.As(err, &inv):
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
		e.Errors = append([]validate.FieldError{}, inv.Fields...)
		return e
	case errors.Is(err, digest.ErrNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, digest.ErrExists):
		return newError(http.StatusConflict, CodeDigestExists, err.Error())
	}
	return rt.mapErr(ctx, err)
}

func (rt *routes) listDigests(ctx context.Context, _ *struct{}) (*digestListOutput, error) {
	svc, err := rt.digestService(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, rt.digestErr(ctx, err)
	}
	out := &digestListOutput{}
	out.Body.Digests = list
	return out, nil
}

func (rt *routes) createDigest(ctx context.Context, in *createDigestInput) (*digestOutput, error) {
	svc, err := rt.digestService(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	info, err := svc.Create(ctx, digestBody{Period: b.Period, At: b.At, Weekday: b.Weekday, Timezone: b.Timezone, Notifiers: b.Notifiers, Scope: b.Scope, Enabled: b.Enabled}.input(b.ID))
	if err != nil {
		return nil, rt.digestErr(ctx, err)
	}
	return &digestOutput{Body: info}, nil
}

func (rt *routes) patchDigest(ctx context.Context, in *patchDigestInput) (*digestOutput, error) {
	svc, err := rt.digestService(ctx)
	if err != nil {
		return nil, err
	}
	info, err := svc.Patch(ctx, in.ID, in.Body.input(in.ID))
	if err != nil {
		return nil, rt.digestErr(ctx, err)
	}
	return &digestOutput{Body: info}, nil
}

func (rt *routes) deleteDigest(ctx context.Context, in *digestIDInput) (*emptyOutput, error) {
	svc, err := rt.digestService(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.Delete(ctx, in.ID); err != nil {
		return nil, rt.digestErr(ctx, err)
	}
	return &emptyOutput{}, nil
}

func (rt *routes) previewDigest(ctx context.Context, in *digestIDInput) (*previewDigestOutput, error) {
	svc, err := rt.digestService(ctx)
	if err != nil {
		return nil, err
	}
	p, err := svc.Preview(ctx, in.ID)
	if err != nil {
		return nil, rt.digestErr(ctx, err)
	}
	return &previewDigestOutput{Body: p}, nil
}
