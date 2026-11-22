package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/assstyle"
	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

type subtitlesInput struct {
	ID      string `path:"id" doc:"影音 ID"`
	Format  string `query:"format" doc:"格式：srt（預設）、vtt、txt、cues-json、ass、bilingual-srt"`
	Variant string `query:"variant" doc:"字幕變體；沒指定時選處理得最完整的（OUT-R2）"`
	Lang    string `query:"lang" doc:"語言；沒指定時使用影音的原文語言（ass 與 bilingual-srt 沒指定時用最近產生的譯文的語言）"`
	Style   string `query:"style" doc:"ASS 樣式名稱，只對 format=ass 有效，預設 default（OUT-R6）；找不到回 422"`
	From    string `query:"from" doc:"起點，HH:MM:SS(.mmm) 或秒數（OUT-R3）"`
	To      string `query:"to" doc:"終點，格式同 from"`
}

// getSubtitles 下載字幕（OUT-R1～R4）：從 cues 即時渲染，不建立任何產出，輸出以串流寫入回應（NFR-RES-3）。
// 錯誤都在開始輸出之前決定，所以失敗時仍然是正常的錯誤回應。
func (rt *routes) getSubtitles(ctx context.Context, in *subtitlesInput) (*huma.StreamResponse, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	format := in.Format
	if format == "" {
		format = subtitle.FormatSRT
	}
	if subtitle.FileExt(format) == "" {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "format 必須是 srt、vtt、txt、cues-json、ass 或 bilingual-srt。")
	}
	q, err := cuesQuery(in.Variant, in.Lang, in.From, in.To)
	if err != nil {
		return nil, err
	}
	if in.ID, err = rt.itemID(ctx, in.ID); err != nil {
		return nil, err
	}
	if format == subtitle.FormatASS || format == subtitle.FormatBilingualSRT {
		return rt.bilingualSubtitles(ctx, in, format, q)
	}
	it, tr, err := rt.d.Pipeline.CuesWithItem(ctx, in.ID, q)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	name := subtitle.DownloadFileName(it.Title, tr.Lang, subtitle.FileExt(format))
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", subtitle.ContentType(format))
		hc.SetHeader("Content-Disposition", subtitle.ContentDisposition(name))
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetStatus(http.StatusOK)
		if err := subtitle.Render(hc.BodyWriter(), format, tr); err != nil {
			// 標頭已經送出，只能記錄；連線會因內容不完整而中斷。
			rt.d.Log.WarnContext(hc.Context(), "輸出字幕時中斷", "error", err.Error())
		}
	}}, nil
}

// bilingualSubtitles 輸出 ASS 與雙語 SRT（OUT-R5）。原文與目標語言相同（包括 convert 只差字形）時一律輸出單語，
// 這時回應標頭 X-Notesteme-Bilingual 是 false。
func (rt *routes) bilingualSubtitles(ctx context.Context, in *subtitlesInput, format string, q pipeline.CuesQuery) (*huma.StreamResponse, error) {
	var style subtitle.ASSStyle
	if format == subtitle.FormatASS {
		if rt.d.AssStyles == nil {
			return nil, rt.noAssStyles(ctx)
		}
		name := in.Style
		if name == "" {
			name = "default"
		}
		st, err := rt.d.AssStyles.Get(ctx, name)
		if errors.Is(err, assstyle.ErrNotFound) {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "style：找不到這個樣式。")
		}
		if err != nil {
			return nil, rt.mapStyleErr(ctx, err)
		}
		style = st.ASS()
	}
	ri, err := rt.d.Pipeline.RenderInput(ctx, in.ID, q)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	var orig []subtitle.Cue
	origLang := ri.Main.Lang
	if ri.Orig != nil {
		orig, origLang = ri.Orig.Cues, ri.Orig.Lang
	}
	pairs := subtitle.PairCues(ri.Main.Cues, orig)
	bilingual := subtitle.Bilingual(pairs)
	style = assstyle.ResolveFonts(style, ri.Main.Lang, origLang)
	name := subtitle.DownloadFileName(ri.Item.Title, ri.Main.Lang, subtitle.FileExt(format))
	return &huma.StreamResponse{Body: func(hc huma.Context) {
		hc.SetHeader("Content-Type", subtitle.ContentType(format))
		hc.SetHeader("Content-Disposition", subtitle.ContentDisposition(name))
		hc.SetHeader("X-Content-Type-Options", "nosniff")
		hc.SetHeader("X-Notesteme-Bilingual", strconv.FormatBool(bilingual))
		hc.SetStatus(http.StatusOK)
		var w io.Writer = hc.BodyWriter()
		var err error
		if format == subtitle.FormatASS {
			err = subtitle.RenderASS(w, pairs, style, ri.Item.Title)
		} else {
			err = subtitle.RenderBilingualSRT(w, pairs)
		}
		if err != nil {
			rt.d.Log.WarnContext(hc.Context(), "輸出字幕時中斷", "error", err.Error())
		}
	}}, nil
}

// cuesQuery 組出取得字幕的條件；from、to 格式不正確時回傳 422。
func cuesQuery(variant, lang, from, to string) (pipeline.CuesQuery, error) {
	q := pipeline.CuesQuery{Variant: variant, Lang: lang, ToMS: -1}
	var err error
	if from != "" {
		if q.FromMS, err = parseTime(from); err != nil {
			return q, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "from 不是正確的時間。")
		}
	}
	if to != "" {
		if q.ToMS, err = parseTime(to); err != nil {
			return q, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "to 不是正確的時間。")
		}
	}
	return q, nil
}
