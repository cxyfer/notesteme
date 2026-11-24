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
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 這個檔案是產出版本的路由（LIB-R25，LIB-R11 的「舊版本保留並可比較」）：
//   - GET /api/items/{id}/artifacts：列出每個產出保留中的每個版本。
//   - GET /api/artifacts/{id}/content?format=：讀取一個版本的內容（名稱沿用 docs/ui/pages.md §4 的 API 清單）。

func (rt *routes) registerArtifacts(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-artifacts", Method: http.MethodGet, Path: "/api/items/{id}/artifacts",
		Summary: "列出影音每個產出保留中的每個版本（含被取代的版本，PIPE-R7），依種類、變體、語言分組，組內由新到舊（LIB-R25）",
	}, auth.ScopeRead, rt.listArtifacts)
	contentSchema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(cuesBody{}), true, "")
	registerScoped(api, huma.Operation{
		OperationID: "artifacts-content", Method: http.MethodGet, Path: "/api/artifacts/{id}/content",
		Summary: "讀取產出的一個版本（LIB-R25）：字幕的 format 是 json（預設，同 GET /api/items/{id}/cues）、srt、vtt、txt；筆記是 md（預設）。其他種類或 format 回 422；影音在垃圾桶中回 404 in_trash；產出檔損毀回 409 artifact_corrupt",
		Responses: map[string]*huma.Response{"200": {
			Description: "這個版本的內容",
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: contentSchema},
				"text/markdown":    {Schema: &huma.Schema{Type: huma.TypeString}},
				"text/plain":       {Schema: &huma.Schema{Type: huma.TypeString}},
			},
		}},
	}, auth.ScopeRead, rt.artifactContent)
}

