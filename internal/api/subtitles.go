package api

import (
	"context"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// m2Formats 是 OUT-R1 中屬於 M2 的格式；回應 501 而不是 422。
var m2Formats = []string{"ass", "bilingual-srt"}

type subtitlesInput struct {
	ID      string `path:"id" doc:"影音 ID"`
	Format  string `query:"format" doc:"格式：srt（預設）、vtt、txt、cues-json"`
	Variant string `query:"variant" doc:"字幕變體；沒指定時選處理得最完整的（OUT-R2）"`
	Lang    string `query:"lang" doc:"語言；沒指定時使用影音的原文語言"`
	From    string `query:"from" doc:"起點，HH:MM:SS(.mmm) 或秒數（OUT-R3）"`
	To      string `query:"to" doc:"終點，格式同 from"`
}

// getSubtitles 下載字幕（OUT-R1～R4）：從 cues 即時渲染，不建立任何產出，輸出以串流寫入回應（NFR-RES-3）。
// 錯誤都在開始輸出之前決定，所以失敗時仍然是正常的錯誤回應。
func (rt *routes) getSubtitles(ctx context.Context, in *subtitlesInput) (*huma.StreamResponse, error) {
	if rt.d.Pipeline == nil {
		return nil, newError(http.StatusNotFound, CodeNotFound, "找不到這部影音。")
	}
	format := in.Format
	if format == "" {
		format = subtitle.FormatSRT
	}
	if slices.Contains(m2Formats, format) {
		return nil, newError(http.StatusNotImplemented, CodeNotImplemented, "這個格式尚未實作："+format+"。")
	}
	if subtitle.FileExt(format) == "" {
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, "format 必須是 srt、vtt、txt 或 cues-json。")
	}
	q, err := cuesQuery(in.Variant, in.Lang, in.From, in.To)
	if err != nil {
		return nil, err
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
