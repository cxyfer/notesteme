package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 這個檔案是 ASS 樣式庫的路由（OUT-R6～R9）：列出、讀取、建立（複製）、修改、從 .ass 檔匯入與預覽。
// scope：讀取與預覽用 read（預覽會執行 ffmpeg，同時最多 maxPreviews 個）；建立、修改、匯入用 admin。

func (rt *routes) registerAssStyles(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "ass-styles-list", Method: http.MethodGet, Path: "/api/ass-styles",
		Summary: "列出 ASS 樣式：內建的在前，接著是自訂的；字型依 lang 帶入預設值（OUT-R6、R7）",
	}, auth.ScopeRead, rt.listAssStyles)
	registerScoped(api, huma.Operation{
		OperationID: "ass-styles-create", Method: http.MethodPost, Path: "/api/ass-styles",
		Summary:       "建立自訂樣式，可以複製內建或自訂樣式（from）再修改；內容不正確回 422，名稱重複回 409（OUT-R6）",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createAssStyle)
	registerScoped(api, huma.Operation{
		OperationID: "ass-styles-get", Method: http.MethodGet, Path: "/api/ass-styles/{id}",
		Summary: "讀取樣式；沒有指定字型的文字層依 lang 帶入預設字型（OUT-R7）",
	}, auth.ScopeRead, rt.getAssStyle)
	registerScoped(api, huma.Operation{
		OperationID: "ass-styles-patch", Method: http.MethodPatch, Path: "/api/ass-styles/{id}",
		Summary: "修改自訂樣式（沒有出現的欄位維持原值）；內建樣式回 409 builtin_style（OUT-R6）",
	}, auth.ScopeAdmin, rt.patchAssStyle)
	registerScoped(api, huma.Operation{
		OperationID: "ass-styles-preview", Method: http.MethodPost, Path: "/api/ass-styles/preview",
		Summary: "用 ffmpeg + libass 把樣式渲染在影音指定時間的畫面上，回傳 PNG（OUT-R9）", DefaultStatus: http.StatusOK,
	}, auth.ScopeRead, rt.previewAssStyle)
	registerScoped(api, huma.Operation{
		OperationID: "ass-styles-import", Method: http.MethodPost, Path: "/api/ass-styles/import",
		Summary:       "從 .ass 檔的 [V4+ Styles] 匯入成自訂樣式；沒有這個段落回 422（OUT-R8）",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: importBodyLimit,
	}, auth.ScopeAdmin, rt.importAssStyle)
}

// importBodyLimit 是匯入的請求本體上限：內容上限加上 JSON 跳脫的餘裕。
const importBodyLimit = 2*pipeline.MaxImportBytes + 4096

// maxStyleImportBytes 是 .ass 樣式檔的大小上限：只讀樣式，不需要整份字幕那麼大。
const maxStyleImportBytes = 2 << 20

// StyleBody 是樣式的回應：id 與 name 相同；字型已依 lang 帶入（沒有指定 lang 時用 Noto Sans）。
type StyleBody struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Builtin     bool               `json:"builtin" doc:"內建的樣式不能修改，只能複製成自訂樣式"`
	Primary     subtitle.ASSLine   `json:"primary" doc:"原文行"`
	Translation subtitle.ASSLine   `json:"translation" doc:"譯文行"`
	Layout      subtitle.ASSLayout `json:"layout" doc:"版面"`
}

func toStyleBody(st assstyle.Style, lang, sourceLang string) StyleBody {
	a := assstyle.ResolveFonts(st.ASS(), lang, sourceLang)
	return StyleBody{ID: st.ID, Name: st.Name, Builtin: st.Builtin, Primary: a.Primary, Translation: a.Translation, Layout: a.Layout}
}

func (rt *routes) assStylesOr500(ctx context.Context) (*assstyle.Service, error) {
	if rt.d.AssStyles == nil {
		return nil, rt.noAssStyles(ctx)
	}
	return rt.d.AssStyles, nil
}

// noAssStyles 回傳沒有注入樣式庫時的錯誤：設定錯誤，不是「尚未實作」（ADR-0015），所以回 500。
func (rt *routes) noAssStyles(ctx context.Context) error {
	rt.d.Log.LogAttrs(ctx, slog.LevelError, "處理請求時發生錯誤", slog.String("error", "api: 沒有設定 AssStyles"))
	return newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}

