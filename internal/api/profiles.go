package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是處理方案的 API（PRF-R3、R7，M2 S1/S2 設計第 6.2 節）：
// 讀取用 read，寫入用 admin；scope 在讀取請求本體之前檢查。

// profileService 是處理方案 handler 需要的 service。*pipeline.Pipeline 實作它；
// 定義成介面是為了讓測試用假的實作驗證 handler 的對應（狀態碼、錯誤格式）。
type profileService interface {
	Profiles(ctx context.Context) ([]pipeline.Profile, error)
	Profile(ctx context.Context, name string) (pipeline.Profile, error)
	CreateProfile(ctx context.Context, name string, cfg map[string]any) (pipeline.Profile, error)
	PatchProfile(ctx context.Context, name string, patch map[string]any) (pipeline.Profile, error)
	DeleteProfile(ctx context.Context, name string) error
	ProfileVersions(ctx context.Context, name string) ([]pipeline.ProfileVersion, error)
	SetDefaultProfile(ctx context.Context, name string) (pipeline.Profile, error)
}

var _ profileService = (*pipeline.Pipeline)(nil)

// profilesAPI 把路由接到 service；svc 是 nil 代表沒有注入 Pipeline（設定錯誤，回 500）。
type profilesAPI struct {
	rt  *routes
	svc profileService
}

func (rt *routes) registerProfiles(api huma.API) {
	h := &profilesAPI{rt: rt}
	if rt.d.Pipeline != nil {
		h.svc = rt.d.Pipeline
	}
	h.register(api)
}

