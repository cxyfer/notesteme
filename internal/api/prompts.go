package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
)

// 這個檔案是 prompt 庫的 6 條路由（PRM-R1～R5、R8）：列出、建立、刪除、版本列表、新增版本、設為預設。
// playground 與 compare 要呼叫 LLM，屬於 LLM 執行切片（S3），在 m2_stubs.go 維持 501。

func (rt *routes) registerPrompts(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "prompts-list", Method: http.MethodGet, Path: "/api/prompts",
		Summary: "列出提示詞：內建的在前，依 id 排序，接著是自訂的（PRM）",
	}, auth.ScopeRead, rt.listPrompts)
	registerScoped(api, huma.Operation{
		OperationID: "prompts-create", Method: http.MethodPost, Path: "/api/prompts",
		Summary: "建立提示詞（版本 1，也是預設版本）；未知的類型或未定義的變數回 422（PRM-R1、R3）", DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createPrompt)
	registerScoped(api, huma.Operation{
		OperationID: "prompts-delete", Method: http.MethodDelete, Path: "/api/prompts/{id}",
		Summary:       "刪除提示詞與它的所有版本；內建的回 409 builtin_prompt，被引用時回 409 prompt_in_use 並列出 used_by（PRM-R4、R8）",
		DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, rt.deletePrompt)
	registerScoped(api, huma.Operation{
		OperationID: "prompts-versions-list", Method: http.MethodGet, Path: "/api/prompts/{id}/versions",
		Summary: "提示詞的所有版本，依版本遞增；內建的也可以（PRM-R2）",
	}, auth.ScopeRead, rt.listPromptVersions)
	registerScoped(api, huma.Operation{
		OperationID: "prompts-versions-create", Method: http.MethodPost, Path: "/api/prompts/{id}/versions",
		Summary: "新增提示詞版本；不改變預設版本，內建的回 409 builtin_prompt（PRM-R2、R4、R5）", DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.addPromptVersion)
	registerScoped(api, huma.Operation{
		OperationID: "prompts-default", Method: http.MethodPost, Path: "/api/prompts/{id}/default",
		Summary: "把指定版本設為預設；引用 default 的處理方案從下一次執行開始改用它（PRM-R5）",
	}, auth.ScopeAdmin, rt.setPromptDefault)
}

// PromptSummary 是提示詞的摘要。
type PromptSummary struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind" doc:"summary、chapters、segment、correct、translate、frames、note 或 note_map"`
	Builtin        bool      `json:"builtin" doc:"內建的提示詞不能修改"`
	DefaultVersion int       `json:"default_version" doc:"id 與 id@default 解析到的版本"`
	LatestVersion  int       `json:"latest_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PromptVersionBody 是提示詞的一個版本。
type PromptVersionBody struct {
	Version   int       `json:"version"`
	SHA256    string    `json:"sha256" doc:"內文（CRLF 換成 LF 之後）的 sha256"`
	Body      string    `json:"body"`
	Variables []string  `json:"variables" doc:"內文用到的 {{變數}}，依第一次出現的順序"`
	CreatedAt time.Time `json:"created_at"`
}

func (rt *routes) promptsOr500(ctx context.Context) (*prompts.Service, error) {
	if rt.d.Prompts == nil {
		// 沒有注入 prompt 庫是設定錯誤，不是「尚未實作」（ADR-0015），所以回 500。
		rt.d.Log.LogAttrs(ctx, slog.LevelError, "處理請求時發生錯誤", slog.String("error", "api: 沒有設定 Prompts"))
		return nil, newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
	}
	return rt.d.Prompts, nil
}

// mapPromptErr 把 prompts 套件的錯誤對應到 HTTP 錯誤：
// undefined_variables 與其他輸入錯誤 → 422；InUseError → 409 並列出 used_by；
// ErrBuiltin → 409 builtin_prompt；ErrExists → 409 prompt_exists；找不到 → 404。
// 其他錯誤交給 mapErr（pipeline 的 501 等；沒有對應的視為內部錯誤）。
func (rt *routes) mapPromptErr(ctx context.Context, err error) error {
	var (
		inv *prompts.InvalidError
		iu  *prompts.InUseError
	)
	switch {
	case errors.As(err, &inv):
		e := fieldsError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Msg, inv.Fields)
		e.UndefinedVariables = inv.Undefined
		return e
	case errors.As(err, &iu):
		e := newError(http.StatusConflict, CodePromptInUse, iu.Error())
		e.UsedBy = iu.UsedBy
		return e
	case errors.Is(err, prompts.ErrBuiltin):
		return newError(http.StatusConflict, CodeBuiltinPrompt, err.Error())
	case errors.Is(err, prompts.ErrExists):
		return newError(http.StatusConflict, CodePromptExists, err.Error())
	case errors.Is(err, prompts.ErrNotFound), errors.Is(err, prompts.ErrVersionNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	}
	var ni *pipeline.NotImplementedError
	if errors.As(err, &ni) {
		return newError(http.StatusNotImplemented, CodeNotImplemented, ni.Error())
	}
	return rt.mapErr(ctx, err)
}

type promptsOutput struct {
	Body struct {
		Prompts []PromptSummary `json:"prompts"`
	}
}

func (rt *routes) listPrompts(ctx context.Context, _ *struct{}) (*promptsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	out := &promptsOutput{}
	out.Body.Prompts = make([]PromptSummary, 0, len(list))
	for _, p := range list {
		out.Body.Prompts = append(out.Body.Prompts, PromptSummary{
			ID: p.ID, Kind: p.Kind, Builtin: p.Builtin, DefaultVersion: p.DefaultVersion, LatestVersion: p.LatestVersion,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, nil
}

type createPromptInput struct {
	Body struct {
		ID   string `json:"id,omitempty" doc:"小寫英文字母、數字、「.」與「-」，不可以和內建的相同"`
		Kind string `json:"kind,omitempty" doc:"summary、chapters、segment、correct、translate、frames、note 或 note_map"`
		Body string `json:"body,omitempty" doc:"內文，可用 {{title}}、{{channel}}、{{description}}、{{chapters}}、{{glossary}}、{{source_lang}}、{{target_lang}}"`
	}
}

type createPromptOutput struct {
	Body struct {
		ID             string `json:"id"`
		Kind           string `json:"kind"`
		Version        int    `json:"version"`
		DefaultVersion int    `json:"default_version"`
		SHA256         string `json:"sha256"`
		Builtin        bool   `json:"builtin"`
	}
}

func (rt *routes) createPrompt(ctx context.Context, in *createPromptInput) (*createPromptOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	p, v, err := svc.Create(ctx, in.Body.ID, in.Body.Kind, in.Body.Body)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	out := &createPromptOutput{}
	out.Body.ID, out.Body.Kind, out.Body.Version = p.ID, p.Kind, v.Version
	out.Body.DefaultVersion, out.Body.SHA256, out.Body.Builtin = p.DefaultVersion, v.SHA256, p.Builtin
	return out, nil
}

type promptIDInput struct {
	ID string `path:"id" doc:"提示詞 id"`
}

func (rt *routes) deletePrompt(ctx context.Context, in *promptIDInput) (*struct{}, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	if err := svc.Delete(ctx, in.ID); err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	return nil, nil
}

type promptVersionsOutput struct {
	Body struct {
		ID       string              `json:"id"`
		Versions []PromptVersionBody `json:"versions"`
	}
}

func (rt *routes) listPromptVersions(ctx context.Context, in *promptIDInput) (*promptVersionsOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	vs, err := svc.Versions(ctx, in.ID)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	out := &promptVersionsOutput{}
	out.Body.ID = in.ID
	out.Body.Versions = make([]PromptVersionBody, 0, len(vs))
	for _, v := range vs {
		out.Body.Versions = append(out.Body.Versions, PromptVersionBody{
			Version: v.Version, SHA256: v.SHA256, Body: v.Body, Variables: v.Variables, CreatedAt: v.CreatedAt,
		})
	}
	return out, nil
}

type addPromptVersionInput struct {
	ID   string `path:"id" doc:"提示詞 id"`
	Body struct {
		Body string `json:"body,omitempty" doc:"新版本的內文"`
	}
}

type addPromptVersionOutput struct {
	Body struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
	}
}

func (rt *routes) addPromptVersion(ctx context.Context, in *addPromptVersionInput) (*addPromptVersionOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	v, err := svc.AddVersion(ctx, in.ID, in.Body.Body)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	out := &addPromptVersionOutput{}
	out.Body.ID, out.Body.Version, out.Body.SHA256 = in.ID, v.Version, v.SHA256
	return out, nil
}

type setPromptDefaultInput struct {
	ID   string `path:"id" doc:"提示詞 id"`
	Body struct {
		Version int `json:"version,omitempty" doc:"要設為預設的版本；不存在回 422"`
	}
}

type setPromptDefaultOutput struct {
	Body struct {
		ID             string `json:"id"`
		DefaultVersion int    `json:"default_version"`
	}
}

func (rt *routes) setPromptDefault(ctx context.Context, in *setPromptDefaultInput) (*setPromptDefaultOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.promptsOr500(ctx)
	if err != nil {
		return nil, err
	}
	p, err := svc.SetDefault(ctx, in.ID, in.Body.Version)
	if err != nil {
		return nil, rt.mapPromptErr(ctx, err)
	}
	out := &setPromptDefaultOutput{}
	out.Body.ID, out.Body.DefaultVersion = p.ID, p.DefaultVersion
	return out, nil
}
