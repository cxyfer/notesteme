package subtitle

import (
	"errors"
	"sort"
	"strings"
)

// 匯入的字幕格式（OUT-R13）。
const (
	ImportSRT = "srt"
	ImportVTT = "vtt"
	ImportASS = "ass"
)

// DetectImportFormat 從內容判斷格式：WEBVTT 開頭是 vtt；有 [Script Info]、[V4+ Styles] 或 [Events] 段落是 ass；其餘當成 srt。
func DetectImportFormat(data []byte) string {
	s := strings.TrimPrefix(string(data), "\uFEFF")
	if strings.HasPrefix(strings.TrimLeft(s, " \t\r\n"), "WEBVTT") {
		return ImportVTT
	}
	head := s
	if len(head) > 4096 {
		head = head[:4096]
	}
	low := strings.ToLower(head)
	for _, marker := range []string{"[script info]", "[v4+ styles]", "[v4 styles]", "[events]"} {
		if strings.Contains(low, marker) {
			return ImportASS
		}
	}
	return ImportSRT
}

// ImportFormatFromName 從副檔名判斷格式；不認得回傳空字串。
func ImportFormatFromName(name string) string {
	low := strings.ToLower(name)
	switch {
	case strings.HasSuffix(low, ".srt"):
		return ImportSRT
	case strings.HasSuffix(low, ".vtt"):
		return ImportVTT
	case strings.HasSuffix(low, ".ass"), strings.HasSuffix(low, ".ssa"):
		return ImportASS
	}
	return ""
}

// ErrNoCues 表示匯入的檔案沒有任何字幕句。
var ErrNoCues = errors.New("no cues")

// ParseImport 解析匯入的外部字幕（SRT、VTT、ASS）並檢查時間（OUT-R13、R15）：
// 每句的結束不早於開始，開始時間不能比前一句早（時間不單調）。format 為空時從內容判斷。
// 錯誤是 *ParseError：Cue 是第幾句（從 1 起算），Line 是檔案中的行號（不一定知道）。
// 沒有任何字幕句時回傳 *ParseError 包住 ErrNoCues。回傳的 cue 編號從 1 起算，時間來源是 source，沒有文字的 cue 會被拿掉。
func ParseImport(data []byte, format string) (cues []Cue, used string, err error) {
	if format == "" {
		format = DetectImportFormat(data)
	}
	switch format {
	case ImportSRT:
		cues, err = ParseSRT(data)
	case ImportVTT:
		cues, err = ParseVTT(data)
	case ImportASS:
		var f *ASSFile
		if f, err = ParseASS(data); err == nil {
			cues = f.Cues()
		}
	default:
		return nil, format, &ParseError{Msg: "unsupported format"}
	}
	if err != nil {
		return nil, format, err
	}
	if len(cues) == 0 {
		return nil, format, &ParseError{Msg: ErrNoCues.Error()}
	}
	var prev int64
	for i, c := range cues {
		if c.EndMS < c.StartMS {
			return nil, format, &ParseError{Cue: i + 1, Msg: "end is earlier than start"}
		}
		if c.StartMS < prev {
			return nil, format, &ParseError{Cue: i + 1, Msg: "start is earlier than the previous cue"}
		}
		prev = c.StartMS
	}
	out := make([]Cue, 0, len(cues))
	for _, c := range cues {
		if strings.TrimSpace(c.Text) == "" {
			continue
		}
		c.Index = len(out) + 1
		c.Timing = TimingSource
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, format, &ParseError{Msg: ErrNoCues.Error()}
	}
	return out, format, nil
}

// Cues 把 ASS 的 Dialogue 轉成 cue，依開始時間排序（ASS 不要求事件依時間排列；同一時間維持檔案中的順序）。
func (f *ASSFile) Cues() []Cue {
	events := append([]ASSEvent(nil), f.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].StartMS < events[j].StartMS })
	cues := make([]Cue, len(events))
	for i, e := range events {
		cues[i] = Cue{Index: i + 1, StartMS: e.StartMS, EndMS: e.EndMS, Text: e.Text, Timing: TimingSource}
	}
	return cues
}
