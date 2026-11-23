package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 筆記端點的錯誤代碼（NOTE-R11～R13）。
const (
	// CodeNoNote：影音還沒有筆記（GET note 回 404，預覽回 409）。
	CodeNoNote = "no_note"
	// CodeBuiltinTemplate：內建的筆記模板不能修改或覆蓋。
	CodeBuiltinTemplate = "builtin_template"
	// CodeTemplateExists：已經有同 id 的筆記模板。
	CodeTemplateExists = "template_exists"
)

// NoteCheck 是一項自動檢查的結果（NOTE-R7）。
type NoteCheck struct {
	ID      string `json:"id" doc:"檢查的 id：frontmatter、heading_timestamps、images_exist、image_names、self_check"`
	OK      bool   `json:"ok" doc:"是否通過"`
	Message string `json:"message,omitempty" doc:"沒有通過時的說明"`
}

// NoteBody 是 GET /api/items/{id}/note 的 JSON 回應（NOTE-R13）。
type NoteBody struct {
	Lang        string            `json:"lang" doc:"筆記的語言"`
	Markdown    string            `json:"markdown" doc:"筆記的 Markdown；由 LLM 依字幕寫成，是不可信的資料"`
	Frontmatter map[string]string `json:"frontmatter" doc:"frontmatter 的欄位（值都是字串）"`
	Checks      []NoteCheck       `json:"checks" doc:"自動檢查的結果，依檢查的順序"`
	Stale       bool              `json:"stale" doc:"輸入字幕已更新、筆記可能過時（NOTE-R10）"`
	StaleReason string            `json:"stale_reason,omitempty" doc:"過時的原因"`
	Outputs     []NoteOutput      `json:"outputs" doc:"筆記寫入的位置（vault，VAULT-R5）"`
}

// NoteOutput 是筆記寫入 vault 的一個檔案（VAULT-R5）。
type NoteOutput struct {
	Target string `json:"target" doc:"輸出目標，固定是 vault"`
	Vault  string `json:"vault" doc:"vault 的 id"`
	Kind   string `json:"kind" doc:"檔案的種類：note、asset 或 subtitle"`
	Path   string `json:"path" doc:"vault 內的路徑（相對於 vault 根目錄）"`
	State  string `json:"state" doc:"最近一次發布時的結果：written、unchanged、saved_as_new、skipped_user_changed"`
	Commit string `json:"commit,omitempty" doc:"git vault 中這個檔案所在的 commit"`
}

type noteInput struct {
	ID     string `path:"id" doc:"影音 ID 或代號"`
	Lang   string `query:"lang" doc:"筆記的語言；沒有指定時用最近寫出的一份"`
	Format string `query:"format" doc:"json（預設）或 md（回傳 text/markdown）"`
}

// NoteTemplateBody 是一個筆記模板（NOTE-R11）。
type NoteTemplateBody struct {
	ID         string    `json:"id" doc:"模板 id"`
	Name       string    `json:"name" doc:"顯示名稱"`
	Version    int       `json:"version" doc:"版本，從 1 起算；PATCH 建立新版本"`
	Builtin    bool      `json:"builtin" doc:"是否為內建模板（不能修改，只能複製）"`
	Body       string    `json:"body" doc:"模板內文（text/template 的受限子集）"`
	ReadButton string    `json:"read_button" doc:"「閱讀」按鈕的 Markdown；空字串代表不加"`
	Checks     []string  `json:"checks" doc:"要執行的自動檢查"`
	SHA256     string    `json:"sha256" doc:"內文的 SHA-256"`
	CreatedAt  time.Time `json:"created_at" doc:"這個版本建立的時間"`
}

type noteTemplatesOutput struct {
	Body struct {
		Templates []NoteTemplateBody `json:"templates" doc:"內建模板在前，其他依 id 排序，每個只列最新版本"`
	}
}

type noteTemplateOutput struct{ Body NoteTemplateBody }

type createNoteTemplateInput struct {
	Body struct {
		ID         string    `json:"id" doc:"模板 id：小寫英數字與 -，1～64 字元"`
		From       string    `json:"from,omitempty" doc:"要複製的模板（例如 video-notes）"`
		Name       *string   `json:"name,omitempty" doc:"顯示名稱"`
		Body       *string   `json:"body,omitempty" doc:"模板內文；沒有時沿用 from 的內文"`
		ReadButton *string   `json:"read_button,omitempty" doc:"「閱讀」按鈕的 Markdown"`
		Checks     *[]string `json:"checks,omitempty" doc:"要執行的自動檢查"`
	}
}

