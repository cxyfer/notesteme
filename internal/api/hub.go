package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/hubclient"
	"github.com/cxyfer/notesteme/internal/hubsettings"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/validate"
)

// Client 的 Hub 路由（HSHR）：設定（R1、R12）、分享（R2～R8）與重新生成（R9～R11）。
// 錯誤依 ADR-0032 第 5 點對應；訊息不含 token。

// HubShareRef 是 GET /api/items/:id 的 hub_shares 的一筆。
type HubShareRef struct {
	ShareID   string    `json:"share_id"`
	HubURL    string    `json:"hub_url"`
	CreatedAt time.Time `json:"created_at"`
}

func (rt *routes) registerHub(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "settings-hub-get", Method: http.MethodGet, Path: "/api/settings/hub",
		Summary: "Hub 的連線設定：url、token_set 與 ASR 前查詢（不含 token，HSHR-R1、R12）",
	}, auth.ScopeAdmin, rt.getHubSettings)
	registerScoped(api, huma.Operation{
		OperationID: "settings-hub-put", Method: http.MethodPut, Path: "/api/settings/hub",
		Summary: "設定 Hub token 與 ASR 前查詢（部分更新；token 為空字串時刪除；不會連線到 Hub）",
		Description: "開啟 ASR 前查詢時 precheck.acknowledge 必須等於目前的 disclosure_version，否則回 422 acknowledgement_required，" +
			"回應帶 disclosure 與 disclosure_version（HSHR-R12）。",
	}, auth.ScopeAdmin, rt.putHubSettings)
	registerScoped(api, huma.Operation{
		OperationID: "settings-hub-test", Method: http.MethodPost, Path: "/api/settings/hub/test",
		Summary: "測試 Hub 連線：以設定的 token 呼叫 Hub 的 GET /api/v1/me（HSHR 待決問題 S9）",
	}, auth.ScopeAdmin, rt.testHub)
	registerScoped(api, huma.Operation{
		OperationID: "items-hub-share", Method: http.MethodPost, Path: "/api/items/{id}/hub/share",
		Summary: "把影音的產出分享到 Hub（HSHR-R2～R8）", DefaultStatus: http.StatusCreated,
		Description: "產生只含白名單欄位的 bundle 送到 Hub 的 POST /api/v1/shares。attest 必須是 true、license 必須是支援的授權；" +
			"本機檔案、非公開影音、需要登入才能取得的字幕不能分享（422 not_shareable）。Hub 的錯誤回 502（hub_*）或原樣的 422。",
	}, auth.ScopeAdmin, rt.shareToHub)
	registerScoped(api, huma.Operation{
		OperationID: "hub-import", Method: http.MethodPost, Path: "/api/hub/import",
		Summary: "以 Hub 上別人的原始字幕重新生成影音（HSHR-R9～R11）", DefaultStatus: http.StatusAccepted,
		Description: "先取得並驗證版本與內容（sha256、cues 格式），通過之後才新增影音或從 transcript 重跑；transcript 寫入這份字幕、" +
			"來源 hub，不呼叫其他字幕來源。只接受 source、normalized 的字幕（422 not_raw）。",
	}, auth.ScopeIngest, rt.importFromHub)
}

type hubSettingsOutput struct{ Body hubsettings.HubSettings }

func (rt *routes) getHubSettings(ctx context.Context, _ *struct{}) (*hubSettingsOutput, error) {
	if rt.d.Hub == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	v, err := rt.d.Hub.Get(ctx)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "讀取 Hub 設定失敗", "error", err)
		return nil, asError(err)
	}
	return &hubSettingsOutput{Body: v}, nil
}

// HubPrecheckUpdate 是 PUT /api/settings/hub 的 precheck（部分更新）。
type HubPrecheckUpdate struct {
	Enabled     *bool  `json:"enabled,omitempty" doc:"開啟或關閉 ASR 前查詢"`
	Acknowledge string `json:"acknowledge,omitempty" doc:"開啟時必須等於 disclosure_version"`
}