// mapStyleErr 把 assstyle 的錯誤對應到 HTTP 錯誤：內容不正確 422、內建樣式 409 builtin_style、重名 409 style_exists、找不到 404。
func (rt *routes) mapStyleErr(ctx context.Context, err error) error {
	var inv *assstyle.InvalidError
	switch {
	case errors.As(err, &inv):
		e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, inv.Error())
		e.Errors = inv.Fields
		return e
	case errors.Is(err, assstyle.ErrBuiltin):
		return newError(http.StatusConflict, CodeBuiltinStyle, err.Error())
	case errors.Is(err, assstyle.ErrExists):
		return newError(http.StatusConflict, CodeStyleExists, err.Error())
	case errors.Is(err, assstyle.ErrNotFound):
		return newError(http.StatusNotFound, CodeNotFound, err.Error())
	}
	return rt.mapErr(ctx, err)
}

type listAssStylesInput struct {
	Lang       string `query:"lang" doc:"目標語言：沒有指定字型的譯文行依它帶入預設字型（OUT-R7）"`
	SourceLang string `query:"source_lang" doc:"原文語言：沒有指定字型的原文行依它帶入預設字型"`
}

type listAssStylesOutput struct {
	Body struct {
		Styles []StyleBody `json:"styles"`
	}
}

func (rt *routes) listAssStyles(ctx context.Context, in *listAssStylesInput) (*listAssStylesOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := rt.assStylesOr500(ctx)
	if err != nil {
		return nil, err
	}
	list, err := svc.List(ctx)
	if err != nil {
		return nil, rt.mapStyleErr(ctx, err)
	}
	out := &listAssStylesOutput{}
	out.Body.Styles = make([]StyleBody, 0, len(list))
	for _, st := range list {
		out.Body.Styles = append(out.Body.Styles, toStyleBody(st, in.Lang, in.SourceLang))
	}
	return out, nil
}

type assStyleInput struct {
	ID         string `path:"id" doc:"樣式名稱"`
	Lang       string `query:"lang" doc:"目標語言：沒有指定字型的譯文行依它帶入預設字型（OUT-R7）"`
	SourceLang string `query:"source_lang" doc:"原文語言：沒有指定字型的原文行依它帶入預設字型"`
}

type assStyleOutput struct{ Body StyleBody }

func (rt *routes) getAssStyle(ctx context.Context, in *assStyleInput) (*assStyleOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := rt.assStylesOr500(ctx)
	if err != nil {
		return nil, err
	}
	st, err := svc.Get(ctx, in.ID)
	if err != nil {
		return nil, rt.mapStyleErr(ctx, err)
	}
	return &assStyleOutput{Body: toStyleBody(st, in.Lang, in.SourceLang)}, nil
}

type createAssStyleInput struct {
	Body struct {
		Name        string                `json:"name,omitempty" doc:"小寫英文字母、數字、「.」「_」「-」，最長 64 個字元；不可以和內建的相同"`
		From        string                `json:"from,omitempty" doc:"複製哪個樣式（內建或自訂），預設 default"`
		Primary     *assstyle.LinePatch   `json:"primary,omitempty" doc:"原文行，覆寫複製來源的欄位"`
		Translation *assstyle.LinePatch   `json:"translation,omitempty" doc:"譯文行，覆寫複製來源的欄位"`
		Layout      *assstyle.LayoutPatch `json:"layout,omitempty" doc:"版面，覆寫複製來源的欄位"`
	}
}