type patchNoteTemplateInput struct {
	ID   string `path:"id" doc:"模板 id"`
	Body struct {
		Name       *string   `json:"name,omitempty" doc:"顯示名稱"`
		Body       *string   `json:"body,omitempty" doc:"模板內文"`
		ReadButton *string   `json:"read_button,omitempty" doc:"「閱讀」按鈕的 Markdown"`
		Checks     *[]string `json:"checks,omitempty" doc:"要執行的自動檢查"`
	}
}

type previewNoteTemplateInput struct {
	ID   string `path:"id" doc:"模板 id（可帶 @版本）"`
	Body struct {
		Item string `json:"item" minLength:"1" doc:"影音 ID 或代號"`
		Lang string `json:"lang,omitempty" doc:"筆記的語言；沒有指定時用最近寫出的一份"`
	}
}

type notePreviewOutput struct {
	Body struct {
		Markdown string      `json:"markdown" doc:"套用模板後的 Markdown"`
		Checks   []NoteCheck `json:"checks" doc:"自動檢查的結果"`
	}
}

func (rt *routes) registerNotes(api huma.API) {
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(NoteBody{}), true, "")
	registerScoped(api, huma.Operation{
		OperationID: "items-note", Method: http.MethodGet, Path: "/api/items/{id}/note",
		Summary: "取得影音的筆記（NOTE-R13）：Markdown、frontmatter、自動檢查與是否過時；format=md 時回傳 text/markdown；沒有筆記時回 404 no_note",
		Responses: map[string]*huma.Response{"200": {
			Description: "筆記",
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: schema},
				"text/markdown":    {Schema: &huma.Schema{Type: huma.TypeString}},
			},
		}},
	}, auth.ScopeRead, rt.getNote)
	registerScoped(api, huma.Operation{
		OperationID: "note-templates-list", Method: http.MethodGet, Path: "/api/note-templates",
		Summary: "列出筆記模板，每個只列最新版本（NOTE-R11）",
	}, auth.ScopeRead, rt.listNoteTemplates)
	registerScoped(api, huma.Operation{
		OperationID: "note-templates-create", Method: http.MethodPost, Path: "/api/note-templates",
		Summary:       "建立筆記模板或複製內建模板（NOTE-R11）；未知的變數或語法錯誤回 422，內建 id 回 409 builtin_template，已存在回 409 template_exists",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createNoteTemplate)
	registerScoped(api, huma.Operation{
		OperationID: "note-templates-update", Method: http.MethodPatch, Path: "/api/note-templates/{id}",
		Summary: "修改筆記模板，建立新版本（NOTE-R11）；內建模板回 409 builtin_template",
	}, auth.ScopeAdmin, rt.updateNoteTemplate)
	registerScoped(api, huma.Operation{
		OperationID: "note-templates-preview", Method: http.MethodPost, Path: "/api/note-templates/{id}/preview",
		Summary: "用影音現行筆記的段落預覽模板（NOTE-R12）：不呼叫 LLM、不建立產出、不寫入檔案；影音沒有筆記時回 409 no_note",
	}, auth.ScopeAdmin, rt.previewNoteTemplate)
}

func toNoteChecks(cs []notes.Check) []NoteCheck {
	out := make([]NoteCheck, 0, len(cs))
	for _, c := range cs {
		out = append(out, NoteCheck{ID: c.ID, OK: c.OK, Message: c.Message})
	}
	return out
}

