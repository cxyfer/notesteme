package hubmcp

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// get_transcript 的格式（HMCP-R3）；cues-json 是預設值。
const formatCuesJSON = subtitle.FormatCuesJSON

var transcriptFormats = []string{subtitle.FormatCuesJSON, subtitle.FormatSRT, subtitle.FormatVTT, subtitle.FormatTXT}

type transcriptArgs struct {
	Version string `json:"version" jsonschema:"字幕版本的 ID"`
	Format  string `json:"format,omitempty" jsonschema:"cues-json（預設）、srt、vtt 或 txt"`
	From    string `json:"from,omitempty" jsonschema:"起點時間碼（HH:MM:SS.mmm）；只回傳開始時間不早於它的字幕。續取時填上一次的 next_from"`
	To      string `json:"to,omitempty" jsonschema:"終點時間碼（HH:MM:SS.mmm，不含）；只回傳開始時間早於它的字幕"`
}

// transcriptRequest 是檢查過的 get_transcript 參數。toMS < 0 代表沒有上限。
type transcriptRequest struct {
	format       string
	fromMS, toMS int64
}

func parseTranscriptArgs(a transcriptArgs) (transcriptRequest, *mcp.CallToolResult) {
	req := transcriptRequest{format: a.Format, toMS: -1}
	if req.format == "" {
		req.format = formatCuesJSON
	}
	if !slices.Contains(transcriptFormats, req.format) {
		return req, invalid("format 必須是 cues-json、srt、vtt 或 txt。")
	}
	var err error
	if a.From != "" {
		if req.fromMS, err = subtitle.ParseTimecode(a.From); err != nil {
			return req, invalid("from 不是合法的時間碼（HH:MM:SS.mmm）。")
		}
	}
	if a.To != "" {
		if req.toMS, err = subtitle.ParseTimecode(a.To); err != nil {
			return req, invalid("to 不是合法的時間碼（HH:MM:SS.mmm）。")
		}
		if req.toMS <= req.fromMS {
			return req, invalid("to 必須晚於 from。")
		}
	}
	return req, nil
}

// CueView 是 get_transcript 回傳的一句字幕（HMCP-R6）：index 是版本中原本的編號，時間是 HH:MM:SS.mmm。
type CueView struct {
	Index int    `json:"index"`
	Start string `json:"start"`
	End   string `json:"end"`
	Text  string `json:"text"`
}

// TranscriptResult 是 get_transcript 的結果：cues-json 時是 cues，其他格式時是渲染後的 text（HMCP-R6、R7）。
type TranscriptResult struct {
	Version   string    `json:"version"`
	Variant   string    `json:"variant"`
	Lang      string    `json:"lang"`
	Format    string    `json:"format"`
	Cues      []CueView `json:"cues,omitempty"`
	Text      *string   `json:"text,omitempty"`
	Truncated bool      `json:"truncated"`
	NextFrom  string    `json:"next_from,omitempty"`
}

func cueView(c subtitle.Cue) CueView {
	return CueView{Index: c.Index, Start: subtitle.FormatDisplayTime(c.StartMS), End: subtitle.FormatDisplayTime(c.EndMS), Text: c.Text}
}

// buildTranscript 解碼 cues JSON，依開始時間篩選 [from, to)，並截到 limit 位元組以內（HMCP-R7）。
//
// 篩選看開始時間（不是和範圍重疊）：上傳的 cues 可能重疊或沒有排序，以重疊判斷時用 next_from 續取會重複回傳同一句，
// 一頁只放得下一句時永遠不會前進。cue 依開始時間穩定排序；一頁至少有一句（單句超過上限時仍回傳它），
// 而且同一個開始時間的句子不會被拆到兩頁，所以 next_from 一定比這一頁所有句子的開始時間晚，續取一定會前進。
// limit 量的是 cues 陣列的 JSON 或渲染後的文字，不含其他欄位。
func buildTranscript(r io.Reader, version string, req transcriptRequest, limit int64) (TranscriptResult, error) {
	t, err := subtitle.DecodeCuesJSON(r)
	if err != nil {
		return TranscriptResult{}, err
	}
	cues := make([]subtitle.Cue, 0, len(t.Cues))
	for _, c := range t.Cues {
		if c.StartMS >= req.fromMS && (req.toMS < 0 || c.StartMS < req.toMS) {
			cues = append(cues, c)
		}
	}
	slices.SortStableFunc(cues, func(a, b subtitle.Cue) int {
		switch {
		case a.StartMS < b.StartMS:
			return -1
		case a.StartMS > b.StartMS:
			return 1
		}
		return 0
	})
	n, err := fitCues(cues, req.format, limit)
	if err != nil {
		return TranscriptResult{}, err
	}
	out := TranscriptResult{Version: version, Variant: string(t.Variant), Lang: t.Lang, Format: req.format}
	if n < len(cues) {
		out.Truncated = true
		out.NextFrom = subtitle.FormatDisplayTime(cues[n].StartMS)
	}
	page := cues[:n]
	if req.format == formatCuesJSON {
		out.Cues = make([]CueView, 0, len(page))
		for _, c := range page {
			out.Cues = append(out.Cues, cueView(c))
		}
		return out, nil
	}
	var buf bytes.Buffer
	if err := subtitle.Render(&buf, req.format, subtitle.Transcript{Lang: t.Lang, Variant: t.Variant, Cues: page}); err != nil {
		return TranscriptResult{}, err
	}
	text := buf.String()
	out.Text = &text
	return out, nil
}

// fitCues 回傳放得進 limit 的前幾句（至少一句；同一個開始時間的句子一起放）。cues 已依開始時間排序。
func fitCues(cues []subtitle.Cue, format string, limit int64) (int, error) {
	size, err := baseSize(format)
	if err != nil {
		return 0, err
	}
	n := 0
	for i, c := range cues {
		piece, err := cueSize(c, format, i)
		if err != nil {
			return 0, err
		}
		sameStart := i > 0 && c.StartMS == cues[i-1].StartMS
		if i > 0 && !sameStart && size+piece > limit {
			break
		}
		size += piece
		n = i + 1
	}
	return n, nil
}

// baseSize 是沒有任何字幕時的大小：cues 陣列的括號、VTT 的檔頭。
func baseSize(format string) (int64, error) {
	switch format {
	case formatCuesJSON:
		return 2, nil
	case subtitle.FormatVTT:
		var b bytes.Buffer
		err := subtitle.RenderVTT(&b, nil)
		return int64(b.Len()), err
	}
	return 0, nil
}

// cueSize 是第 i 句（從 0 起算）加進結果後增加的位元組數，與最後的渲染一致（測試鎖住）。
func cueSize(c subtitle.Cue, format string, i int) (int64, error) {
	var b bytes.Buffer
	switch format {
	case formatCuesJSON:
		raw, err := json.Marshal(cueView(c))
		if i > 0 {
			return int64(len(raw)) + 1, err // 逗號
		}
		return int64(len(raw)), err
	case subtitle.FormatSRT:
		// SRT 的編號依輸出順序：單獨渲染時是 1，換成第 i+1 句的位數。
		err := subtitle.RenderSRT(&b, []subtitle.Cue{c})
		return int64(b.Len() - 1 + len(strconv.Itoa(i+1))), err
	case subtitle.FormatVTT:
		err := subtitle.RenderVTT(&b, []subtitle.Cue{c})
		base, _ := baseSize(format)
		return int64(b.Len()) - base, err
	default:
		err := subtitle.RenderTXT(&b, []subtitle.Cue{c})
		return int64(b.Len()), err
	}
}