func (rt *routes) createAssStyle(ctx context.Context, in *createAssStyleInput) (*assStyleOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.assStylesOr500(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	st, err := svc.Create(ctx, b.Name, b.From, assstyle.Patch{Primary: b.Primary, Translation: b.Translation, Layout: b.Layout})
	if err != nil {
		return nil, rt.mapStyleErr(ctx, err)
	}
	return &assStyleOutput{Body: toStyleBody(st, "", "")}, nil
}

type patchAssStyleInput struct {
	ID   string `path:"id" doc:"樣式名稱"`
	Body struct {
		Primary     *assstyle.LinePatch   `json:"primary,omitempty" doc:"原文行"`
		Translation *assstyle.LinePatch   `json:"translation,omitempty" doc:"譯文行"`
		Layout      *assstyle.LayoutPatch `json:"layout,omitempty" doc:"版面"`
	}
}

func (rt *routes) patchAssStyle(ctx context.Context, in *patchAssStyleInput) (*assStyleOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.assStylesOr500(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	st, err := svc.Update(ctx, in.ID, assstyle.Patch{Primary: b.Primary, Translation: b.Translation, Layout: b.Layout})
	if err != nil {
		return nil, rt.mapStyleErr(ctx, err)
	}
	return &assStyleOutput{Body: toStyleBody(st, "", "")}, nil
}

type importAssStyleInput struct {
	Body struct {
		Name             string `json:"name,omitempty" doc:"自訂樣式的名稱"`
		Path             string `json:"path,omitempty" doc:"伺服器上 .ass 或 .ssa 檔的絕對路徑；與 content 擇一"`
		Content          string `json:"content,omitempty" doc:".ass 檔的內容；與 path 擇一"`
		TranslationStyle string `json:"translation_style,omitempty" doc:"檔案中哪個樣式是譯文行，預設是 Default，沒有就用第一個"`
		PrimaryStyle     string `json:"primary_style,omitempty" doc:"檔案中哪個樣式是原文行，預設是下一個樣式；只有一個樣式時由譯文行縮小產生"`
	}
}

func (rt *routes) importAssStyle(ctx context.Context, in *importAssStyleInput) (*assStyleOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	svc, err := rt.assStylesOr500(ctx)
	if err != nil {
		return nil, err
	}
	b := in.Body
	data, _, ierr := readImportInput(b.Path, b.Content, maxStyleImportBytes, []string{".ass", ".ssa"})
	if ierr != nil {
		return nil, ierr
	}
	st, err := svc.Import(ctx, data, b.Name, assstyle.ImportOptions{TranslationStyle: b.TranslationStyle, PrimaryStyle: b.PrimaryStyle})
	if err != nil {
		return nil, rt.mapStyleErr(ctx, err)
	}
	return &assStyleOutput{Body: toStyleBody(st, "", "")}, nil
}

// maxPreviews 是同時進行的預覽（ffmpeg）數量上限；多的請求排隊等待。
const maxPreviews = 2

var previewSlots = make(chan struct{}, maxPreviews)

type previewInput struct {
	Body struct {
		Style string          `json:"style,omitempty" doc:"樣式名稱，預設 default"`
		Item  string          `json:"item,omitempty" doc:"影音的 ID 或短代號（例如 yt:abc123xxxxx）"`
		At    string          `json:"at,omitempty" doc:"畫面的時間，HH:MM:SS(.mmm) 或秒數，預設 0；也決定預覽用哪一句字幕"`
		Lang  string          `json:"lang,omitempty" doc:"目標語言；沒有指定時用影音最近的譯文語言"`
		Patch *assstyle.Patch `json:"patch,omitempty" doc:"套用在樣式上的暫時修改（還沒儲存的調整）；不會寫入樣式"`
	}
}

func (rt *routes) previewAssStyle(ctx context.Context, in *previewInput) (*huma.StreamResponse, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	svc, err := rt.assStylesOr500(ctx)
	if err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	b := in.Body
	if b.Item == "" {
		return nil, invalidInput("item：請指定影音。")
	}
	name := b.Style
	if name == "" {
		name = "default"
	}
	st, err := svc.Get(ctx, name)
	if errors.Is(err, assstyle.ErrNotFound) {
		return nil, invalidInput("style：找不到這個樣式。")
	}
	if err != nil {
		return nil, rt.mapStyleErr(ctx, err)
	}
	style := st.ASS()
	if b.Patch != nil {
		style = b.Patch.Apply(style)
		if errs := assstyle.Validate(style); len(errs) > 0 {
			return nil, rt.mapStyleErr(ctx, &assstyle.InvalidError{Fields: errs})
		}
	}
	var atMS int64
	if b.At != "" {
		if atMS, err = parseTime(b.At); err != nil {
			return nil, invalidInput("at：不是正確的時間。")
		}
	}
	select {
	case previewSlots <- struct{}{}:
		defer func() { <-previewSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	png, err := rt.d.Pipeline.PreviewFrame(ctx, pipeline.PreviewRequest{Item: b.Item, Style: style, AtMS: atMS, Lang: b.Lang})
	if err != nil {
		return nil, rt.mapPreviewErr(ctx, err)
	}
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", "image/png")
		hc.SetHeader("Content-Disposition", `inline; filename="preview.png"`)
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetHeader("Cache-Control", "no-store")
		hc.SetStatus(http.StatusOK)
		if _, err := hc.BodyWriter().Write(png); err != nil {
			rt.d.Log.WarnContext(hc.Context(), "輸出預覽圖時中斷", "error", err.Error())
		}
	}}, nil
}

// mapPreviewErr 對應預覽的錯誤：item 欄位指定的影音不存在是輸入錯誤 422；ffmpeg 失敗已由 pipeline 包成 *UpstreamError（502），其餘交給 mapErr。
func (rt *routes) mapPreviewErr(ctx context.Context, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return invalidInput("item：找不到這部影音。")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return rt.mapErr(ctx, err)
}
