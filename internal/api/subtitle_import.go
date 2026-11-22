package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 這個檔案是匯入字幕的路由（OUT-R13～R15）。scope 用 ingest：匯入會寫入影音的產出，不是系統設定。

func (rt *routes) registerSubtitleImport(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-subtitles-import", Method: http.MethodPost, Path: "/api/items/{id}/subtitles/import",
		Summary: "把外部編輯過的 SRT、VTT 或 ASS 匯入成新的 edited 版本（executor: human）；既有的譯文標示過時，不自動重跑下游（OUT-R13、R14、R15）",
		Description: "可以用 path（伺服器上的字幕檔）或 content（檔案內容）提供字幕。檔案無法解析、時間不單調時回 422，line 是第幾句字幕（從 1 起算），" +
			"file_line 是檔案中的行號；這時不寫入任何東西。",
		DefaultStatus: http.StatusCreated, MaxBodyBytes: importBodyLimit,
	}, auth.ScopeIngest, rt.importSubtitles)
}

type importSubtitlesInput struct {
	ID   string `path:"id" doc:"影音 ID"`
	Body struct {
		Path    string `json:"path,omitempty" doc:"伺服器上字幕檔（.srt、.vtt、.ass、.ssa）的絕對路徑；與 content 擇一"`
		Content string `json:"content,omitempty" doc:"字幕檔的內容；與 path 擇一"`
		Lang    string `json:"lang,omitempty" doc:"字幕的語言；沒有指定時用影音的原文語言"`
		Format  string `json:"format,omitempty" enum:"srt,vtt,ass" doc:"檔案格式；沒有指定時依副檔名或內容判斷"`
		Name    string `json:"name,omitempty" doc:"來源檔名，只取檔名部分寫進來源紀錄；用 path 時預設是檔名"`
	}
}

// ImportedSubtitle 是匯入的結果。
type ImportedSubtitle struct {
	Artifact string `json:"artifact" doc:"新的 edited 產出的 ID"`
	Variant  string `json:"variant" doc:"一律是 edited"`
	Lang     string `json:"lang"`
	Format   string `json:"format" doc:"判斷出的檔案格式：srt、vtt 或 ass"`
	Cues     int    `json:"cues" doc:"匯入的字幕句數"`
	Stale    bool   `json:"stale_translation" doc:"既有的譯文是否已標示「字幕已更新，譯文可能過時」"`
}

type importSubtitlesOutput struct{ Body ImportedSubtitle }

var importSubtitleExts = []string{".srt", ".vtt", ".ass", ".ssa"}

func (rt *routes) importSubtitles(ctx context.Context, in *importSubtitlesInput) (*importSubtitlesOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	b := in.Body
	data, name, ierr := readImportInput(b.Path, b.Content, pipeline.MaxImportBytes, importSubtitleExts)
	if ierr != nil {
		return nil, ierr
	}
	if b.Name != "" {
		name = b.Name
	}
	format := b.Format
	if format == "" && b.Path != "" {
		format = subtitle.ImportFormatFromName(b.Path)
	}
	res, err := rt.d.Pipeline.ImportSubtitle(ctx, pipeline.ImportRequest{ItemID: in.ID, Data: data, Format: format, Lang: b.Lang, Name: name})
	if err != nil {
		var pe *subtitle.ParseError
		if errors.As(err, &pe) {
			return nil, parseFailure(pe)
		}
		return nil, rt.mapErr(ctx, err)
	}
	return &importSubtitlesOutput{Body: ImportedSubtitle{
		Artifact: res.Artifact.ID, Variant: res.Artifact.Variant, Lang: res.Artifact.Lang,
		Format: res.Format, Cues: res.Cues, Stale: res.Flagged,
	}}, nil
}

// parseFailure 把解析錯誤變成 422：指出是第幾句（line）與檔案的行號（file_line）。
// 檔案是不可信的輸入，訊息只用固定的文字，不回顯檔案的內容。
func parseFailure(pe *subtitle.ParseError) *Error {
	var msg string
	switch pe.Msg {
	case subtitle.ErrNoCues.Error():
		msg = "檔案裡沒有任何字幕。"
	case "end is earlier than start":
		msg = fmt.Sprintf("第 %d 句的結束時間早於開始時間。", pe.Cue)
	case "start is earlier than the previous cue":
		msg = fmt.Sprintf("第 %d 句的開始時間早於前一句，時間必須由前到後排列。", pe.Cue)
	case "unsupported format":
		msg = "無法判斷檔案格式，請指定 format（srt、vtt 或 ass）。"
	case "line too long":
		msg = "檔案中有過長的一行。"
	default:
		if pe.Cue > 0 {
			msg = fmt.Sprintf("第 %d 句的格式或時間不正確。", pe.Cue)
		} else {
			msg = "字幕檔的格式不正確。"
		}
	}
	e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg)
	if pe.Cue > 0 {
		cue := pe.Cue
		e.Line = &cue
	}
	if pe.Line > 0 {
		fl := pe.Line
		e.FileLine = &fl
	}
	return e
}