// HubShareArtifact 指定要分享的產出（HSHR-R5）。
type HubShareArtifact struct {
	Kind    string `json:"kind" doc:"transcript、chapters、note 或 frames"`
	Variant string `json:"variant,omitempty"`
	Lang    string `json:"lang,omitempty"`
}

type hubSettingsInput struct {
	Body struct {
		Token    *string            `json:"token,omitempty" doc:"Hub token（nsh_…）；空字串刪除，省略時不變"`
		Precheck *HubPrecheckUpdate `json:"precheck,omitempty"`
	}
}

func (rt *routes) putHubSettings(ctx context.Context, in *hubSettingsInput) (*hubSettingsOutput, error) {
	if rt.d.Hub == nil {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個功能尚未實作。")
	}
	u := hubsettings.Update{Token: in.Body.Token}
	if p := in.Body.Precheck; p != nil {
		u.Precheck = &hubsettings.PrecheckUpdate{Enabled: p.Enabled, Acknowledge: p.Acknowledge}
	}
	v, err := rt.d.Hub.Put(ctx, u)
	switch {
	case errors.Is(err, hubsettings.ErrAcknowledgementRequired):
		e := newError(http.StatusUnprocessableEntity, "acknowledgement_required", err.Error())
		e.Disclosure, e.DisclosureVersion = hubsettings.Disclosure(rt.d.Hub.URL), hubsettings.DisclosureVersion
		return nil, e
	case errors.Is(err, secrets.ErrInvalidHubToken):
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error())
		e.Errors = []validate.FieldError{{Path: "token", Message: err.Error()}}
		return nil, e
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "更新 Hub 設定失敗", "error", err)
		return nil, asError(err)
	}
	return &hubSettingsOutput{Body: v}, nil
}

// HubTestView 是測試連線的結果：Hub 上的帳號與 token 的有效權限。
type HubTestView struct {
	DisplayName string   `json:"display_name"`
	Permissions []string `json:"permissions"`
}

type hubTestOutput struct{ Body HubTestView }

func (rt *routes) testHub(ctx context.Context, _ *struct{}) (*hubTestOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	me, err := rt.d.Pipeline.HubMe(ctx)
	if err != nil {
		return nil, rt.hubErr(ctx, err, http.StatusNotFound)
	}
	return &hubTestOutput{Body: HubTestView{DisplayName: me.DisplayName, Permissions: me.Permissions}}, nil
}

type hubShareInput struct {
	ID   string `path:"id" doc:"影音 ID 或代號"`
	Body struct {
		License       string             `json:"license,omitempty" doc:"授權（SPDX）：CC-BY-4.0、CC-BY-SA-4.0、CC-BY-NC-4.0、CC-BY-NC-SA-4.0 或 CC0-1.0"`
		Attest        bool               `json:"attest,omitempty" doc:"必須是 true：確認有權分享這些內容"`
		Artifacts     []HubShareArtifact `json:"artifacts,omitempty" doc:"只分享列出的產出；省略時分享每個字幕變體與語言的現行版本、chapters 與 note"`
		IncludeFrames bool               `json:"include_frames,omitempty" doc:"也分享選中的畫面（Hub 端一律 private）"`
		Visibility    string             `json:"visibility,omitempty" doc:"public、authenticated、private 或 group:<群組>；省略時依 Hub 的預設"`
	}
}

// HubShareView 是分享的結果。
type HubShareView struct {
	ShareID  string                      `json:"share_id"`
	Versions []pipeline.HubSharedVersion `json:"versions"`
}

type hubShareOutput struct{ Body HubShareView }