func (rt *routes) getNote(ctx context.Context, in *noteInput) (*huma.StreamResponse, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	if in.Format != "" && in.Format != "json" && in.Format != "md" {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "format 必須是 json 或 md。")
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	v, err := rt.d.Pipeline.ItemNote(ctx, id, in.Lang)
	if err != nil {
		return nil, rt.noteErr(ctx, err, http.StatusNotFound)
	}
	if in.Format == "md" {
		return &huma.StreamResponse{Body: func(hc huma.Context) {
			hc.SetHeader("Content-Type", "text/markdown; charset=utf-8")
			hc.SetHeader("X-Content-Type-Options", "nosniff")
			hc.SetStatus(http.StatusOK)
			_, _ = hc.BodyWriter().Write([]byte(v.Markdown))
		}}, nil
	}
	body := NoteBody{Lang: v.Lang, Markdown: v.Markdown, Frontmatter: v.Frontmatter, Checks: toNoteChecks(v.Checks),
		Stale: v.Stale, StaleReason: v.StaleReason, Outputs: []NoteOutput{}}
	for _, o := range v.Outputs {
		body.Outputs = append(body.Outputs, NoteOutput{Target: o.Target, Vault: o.Vault, Kind: o.Kind, Path: o.Path, State: o.State, Commit: o.GitCommit})
	}
	if body.Frontmatter == nil {
		body.Frontmatter = map[string]string{}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", "application/json")
		hc.SetStatus(http.StatusOK)
		_, _ = hc.BodyWriter().Write(b)
	}}, nil
}

func toNoteTemplateBody(t pipeline.NoteTemplate) NoteTemplateBody {
	b := NoteTemplateBody{ID: t.ID, Name: t.Name, Version: t.Version, Builtin: t.Builtin, Body: t.Body,
		ReadButton: t.ReadButton, Checks: t.Checks, SHA256: t.SHA256, CreatedAt: t.CreatedAt}
	if b.Checks == nil {
		b.Checks = append([]string{}, notes.CheckIDs...)
	}
	return b
}

func (rt *routes) listNoteTemplates(ctx context.Context, _ *struct{}) (*noteTemplatesOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	list, err := rt.d.Pipeline.NoteTemplates(ctx)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &noteTemplatesOutput{}
	out.Body.Templates = []NoteTemplateBody{}
	for _, t := range list {
		out.Body.Templates = append(out.Body.Templates, toNoteTemplateBody(t))
	}
	return out, nil
}

func (rt *routes) createNoteTemplate(ctx context.Context, in *createNoteTemplateInput) (*noteTemplateOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	b := in.Body
	t, err := rt.d.Pipeline.CreateNoteTemplate(ctx, pipeline.NoteTemplateInput{ID: b.ID, From: b.From, Name: b.Name,
		Body: b.Body, ReadButton: b.ReadButton, Checks: b.Checks})
	if err != nil {
		return nil, rt.noteErr(ctx, err, http.StatusNotFound)
	}
	return &noteTemplateOutput{Body: toNoteTemplateBody(t)}, nil
}

func (rt *routes) updateNoteTemplate(ctx context.Context, in *patchNoteTemplateInput) (*noteTemplateOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	b := in.Body
	t, err := rt.d.Pipeline.UpdateNoteTemplate(ctx, in.ID, pipeline.NoteTemplateInput{Name: b.Name, Body: b.Body,
		ReadButton: b.ReadButton, Checks: b.Checks})
	if err != nil {
		return nil, rt.noteErr(ctx, err, http.StatusNotFound)
	}
	return &noteTemplateOutput{Body: toNoteTemplateBody(t)}, nil
}

func (rt *routes) previewNoteTemplate(ctx context.Context, in *previewNoteTemplateInput) (*notePreviewOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	pv, err := rt.d.Pipeline.PreviewNoteTemplate(ctx, in.ID, in.Body.Item, in.Body.Lang)
	if err != nil {
		// 預覽時影音沒有筆記是狀態衝突（409），不是找不到資源（NOTE-R12）。
		return nil, rt.noteErr(ctx, err, http.StatusConflict)
	}
	out := &notePreviewOutput{}
	out.Body.Markdown, out.Body.Checks = pv.Markdown, toNoteChecks(pv.Checks)
	return out, nil
}

// noteErr 對應筆記與模板的錯誤；noNoteStatus 是沒有筆記時的狀態碼（GET 404、預覽 409）。
func (rt *routes) noteErr(ctx context.Context, err error, noNoteStatus int) error {
	switch {
	case errors.Is(err, pipeline.ErrNoNote):
		return newError(noNoteStatus, CodeNoNote, err.Error())
	case errors.Is(err, pipeline.ErrBuiltinTemplate):
		return newError(http.StatusConflict, CodeBuiltinTemplate, err.Error())
	case errors.Is(err, pipeline.ErrTemplateExists):
		return newError(http.StatusConflict, CodeTemplateExists, err.Error())
	case errors.Is(err, pipeline.ErrTemplateNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	}
	return rt.mapErr(ctx, err)
}