// ArtifactVersionBody 是產出的一個版本（LIB-R25）。來源資訊只取來源紀錄中的這幾個欄位（白名單）：
// 來源紀錄另外可能有 Hub 上傳者名稱、匯入資訊等不可信或不必要的內容。
type ArtifactVersionBody struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind" doc:"產出的種類，例如 transcript、note、chapters、frames、summary"`
	Variant       string    `json:"variant,omitempty" doc:"字幕的變體（source、normalized、segmented、corrected、translated、edited）；其他種類省略"`
	Lang          string    `json:"lang,omitempty"`
	Format        string    `json:"format"`
	Size          int64     `json:"size"`
	CreatedAt     time.Time `json:"created_at"`
	Current       bool      `json:"current" doc:"這一組的現行版本（沒有被取代）"`
	Stage         string    `json:"stage,omitempty" doc:"產生它的步驟（來源紀錄的 stage）"`
	Executor      string    `json:"executor,omitempty" doc:"執行方式（來源紀錄的 executor，例如 local、api、agent、human、import）"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	Prompt        string    `json:"prompt,omitempty"`
	PromptVersion int       `json:"prompt_version,omitempty"`
}

type artifactsOutput struct {
	Body struct {
		Artifacts []ArtifactVersionBody `json:"artifacts"`
	}
}

type artifactsInput struct {
	ID      string `path:"id" doc:"影音 ID 或代號"`
	Kind    string `query:"kind" doc:"只列這個種類（例如 transcript、note）；省略時全部"`
	Variant string `query:"variant" doc:"只列這個字幕變體（例如 corrected）；省略時全部"`
}

func (rt *routes) listArtifacts(ctx context.Context, in *artifactsInput) (*artifactsOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	id, err := rt.itemID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	vs, err := rt.d.Pipeline.ItemArtifacts(ctx, id)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &artifactsOutput{}
	out.Body.Artifacts = make([]ArtifactVersionBody, 0, len(vs))
	for _, v := range vs {
		if (in.Kind != "" && v.Kind != in.Kind) || (in.Variant != "" && v.Variant != in.Variant) {
			continue
		}
		out.Body.Artifacts = append(out.Body.Artifacts, toArtifactVersion(v))
	}
	return out, nil
}

func toArtifactVersion(v pipeline.ArtifactVersion) ArtifactVersionBody {
	b := ArtifactVersionBody{ID: v.ID, Kind: v.Kind, Variant: v.Variant, Lang: v.Lang, Format: v.Format, Size: v.Size,
		CreatedAt: v.CreatedAt.UTC(), Current: v.Current}
	var prov map[string]any
	if json.Unmarshal(v.Provenance, &prov) != nil {
		return b
	}
	str := func(k string) string {
		s, _ := prov[k].(string)
		if len(s) > 200 {
			return ""
		}
		return s
	}
	b.Stage, b.Executor, b.Provider, b.Model, b.Prompt = str("stage"), str("executor"), str("provider"), str("model"), str("prompt")
	if n, ok := prov["prompt_version"].(float64); ok && n > 0 && n == float64(int(n)) {
		b.PromptVersion = int(n)
	}
	return b
}

type artifactContentInput struct {
	ID     string `path:"id" doc:"產出 ID（GET /api/items/{id}/artifacts 的 id）"`
	Format string `query:"format" doc:"字幕：json（預設）、srt、vtt、txt；筆記：md（預設）"`
}

// cuesBody 是 GET /api/items/{id}/cues 的回應內容；版本內容的 json 用同一個形狀，前端共用同一套處理。
type cuesBody struct {
	Variant string `json:"variant"`
	Lang    string `json:"lang"`
	Cues    []Cue  `json:"cues"`
}

func (rt *routes) artifactContent(ctx context.Context, in *artifactContentInput) (*huma.StreamResponse, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這個產出。")
	}
	it, a, err := rt.d.Pipeline.ArtifactWithItem(ctx, in.ID)
	if errors.Is(err, pipeline.ErrArtifactNotFound) {
		return nil, newError(http.StatusNotFound, CodeNotFound, err.Error())
	}
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	switch a.Kind {
	case "transcript":
		format := in.Format
		if format == "" {
			format = "json"
		}
		if format != "json" && format != subtitle.FormatSRT && format != subtitle.FormatVTT && format != subtitle.FormatTXT {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "字幕的 format 必須是 json、srt、vtt 或 txt。")
		}
		tr, err := rt.d.Pipeline.TranscriptVersion(ctx, a)
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		if format == "json" {
			body := cuesBody{Variant: a.Variant, Lang: a.Lang, Cues: make([]Cue, 0, len(tr.Cues))}
			for _, c := range tr.Cues {
				body.Cues = append(body.Cues, toCue(c))
			}
			return jsonStream(body)
		}
		name := subtitle.DownloadFileName(it.Title, a.Lang, subtitle.FileExt(format))
		return &huma.StreamResponse{Body: func(hc huma.Context) {
			hc.SetHeader("Content-Type", subtitle.ContentType(format))
			hc.SetHeader("Content-Disposition", subtitle.ContentDisposition(name))
			hc.SetHeader("X-Content-Type-Options", "nosniff")
			hc.SetStatus(http.StatusOK)
			if err := subtitle.Render(hc.BodyWriter(), format, tr); err != nil {
				rt.d.Log.WarnContext(hc.Context(), "輸出字幕時中斷", "error", err.Error())
			}
		}}, nil
	case pipeline.KindNote:
		if in.Format != "" && in.Format != "md" {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "筆記的 format 必須是 md。")
		}
		md, err := rt.d.Pipeline.NoteVersion(ctx, a)
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		return &huma.StreamResponse{Body: func(hc huma.Context) {
			hc.SetHeader("Content-Type", "text/markdown; charset=utf-8")
			hc.SetHeader("X-Content-Type-Options", "nosniff")
			hc.SetStatus(http.StatusOK)
			_, _ = hc.BodyWriter().Write([]byte(md))
		}}, nil
	}
	return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "這種產出（"+a.Kind+"）不能讀取內容，只有字幕與筆記可以。")
}

func jsonStream(v any) (*huma.StreamResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", "application/json")
		hc.SetStatus(http.StatusOK)
		_, _ = hc.BodyWriter().Write(b)
	}}, nil
}
