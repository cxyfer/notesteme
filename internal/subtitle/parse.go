package subtitle

import (
	"html"
	"regexp"
	"strings"
)

var (
	// 字詞時間標記，例如 <00:00:01.000>。
	vttTimestampTag = regexp.MustCompile(`<((?:\d{1,4}:)?\d{1,2}:\d{2}[.,]\d{1,3})>`)
	// YouTube 自動字幕的 <c> 與 <c.colorXXXXXX> 標記。
	vttClassTag = regexp.MustCompile(`</?c(?:\.[^>\s]*)*>`)
)

// block 為以空行分隔的一段文字，lineNo 為第一行在檔案中的行號。
type block struct {
	lines  []string
	lineNo int
}

// splitBlocks 去除 BOM、統一換行並以空行切成區塊。
// strictBlank 為 true（VTT）時只有真正的空行（長度為 0）才是分隔；只含空白的行屬於 cue 內容
// （yt-dlp 的 YouTube 自動字幕常以單一空白當 cue 的第一行）。
// 為 false（SRT）時維持原行為，只含空白的行也視為分隔。
func splitBlocks(data []byte, strictBlank bool) []block {
	s := strings.TrimPrefix(string(data), "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var blocks []block
	var cur block
	for i, line := range strings.Split(s, "\n") {
		if line == "" || (!strictBlank && strings.TrimSpace(line) == "") {
			if len(cur.lines) > 0 {
				blocks = append(blocks, cur)
				cur = block{}
			}
			continue
		}
		if len(cur.lines) == 0 {
			cur.lineNo = i + 1
		}
		cur.lines = append(cur.lines, line)
	}
	if len(cur.lines) > 0 {
		blocks = append(blocks, cur)
	}
	return blocks
}

// parseTimingLine 解析 `start --> end [設定]`。
func parseTimingLine(line string) (start, end int64, err error) {
	i := strings.Index(line, "-->")
	if i < 0 {
		return 0, 0, &ParseError{Msg: "missing -->"}
	}
	left := strings.Fields(line[:i])
	right := strings.Fields(line[i+3:])
	if len(left) != 1 || len(right) == 0 {
		return 0, 0, &ParseError{Msg: "invalid timing line"}
	}
	if start, err = ParseTimecode(left[0]); err != nil {
		return 0, 0, &ParseError{Msg: err.Error()}
	}
	if end, err = ParseTimecode(right[0]); err != nil {
		return 0, 0, &ParseError{Msg: err.Error()}
	}
	if end < start {
		return 0, 0, &ParseError{Msg: "end is earlier than start"}
	}
	return start, end, nil
}

func isVTTNonCueBlock(first string) bool {
	for _, p := range []string{"NOTE", "STYLE", "REGION"} {
		if first == p || strings.HasPrefix(first, p+" ") || strings.HasPrefix(first, p+"\t") {
			return true
		}
	}
	return false
}

// ParseVTT 解析 WebVTT 字幕。
// 處理 header、NOTE／STYLE／REGION 區塊、cue 識別碼與設定（align、position），
// 並把 YouTube 自動字幕的 <00:00:01.000><c> 字詞時間標記解析成 Words。
// 沒有 --> 的區塊（例如 header 的中繼資料）會被忽略；時間行不合法時回傳 *ParseError。
func ParseVTT(data []byte) ([]Cue, error) {
	var cues []Cue
	for bi, b := range splitBlocks(data, true) {
		lines := b.lines
		if bi == 0 && strings.HasPrefix(lines[0], "WEBVTT") {
			lines = lines[1:]
		}
		if len(lines) == 0 || isVTTNonCueBlock(lines[0]) {
			continue
		}
		arrow := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				arrow = i
				break
			}
		}
		if arrow < 0 {
			continue
		}
		lineNo := b.lineNo + arrow
		if bi == 0 && len(lines) < len(b.lines) {
			lineNo++
		}
		n := len(cues) + 1
		start, end, err := parseTimingLine(lines[arrow])
		if err != nil {
			pe := err.(*ParseError)
			pe.Cue, pe.Line = n, lineNo
			return nil, pe
		}
		cue := Cue{Index: n, StartMS: start, EndMS: end, Timing: TimingSource}
		var texts []string
		for _, l := range lines[arrow+1:] {
			text, words := parseVTTTextLine(l, start, end)
			cue.Words = append(cue.Words, words...)
			if text != "" {
				texts = append(texts, text)
			}
		}
		cue.Text = strings.Join(texts, "\n")
		cues = append(cues, cue)
	}
	return cues, nil
}

// parseVTTTextLine 移除字詞時間標記與 <c> 標記、解碼 HTML 實體，
// 並在有時間標記時取出字詞時間。其他標記（<i>、<v>）留給正規化處理。
func parseVTTTextLine(line string, cueStart, cueEnd int64) (string, []Word) {
	idx := vttTimestampTag.FindAllStringSubmatchIndex(line, -1)
	if len(idx) == 0 {
		return cleanVTTText(line), nil
	}
	type seg struct {
		start int64
		text  string
	}
	var segs []seg
	prev := 0
	cur := cueStart
	for _, m := range idx {
		segs = append(segs, seg{cur, line[prev:m[0]]})
		ts, err := ParseTimecode(line[m[2]:m[3]])
		if err != nil {
			ts = cur
		}
		if ts < cur {
			ts = cur
		}
		if ts > cueEnd {
			ts = cueEnd
		}
		cur = ts
		prev = m[1]
	}
	segs = append(segs, seg{cur, line[prev:]})

	var whole strings.Builder
	var words []Word
	for i, sg := range segs {
		raw := html.UnescapeString(vttClassTag.ReplaceAllString(sg.text, ""))
		whole.WriteString(raw)
		w := strings.TrimSpace(raw)
		if w == "" {
			continue
		}
		end := cueEnd
		if i+1 < len(segs) {
			end = segs[i+1].start
		}
		words = append(words, Word{Text: strings.Join(strings.Fields(w), " "), StartMS: sg.start, EndMS: end})
	}
	return strings.Join(strings.Fields(whole.String()), " "), words
}

func cleanVTTText(line string) string {
	return strings.TrimSpace(html.UnescapeString(vttClassTag.ReplaceAllString(line, "")))
}

// ParseSRT 解析 SRT 字幕。識別碼行可省略；時間行或區塊不合法時回傳 *ParseError。
func ParseSRT(data []byte) ([]Cue, error) {
	var cues []Cue
	for _, b := range splitBlocks(data, false) {
		n := len(cues) + 1
		arrow := -1
		for i, l := range b.lines {
			if strings.Contains(l, "-->") {
				arrow = i
				break
			}
		}
		if arrow < 0 {
			return nil, &ParseError{Cue: n, Line: b.lineNo, Msg: "missing timing line"}
		}
		if arrow > 1 {
			return nil, &ParseError{Cue: n, Line: b.lineNo, Msg: "unexpected text before timing line"}
		}
		start, end, err := parseTimingLine(b.lines[arrow])
		if err != nil {
			pe := err.(*ParseError)
			pe.Cue, pe.Line = n, b.lineNo+arrow
			return nil, pe
		}
		var texts []string
		for _, l := range b.lines[arrow+1:] {
			if t := strings.TrimSpace(l); t != "" {
				texts = append(texts, t)
			}
		}
		cues = append(cues, Cue{
			Index: n, StartMS: start, EndMS: end,
			Text: strings.Join(texts, "\n"), Timing: TimingSource,
		})
	}
	return cues, nil
}
