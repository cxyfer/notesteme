package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 通知的錯誤代碼（NOTIFY-R1）。
const (
	CodeNotifierExists   = "notifier_exists"
	CodeTooManyNotifiers = "too_many_notifiers"
)

// registerNotifiers 註冊 NOTIFY 的路由；全部需要 scope admin（NOTIFY-R1、R8、R12）。
func (rt *routes) registerNotifiers(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "notifiers-list", Method: http.MethodGet, Path: "/api/notifiers",
		Summary: "列出通知端點（密鑰只顯示末四碼，NOTIFY-R6）",
	}, auth.ScopeAdmin, rt.listNotifiers)
	registerScoped(api, huma.Operation{
		OperationID: "notifiers-create", Method: http.MethodPost, Path: "/api/notifiers",
		Summary:       "建立通知端點（ntfy、Discord、Telegram 或 webhook，NOTIFY-R1～R3）",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createNotifier)
	registerScoped(api, huma.Operation{
		OperationID: "notifiers-get", Method: http.MethodGet, Path: "/api/notifiers/{id}",
		Summary: "取得一個通知端點",
	}, auth.ScopeAdmin, rt.getNotifier)
	registerScoped(api, huma.Operation{
		OperationID: "notifiers-patch", Method: http.MethodPatch, Path: "/api/notifiers/{id}",
		Summary: "修改通知端點：只更新給定的欄位，沒給的密鑰保留原值（NOTIFY-R6）",
	}, auth.ScopeAdmin, rt.patchNotifier)
	registerScoped(api, huma.Operation{
		OperationID: "notifiers-delete", Method: http.MethodDelete, Path: "/api/notifiers/{id}",
		Summary:       "刪除通知端點與它還沒送出的通知",
		DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, rt.deleteNotifier)
	registerScoped(api, huma.Operation{
		OperationID: "notifiers-test", Method: http.MethodPost, Path: "/api/notifiers/{id}/test",
		Summary: "立即傳送一則測試通知（不經 outbox、不重試，NOTIFY-R12）",
	}, auth.ScopeAdmin, rt.testNotifier)
	registerScoped(api, huma.Operation{
		OperationID: "notifications-list", Method: http.MethodGet, Path: "/api/notifications",
		Summary: "列出最近 100 筆通知 outbox 的項目（state=pending、sent 或 dropped，NOTIFY-R8）",
	}, auth.ScopeAdmin, rt.listNotifications)
}

type notifierListOutput struct {
	Body struct {
		Notifiers []notify.Info `json:"notifiers"`
	}
}

type notifierOutput struct{ Body notify.Info }

type notifierIDInput struct {
	ID string `path:"id" doc:"notifier 的 id"`
}

type createNotifierInput struct {
	Body struct {
		ID      string         `json:"id" doc:"notifier 的 id（小寫英文字母、數字與 -，最多 40 字元）"`
		Kind    string         `json:"kind" doc:"ntfy、discord、telegram 或 webhook"`
		Config  map[string]any `json:"config" doc:"各種類的設定（NOTIFY-R2）"`
		Events  []string       `json:"events" doc:"要通知的事件（1～7 個，NOTIFY-R3）"`
		Enabled *bool          `json:"enabled,omitempty" doc:"預設 true"`
	}
}

type patchNotifierInput struct {
	ID   string `path:"id" doc:"notifier 的 id"`
	Body struct {
		Config  map[string]any `json:"config,omitempty" doc:"只覆寫給定的欄位；null 刪除選填欄位"`
		Events  []string       `json:"events,omitempty"`
		Enabled *bool          `json:"enabled,omitempty"`
	}
}

type notifierTestOutput struct{ Body notify.TestResult }

type notificationsInput struct {
	State string `query:"state" enum:"pending,sent,dropped" doc:"只列出這個狀態"`
}

type notificationsOutput struct {
	Body struct {
		Notifications []notify.Notification `json:"notifications"`
	}
}

func (rt *routes) notifyService(ctx context.Context) (*notify.Service, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Notify == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, defaultMessages[http.StatusNotImplemented].msg)
	}
	return rt.d.Notify, nil
}

// notifyErr 把 notify 的錯誤轉成 API 錯誤；其他錯誤只記錄，不洩漏細節（也不含 notifier 的網址）。
func (rt *routes) notifyErr(ctx context.Context, err error) error {
	var inv *notify.InvalidError
	switch {
	case errors.As(err, &inv):
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
		e.Errors = append([]validate.FieldError{}, inv.Fields...)
		return e
	case errors.Is(err, notify.ErrNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, notify.ErrExists):
		return newError(http.StatusConflict, CodeNotifierExists, err.Error())
	case errors.Is(err, notify.ErrTooMany):
		return newError(http.StatusConflict, CodeTooManyNotifiers, err.Error())
	}
	rt.d.Log.ErrorContext(ctx, "處理 notifier 失敗", "error", err)
	return asError(err)
}

func (rt *routes) listNotifiers(ctx context.Context, _ *struct{}) (*notifierListOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	out := &notifierListOutput{}
	out.Body.Notifiers = list
	return out, nil
}

func (rt *routes) createNotifier(ctx context.Context, in *createNotifierInput) (*notifierOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	info, err := svc.Create(ctx, notify.CreateInput{ID: in.Body.ID, Kind: in.Body.Kind, Config: in.Body.Config, Events: in.Body.Events, Enabled: in.Body.Enabled})
	if err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	return &notifierOutput{Body: info}, nil
}

func (rt *routes) getNotifier(ctx context.Context, in *notifierIDInput) (*notifierOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	info, err := svc.Get(ctx, in.ID)
	if err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	return &notifierOutput{Body: info}, nil
}

func (rt *routes) patchNotifier(ctx context.Context, in *patchNotifierInput) (*notifierOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	info, err := svc.Patch(ctx, in.ID, notify.PatchInput{Config: in.Body.Config, Events: in.Body.Events, Enabled: in.Body.Enabled})
	if err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	return &notifierOutput{Body: info}, nil
}

func (rt *routes) deleteNotifier(ctx context.Context, in *notifierIDInput) (*emptyOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.Delete(ctx, in.ID); err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	return &emptyOutput{}, nil
}

func (rt *routes) testNotifier(ctx context.Context, in *notifierIDInput) (*notifierTestOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	res, err := svc.Test(ctx, in.ID)
	if err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	return &notifierTestOutput{Body: res}, nil
}

func (rt *routes) listNotifications(ctx context.Context, in *notificationsInput) (*notificationsOutput, error) {
	svc, err := rt.notifyService(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.Notifications(ctx, in.State)
	if err != nil {
		return nil, rt.notifyErr(ctx, err)
	}
	out := &notificationsOutput{}
	out.Body.Notifications = list
	return out, nil
}