func (h *profilesAPI) register(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "profiles-list", Method: http.MethodGet, Path: "/api/profiles",
		Summary: "列出處理方案，依名稱排序（PRF-R1）",
	}, auth.ScopeRead, h.list)
	registerScoped(api, huma.Operation{
		OperationID: "profiles-create", Method: http.MethodPost, Path: "/api/profiles",
		Summary: "建立處理方案（版本 1）；內容不正確回 422 與逐欄位的 errors（PRF-R4）", DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, h.create)
	registerScoped(api, huma.Operation{
		OperationID: "profiles-get", Method: http.MethodGet, Path: "/api/profiles/{id}",
		Summary: "讀取處理方案的目前版本",
	}, auth.ScopeRead, h.get)
	registerScoped(api, huma.Operation{
		OperationID: "profiles-patch", Method: http.MethodPatch, Path: "/api/profiles/{id}",
		Summary: "以 merge patch（RFC 7396）修改處理方案，建立新版本（PRF-R3）",
	}, auth.ScopeAdmin, h.patch)
	registerScoped(api, huma.Operation{
		OperationID: "profiles-delete", Method: http.MethodDelete, Path: "/api/profiles/{id}",
		Summary: "刪除處理方案與它的所有版本；內建的 default 回 409 builtin_profile，預設處理方案回 409 profile_in_use（PRF-R7）", DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, h.delete)
	registerScoped(api, huma.Operation{
		OperationID: "profiles-versions", Method: http.MethodGet, Path: "/api/profiles/{id}/versions",
		Summary: "處理方案的所有版本，依版本遞增",
	}, auth.ScopeRead, h.versions)
	registerScoped(api, huma.Operation{
		OperationID: "profiles-default", Method: http.MethodPost, Path: "/api/profiles/{id}/default",
		Summary: "設為預設處理方案", DefaultStatus: http.StatusOK,
	}, auth.ScopeAdmin, h.setDefault)
}

// Profile 是處理方案目前的版本。Config 是存下來的這一層，沒有合併內建預設。
type Profile struct {
	Name      string         `json:"name"`
	Version   int            `json:"version" doc:"目前的版本，從 1 起算；每次儲存加 1"`
	Config    map[string]any `json:"config" doc:"存下來的這一層設定，沒有合併內建預設"`
	SHA256    string         `json:"sha256,omitempty"`
	Builtin   bool           `json:"builtin" doc:"內建的處理方案（default）不能刪除"`
	Default   bool           `json:"default" doc:"是否為預設處理方案"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func toProfile(p pipeline.Profile) Profile {
	cfg := p.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	return Profile{
		Name: p.Name, Version: p.Version, Config: cfg, SHA256: p.SHA256, Builtin: p.Builtin, Default: p.IsDefault,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// ProfileVersion 是處理方案的一個歷史版本（不可修改）。
type ProfileVersion struct {
	Version   int            `json:"version"`
	SHA256    string         `json:"sha256"`
	Config    map[string]any `json:"config"`
	CreatedAt time.Time      `json:"created_at"`
}

type profileIDInput struct {
	ID string `path:"id" doc:"處理方案的名稱"`
}

type profileOutput struct{ Body Profile }

// service 回傳 service；沒有注入時回 500（設定錯誤，不是 501）。
func (h *profilesAPI) service(ctx context.Context) (profileService, error) {
	if h.svc == nil {
		return nil, h.rt.noPipeline(ctx)
	}
	return h.svc, nil
}

type profileListOutput struct {
	Body struct {
		Profiles []Profile `json:"profiles"`
	}
}

func (h *profilesAPI) list(ctx context.Context, _ *struct{}) (*profileListOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	ps, err := svc.Profiles(ctx)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	out := &profileListOutput{}
	out.Body.Profiles = make([]Profile, 0, len(ps))
	for _, p := range ps {
		out.Body.Profiles = append(out.Body.Profiles, toProfile(p))
	}
	return out, nil
}

type createProfileInput struct {
	Body struct {
		// Name 與 Config 都不設必填：缺少或不合法時由 service 回 422 與 errors（路徑 name 排在 config 的錯誤之前）。
		Name   string         `json:"name,omitempty" doc:"處理方案的名稱，^[a-z0-9][a-z0-9_-]{0,63}$；建立後不能改名"`
		Config map[string]any `json:"config,omitempty" nullable:"true" doc:"處理方案的設定；省略或 null 視為 {}"`
	}
}

func (h *profilesAPI) create(ctx context.Context, in *createProfileInput) (*profileOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	cfg := in.Body.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	p, err := svc.CreateProfile(ctx, in.Body.Name, cfg)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	return &profileOutput{Body: toProfile(p)}, nil
}

func (h *profilesAPI) get(ctx context.Context, in *profileIDInput) (*profileOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	p, err := svc.Profile(ctx, in.ID)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	return &profileOutput{Body: toProfile(p)}, nil
}

type patchProfileInput struct {
	ID   string `path:"id" doc:"處理方案的名稱"`
	Body struct {
		Config map[string]any `json:"config,omitempty" nullable:"true" doc:"RFC 7396 merge patch：null 刪除該鍵，物件遞迴合併，陣列與純量整個取代"`
	}
}

func (h *profilesAPI) patch(ctx context.Context, in *patchProfileInput) (*profileOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	if in.Body.Config == nil {
		return nil, configRequired()
	}
	p, err := svc.PatchProfile(ctx, in.ID, in.Body.Config)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	return &profileOutput{Body: toProfile(p)}, nil
}

// configRequired 是 PATCH 沒帶 config 物件的 422；格式同其他逐欄位的錯誤。
func configRequired() *Error {
	e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, "請提供 JSON 物件格式的 config。")
	e.Errors = []validate.FieldError{{Path: "config", Message: "必須是物件。"}}
	return e
}

func (h *profilesAPI) delete(ctx context.Context, in *profileIDInput) (*emptyOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.DeleteProfile(ctx, in.ID); err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	return &emptyOutput{}, nil
}

type profileVersionsOutput struct {
	Body struct {
		Name     string           `json:"name"`
		Versions []ProfileVersion `json:"versions"`
	}
}

func (h *profilesAPI) versions(ctx context.Context, in *profileIDInput) (*profileVersionsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	vs, err := svc.ProfileVersions(ctx, in.ID)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	out := &profileVersionsOutput{}
	out.Body.Name = in.ID
	out.Body.Versions = make([]ProfileVersion, 0, len(vs))
	for _, v := range vs {
		cfg := v.Config
		if cfg == nil {
			cfg = map[string]any{}
		}
		out.Body.Versions = append(out.Body.Versions, ProfileVersion{Version: v.Version, SHA256: v.SHA256, Config: cfg, CreatedAt: v.CreatedAt})
	}
	return out, nil
}

func (h *profilesAPI) setDefault(ctx context.Context, in *profileIDInput) (*profileOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := h.service(ctx)
	if err != nil {
		return nil, err
	}
	p, err := svc.SetDefaultProfile(ctx, in.ID)
	if err != nil {
		return nil, h.rt.mapErr(ctx, err)
	}
	return &profileOutput{Body: toProfile(p)}, nil
}