func (rt *routes) shareToHub(ctx context.Context, in *hubShareInput) (*hubShareOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	req := pipeline.HubShareRequest{License: in.Body.License, Attest: in.Body.Attest, IncludeFrames: in.Body.IncludeFrames, Visibility: in.Body.Visibility}
	for _, a := range in.Body.Artifacts {
		req.Artifacts = append(req.Artifacts, pipeline.HubArtifactRef{Kind: a.Kind, Variant: a.Variant, Lang: a.Lang})
	}
	// 請求本身的檢查在找影音之前（HSHR-R3）。
	if err := pipeline.CheckShareRequest(req); err != nil {
		return nil, rt.hubErr(ctx, err, http.StatusNotFound)
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	res, err := rt.d.Pipeline.ShareToHub(ctx, id, req)
	if err != nil {
		return nil, rt.hubErr(ctx, err, http.StatusNotFound)
	}
	return &hubShareOutput{Body: HubShareView{ShareID: res.ShareID, Versions: res.Versions}}, nil
}

type hubImportInput struct {
	Body struct {
		Version   string         `json:"version" minLength:"1" maxLength:"64" doc:"Hub 上的版本 ID"`
		Profile   string         `json:"profile,omitempty" doc:"處理方案；省略時用預設"`
		Overrides map[string]any `json:"overrides,omitempty" doc:"單次覆寫（ING-R7）"`
	}
}

// HubImportItem 是重新生成的影音。
type HubImportItem struct {
	ID  string `json:"id"`
	Ref string `json:"ref"`
}

type hubImportOutput struct {
	Body HubImportView
}

// HubImportView 是 POST /api/hub/import 的回應。
type HubImportView struct {
	Item HubImportItem `json:"item"`
}

func (rt *routes) importFromHub(ctx context.Context, in *hubImportInput) (*hubImportOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	if !hubclient.ValidVersionID(in.Body.Version) {
		return nil, rt.hubErr(ctx, &hubclient.Error{Kind: hubclient.KindNotFound, Status: http.StatusNotFound}, http.StatusConflict)
	}
	res, err := rt.d.Pipeline.ImportFromHub(ctx, pipeline.HubImportRequest{Version: in.Body.Version, Profile: in.Body.Profile, Overrides: in.Body.Overrides})
	if err != nil {
		// 垃圾桶中的影音：新增它回 409（DEL-R10）。
		return nil, rt.hubErr(ctx, err, http.StatusConflict)
	}
	out := &hubImportOutput{}
	out.Body.Item = HubImportItem{ID: res.ItemID, Ref: res.Ref}
	return out, nil
}

// hubErr 把分享與重新生成的錯誤對應成回應；trashStatus 是影音在垃圾桶中時的狀態碼（分享 404、重新生成 409）。
func (rt *routes) hubErr(ctx context.Context, err error, trashStatus int) error {
	var (
		he *hubclient.Error
		pe *pipeline.HubError
		tr *pipeline.InTrashError
	)
	switch {
	case errors.Is(err, hubclient.ErrNotConfigured):
		e := newError(http.StatusConflict, hubclient.CodeNotConfigured, hubclient.MsgNotConfigured)
		e.Actions = []string{hubclient.ActionConfigure}
		return e
	case errors.As(err, &he):
		rt.d.Log.LogAttrs(ctx, slog.LevelWarn, "Hub 請求失敗", slog.String("code", he.Code()), slog.Int("status", he.Status))
		e := newError(he.HTTPStatus(), he.Code(), he.Error())
		if he.Kind == hubclient.KindRateLimited {
			n := he.RetryAfterS
			e.RetryAfterS = &n
		}
		for _, fe := range he.Errors {
			e.Errors = append(e.Errors, validate.FieldError{Path: fe.Path, Message: fe.Message})
		}
		return e
	case errors.As(err, &pe):
		e := newError(pe.Status, pe.Code, pe.Msg)
		e.Actions = pe.Actions
		if pe.Path != "" {
			e.Errors = []validate.FieldError{{Path: pe.Path, Message: pe.Msg}}
		}
		return e
	case errors.As(err, &tr):
		return inTrashError(trashStatus, tr.ItemID)
	}
	return rt.mapErr(ctx, err)
}

// hubShares 回傳影音的分享紀錄；沒有時回傳 nil（回應中省略）。
func (rt *routes) hubShares(ctx context.Context, itemID string) ([]HubShareRef, error) {
	rows, err := rt.d.Pipeline.ItemHubShares(ctx, itemID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	out := make([]HubShareRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, HubShareRef{ShareID: r.ShareID, HubURL: r.HubURL, CreatedAt: r.CreatedAt})
	}
	return out, nil
}
