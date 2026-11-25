package subtitle

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// M2 新增的輸出格式（OUT-R1）。
const (
	FormatASS          = "ass"
	FormatBilingualSRT = "bilingual-srt"
)

// ASSLine 是一個文字層（原文行或譯文行）的樣式。Font 為空代表「依語言的文字系統自動選字型」，
// 由呼叫端在渲染前補上（見 assstyle.ResolveFonts）；渲染時仍為空就用 Noto Sans。
// 顏色是 #RRGGBB 或 #RRGGBBAA（AA 是不透明度，FF 完全不透明，與 CSS 相同；ASS 的 alpha 相反，渲染時轉換）。
type ASSLine struct {
	Font         string  `json:"font"`
	Size         int     `json:"size"`
	Color        string  `json:"color"`
	OutlineColor string  `json:"outline_color"`
	BackColor    string  `json:"back_color"`
	Outline      float64 `json:"outline"`
	Shadow       float64 `json:"shadow"`
	Bold         bool    `json:"bold"`
}

// 版面的列舉值。
const (
	ShowBoth        = "both"
	ShowTranslation = "translation"
	ShowPrimary     = "primary"

	OrderTranslationFirst = "translation-first"
	OrderPrimaryFirst     = "primary-first"

	AtStacked = "stacked"
	AtTop     = "top"
)

// ASSLayout 是兩行字的版面。Alignment 是數字鍵盤的位置（1～9，2 是底部置中）；
// TranslationAt 為 top 時，譯文行放在畫面上方（同一欄的第一排），原文行留在 Alignment 指定的位置。
// BorderStyle 為 1 是描邊加陰影，3 是不透明底框（底框顏色取自 OutlineColor）。
type ASSLayout struct {
	Show          string `json:"show"`
	Order         string `json:"order"`
	TranslationAt string `json:"translation_at"`
	Alignment     int    `json:"alignment"`
	MarginV       int    `json:"margin_v"`
	BorderStyle   int    `json:"border_style"`
}

// ASSStyle 是完整的 ASS 樣式：原文行（Primary）、譯文行（Translation）與版面。
type ASSStyle struct {
	Primary     ASSLine   `json:"primary"`
	Translation ASSLine   `json:"translation"`
	Layout      ASSLayout `json:"layout"`
}

// ASS 畫布大小；樣式的大小與邊距都以這個解析度為準（ScaledBorderAndShadow 讓它隨實際畫面縮放）。
const (
	ASSPlayResX = 1920
	ASSPlayResY = 1080
)

// Pair 是一句字幕：Main 是主要文字（雙語時是譯文），Orig 是原文，單語時為空。
type Pair struct {
	StartMS, EndMS int64
	Main, Orig     string
}

// PairCues 把譯文 cue 與原文 cue 依時間配對：每句譯文取重疊最久的原文（平手取較早的）；
// 沒有任何重疊的譯文維持單語。orig 為 nil 或空時，全部是單語。
func PairCues(main, orig []Cue) []Pair {
	out := make([]Pair, len(main))
	for i, m := range main {
		out[i] = Pair{StartMS: m.StartMS, EndMS: m.EndMS, Main: m.Text}
		best, bestOverlap := -1, int64(0)
		for j, o := range orig {
			ov := min(m.EndMS, o.EndMS) - max(m.StartMS, o.StartMS)
			if ov > bestOverlap {
				best, bestOverlap = j, ov
			}
		}
		if best >= 0 {
			out[i].Orig = orig[best].Text
		}
	}
	return out
}

// Bilingual 回報是否至少有一句帶有原文。
func Bilingual(pairs []Pair) bool {
	for _, p := range pairs {
		if p.Orig != "" {
			return true
		}
	}
	return false
}

// CueIter 逐句提供 cue；ok 為 false 代表沒有更多句子。
type CueIter func() (c Cue, ok bool, err error)

// ErrUnsorted 表示 PairStream 的輸入沒有依 StartMS 非遞減排列；呼叫端改用 PairCues（整份載入）。
var ErrUnsorted = errors.New("subtitle: cues 沒有依開始時間排序，不能串流配對")

// PairStream 逐句配對 main 與 orig，依序交給 fn，結果與 PairCues 相同（NFR-RES-3：不把整份字幕載入記憶體）。
// main 與 orig 都必須依 StartMS 非遞減排列，否則回傳 ErrUnsorted（可能已經呼叫過 fn）；orig 為 nil 時全部是單語。
// 回傳之前會讀完 orig，所以回傳 nil 代表兩邊都完整讀過、都有排序。
//
// 只需要保留可能與目前或之後的 main 句重疊的原文句：main 的開始時間不會變小，所以結束時間不晚於目前 main 開始時間的
// 原文句之後都不可能重疊（重疊量 ≤ 0，PairCues 只取重疊量 > 0 的）；orig 依開始時間排序，所以開始時間不早於
// main 結束時間的原文句（以及之後的）還不必讀。視窗依讀入的順序保存，平手時取較早的，與 PairCues 相同。
func PairStream(main, orig CueIter, fn func(Pair) error) error {
	var (
		win                []Cue
		next               Cue
		hasNext            bool
		lastOrig, lastMain int64
		started            bool
	)
	pull := func() error {
		hasNext = false
		if orig == nil {
			return nil
		}
		c, ok, err := orig()
		if err != nil || !ok {
			return err
		}
		if started && c.StartMS < lastOrig {
			return ErrUnsorted
		}
		started, lastOrig = true, c.StartMS
		next, hasNext = c, true
		return nil
	}
	if err := pull(); err != nil {
		return err
	}
	for first := true; ; first = false {
		m, ok, err := main()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if !first && m.StartMS < lastMain {
			return ErrUnsorted
		}
		lastMain = m.StartMS
		keep := win[:0]
		for _, o := range win {
			if o.EndMS > m.StartMS {
				keep = append(keep, o)
			}
		}
		win = keep
		for hasNext && next.StartMS < m.EndMS {
			if next.EndMS > m.StartMS {
				win = append(win, next)
			}
			if err := pull(); err != nil {
				return err
			}
		}
		p := Pair{StartMS: m.StartMS, EndMS: m.EndMS, Main: m.Text}
		best, bestOverlap := -1, int64(0)
		for j, o := range win {
			if ov := min(m.EndMS, o.EndMS) - max(m.StartMS, o.StartMS); ov > bestOverlap {
				best, bestOverlap = j, ov
			}
		}
		if best >= 0 {
			p.Orig = win[best].Text
		}
		if err := fn(p); err != nil {
			return err
		}
	}
	for hasNext {
		if err := pull(); err != nil {
			return err
		}
	}
	return nil
}

// SliceIter 把一份 cue 陣列包成 CueIter。
func SliceIter(cues []Cue) CueIter {
	i := 0
	return func() (Cue, bool, error) {
		if i >= len(cues) {
			return Cue{}, false, nil
		}
		i++
		return cues[i-1], true, nil
	}
}

// BilingualSRTWriter 逐句寫出雙語 SRT（RenderBilingualSRT 的串流版本，輸出相同）。
type BilingualSRTWriter struct {
	bw *bufio.Writer
	n  int
}

// NewBilingualSRTWriter 建立 BilingualSRTWriter。
func NewBilingualSRTWriter(w io.Writer) *BilingualSRTWriter {
	return &BilingualSRTWriter{bw: bufio.NewWriter(w)}
}

// Write 寫出一句：先是譯文，再是原文；單語的句子只有一行文字。回傳第一個寫入錯誤（client 斷線時停止）。
func (sw *BilingualSRTWriter) Write(p Pair) error {
	sw.n++
	lines := cueLines(strings.ReplaceAll(p.Main, "-->", "->"))
	if p.Orig != "" {
		lines = append(lines, cueLines(strings.ReplaceAll(p.Orig, "-->", "->"))...)
	}
	fmt.Fprintf(sw.bw, "%d\n%s --> %s\n%s\n\n", sw.n, FormatSRTTime(p.StartMS), FormatSRTTime(p.EndMS), strings.Join(lines, "\n"))
	_, err := sw.bw.Write(nil)
	return err
}

// Close 清空緩衝。
func (sw *BilingualSRTWriter) Close() error { return sw.bw.Flush() }

// RenderBilingualSRT 串流輸出雙語 SRT：每個 cue 先是譯文，再是原文；單語的 cue 只有一行文字。
func RenderBilingualSRT(w io.Writer, pairs []Pair) error {
	sw := NewBilingualSRTWriter(w)
	for _, p := range pairs {
		if err := sw.Write(p); err != nil {
			return err
		}
	}
	return sw.Close()
}

// assTime 格式化 ASS 的時間（H:MM:SS.cc）；round 為 up 時進位到下一個百分之一秒，讓 cue 不會被切短。
func assTime(ms int64, up bool) string {
	if ms < 0 {
		ms = 0
	}
	cs := ms / 10
	if up && ms%10 != 0 {
		cs++
	}
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

// assEscaper 讓 cue 文字不會被當成覆寫碼：大括號與反斜線換成外觀相近的全形字元，
// 否則 `{\an8}`、`\N` 這類內容會改變版面（cue 文字是不可信的輸入）。
var assEscaper = strings.NewReplacer("{", "｛", "}", "｝", "\\", "＼")

// assText 把多行文字轉成 ASS 的單一文字欄位：移除控制字元，換行改成 \N。
func assText(s string) string {
	lines := cueLines(strings.Map(func(r rune) rune {
		if r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s))
	for i, l := range lines {
		lines[i] = assEscaper.Replace(l)
	}
	return strings.Join(lines, `\N`)
}

// assField 清理會放進以逗號分隔的欄位（樣式名稱、字型、標題）的字串：逗號與控制字元會破壞欄位結構。
func assField(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == ',' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// assColour 把 #RRGGBB 或 #RRGGBBAA 轉成 ASS 的 &HAABBGGRR；格式不正確時用 def（同樣的格式）。
func assColour(hex, def string) string {
	rgb, alpha, ok := parseHexColour(hex)
	if !ok {
		rgb, alpha, _ = parseHexColour(def)
	}
	// ASS 的 alpha 00 是完全不透明。
	return fmt.Sprintf("&H%02X%02X%02X%02X", 255-alpha, rgb&0xFF, (rgb>>8)&0xFF, (rgb>>16)&0xFF)
}

// parseHexColour 解析 #RRGGBB 或 #RRGGBBAA；rgb 的高位是紅色。
func parseHexColour(s string) (rgb uint32, alpha uint32, ok bool) {
	if len(s) != 7 && len(s) != 9 || s[0] != '#' {
		return 0, 0, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, false
	}
	if len(s) == 7 {
		return uint32(v), 255, true
	}
	return uint32(v >> 8), uint32(v & 0xFF), true
}

// ValidColour 回報字串是不是 #RRGGBB 或 #RRGGBBAA。
func ValidColour(s string) bool {
	_, _, ok := parseHexColour(s)
	return ok
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// styleLine 組出 [V4+ Styles] 的一列。
func (l ASSLine) styleLine(name string, border, align, marginV int) string {
	font := assField(l.Font)
	if font == "" {
		font = "Noto Sans"
	}
	size := l.Size
	if size <= 0 {
		size = 48
	}
	bold := 0
	if l.Bold {
		bold = -1
	}
	return fmt.Sprintf("Style: %s,%s,%d,%s,%s,%s,%s,%d,0,0,0,100,100,0,0,%d,%s,%s,%d,60,60,%d,1",
		name, font, size,
		assColour(l.Color, "#FFFFFF"), assColour(l.Color, "#FFFFFF"),
		assColour(l.OutlineColor, "#000000"), assColour(l.BackColor, "#00000080"),
		bold, border, fmtNum(math.Max(l.Outline, 0)), fmtNum(math.Max(l.Shadow, 0)), align, marginV)
}

// ASSWriter 逐句寫出 ASS（RenderASS 的串流版本，輸出相同）：標頭與樣式只和樣式設定有關，建立時先寫出，
// 之後每句寫出事件。
type ASSWriter struct {
	bw  *bufio.Writer
	lay ASSLayout
}

// NewASSWriter 寫出 [Script Info] 與 [V4+ Styles]，以及 [Events] 的 Format 列。
// title 寫進 [Script Info]，會先清理換行與逗號。
func NewASSWriter(w io.Writer, st ASSStyle, title string) *ASSWriter {
	lay := st.Layout
	if lay.Show == "" {
		lay.Show = ShowBoth
	}
	if lay.Alignment < 1 || lay.Alignment > 9 {
		lay.Alignment = 2
	}
	if lay.BorderStyle != 3 {
		lay.BorderStyle = 1
	}
	if lay.MarginV < 0 {
		lay.MarginV = 0
	}
	topAlign := 7 + (lay.Alignment-1)%3

	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "[Script Info]\n; Generated by notesteme\nTitle: %s\nScriptType: v4.00+\nWrapStyle: 0\nScaledBorderAndShadow: yes\nYCbCr Matrix: None\nPlayResX: %d\nPlayResY: %d\n\n",
		assField(title), ASSPlayResX, ASSPlayResY)

	transAlign, primAlign := lay.Alignment, lay.Alignment
	if lay.TranslationAt == AtTop {
		transAlign = topAlign
	}
	bw.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	bw.WriteString(st.Translation.styleLine("Translation", lay.BorderStyle, transAlign, lay.MarginV) + "\n")
	bw.WriteString(st.Primary.styleLine("Primary", lay.BorderStyle, primAlign, lay.MarginV) + "\n\n")

	bw.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	return &ASSWriter{bw: bw, lay: lay}
}

// Write 寫出一句的事件。雙語時依 Layout 決定：堆疊在同一個事件（第二行用 {\r樣式} 切換）、或譯文放到畫面上方（兩個事件）、
// 或只顯示其中一種。單語（Orig 為空）時用譯文行的樣式；Layout.Show 為 primary 時用原文行的樣式。
// 回傳第一個寫入錯誤（client 斷線時停止）。
func (aw *ASSWriter) Write(p Pair) error {
	lay, bw := aw.lay, aw.bw
	start, end := assTime(p.StartMS, false), assTime(p.EndMS, true)
	event := func(style, text string) {
		if text != "" {
			fmt.Fprintf(bw, "Dialogue: 0,%s,%s,%s,,0,0,0,,%s\n", start, end, style, text)
		}
	}
	main, orig := assText(p.Main), assText(p.Orig)
	switch {
	case orig == "":
		style := "Translation"
		if lay.Show == ShowPrimary {
			style = "Primary"
		}
		event(style, main)
	case lay.Show == ShowTranslation:
		event("Translation", main)
	case lay.Show == ShowPrimary:
		event("Primary", orig)
	case lay.TranslationAt == AtTop:
		event("Translation", main)
		event("Primary", orig)
	case lay.Order == OrderPrimaryFirst:
		event("Primary", orig+`\N{\rTranslation}`+main)
	default:
		event("Translation", main+`\N{\rPrimary}`+orig)
	}
	_, err := bw.Write(nil)
	return err
}

// Close 清空緩衝。
func (aw *ASSWriter) Close() error { return aw.bw.Flush() }

// RenderASS 串流輸出 ASS：兩個樣式（Translation、Primary）加上每句一個事件（見 ASSWriter）。
func RenderASS(w io.Writer, pairs []Pair, st ASSStyle, title string) error {
	aw := NewASSWriter(w, st, title)
	for _, p := range pairs {
		if err := aw.Write(p); err != nil {
			return err
		}
	}
	return aw.Close()
}

// ---- 解析 ----

// ASSStyleRow 是 [V4+ Styles] 中的一列；欄位依 Format 列的欄名對應，沒有出現的欄位保持零值。
// 顏色已轉成 #RRGGBBAA（ASS 的 alpha 反轉成不透明度）；欄位值不合法時視為沒有出現。
type ASSStyleRow struct {
	Name                            string
	Font                            string
	Size                            float64
	Color, OutlineColor, BackColor  string
	Bold, HasBold                   bool
	Outline, Shadow                 float64
	HasOutline, HasShadow           bool
	BorderStyle, Alignment, MarginV int
	HasBorderStyle, HasAlignment    bool
	HasMargin                       bool
}

// ASSEvent 是 [Events] 中的一句 Dialogue：文字已移除覆寫碼，\N 換成換行。
type ASSEvent struct {
	StartMS, EndMS int64
	Style          string
	Text           string
	Line           int // 在檔案中的行號
}

// ASSFile 是解析後的 ASS。HasStyles 為 false 代表檔案沒有 [V4+ Styles] 段落。
type ASSFile struct {
	HasStyles bool
	// PlayResY 是 [Script Info] 的 PlayResY；沒有寫（或不合法）時為 0。
	PlayResY int
	Styles   []ASSStyleRow
	Events   []ASSEvent
}

// ASS 解析的上限：不可信輸入，只保護解析本身（檔案大小由呼叫端限制）。
const maxASSLineBytes = 1 << 20

// splitASSLine 把 `Key: value` 拆成小寫的 Key 與 value；沒有冒號時回傳 ok=false。
func splitASSLine(line string) (key, value string, ok bool) {
	k, v, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v), true
}

// ParseASS 解析 ASS／SSA：只讀 [V4+ Styles] 與 [Events]，其他段落（字型、圖片等）忽略。
// 欄位依 Format 列的欄名對應，不依位置；Dialogue 的最後一欄（Text）可以含逗號。
// 時間不合法時回傳 *ParseError（Cue 為第幾個 Dialogue，Line 為檔案行號）。
func ParseASS(data []byte) (*ASSFile, error) {
	s := strings.TrimPrefix(string(data), "\uFEFF")
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")

	f := &ASSFile{}
	var (
		section              string
		styleCols, eventCols []string
		nth                  int
	)
	for i, line := range strings.Split(s, "\n") {
		lineNo := i + 1
		if len(line) > maxASSLineBytes {
			return nil, &ParseError{Line: lineNo, Msg: "line too long"}
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "v4+ styles" {
				f.HasStyles = true
			}
			continue
		}
		key, val, ok := splitASSLine(line)
		if !ok {
			continue
		}
		switch section {
		case "script info":
			if key == "playresy" {
				if n, err := strconv.Atoi(val); err == nil && n > 0 && n <= 100000 {
					f.PlayResY = n
				}
			}
		case "v4+ styles":
			switch key {
			case "format":
				styleCols = splitCols(val)
			case "style":
				if len(styleCols) == 0 {
					continue
				}
				if row, ok := parseStyleRow(styleCols, val); ok {
					f.Styles = append(f.Styles, row)
				}
			}
		case "events":
			switch key {
			case "format":
				eventCols = splitCols(val)
			case "dialogue":
				if len(eventCols) == 0 {
					continue
				}
				nth++
				ev, err := parseEvent(eventCols, val)
				if err != nil {
					return nil, &ParseError{Cue: nth, Line: lineNo, Msg: err.Error()}
				}
				ev.Line = lineNo
				if ev.Text != "" {
					f.Events = append(f.Events, ev)
				}
			}
		}
	}
	return f, nil
}

func splitCols(format string) []string {
	cols := strings.Split(format, ",")
	for i, c := range cols {
		cols[i] = strings.ToLower(strings.TrimSpace(c))
	}
	return cols
}

// fieldsByCols 依欄名切開一列；最後一欄取走剩下的全部（Text 含逗號）。
func fieldsByCols(cols []string, val string) map[string]string {
	parts := strings.SplitN(val, ",", len(cols))
	out := make(map[string]string, len(parts))
	for i, p := range parts {
		out[cols[i]] = strings.TrimSpace(p)
	}
	return out
}

func parseStyleRow(cols []string, val string) (ASSStyleRow, bool) {
	m := fieldsByCols(cols, val)
	row := ASSStyleRow{Name: m["name"], Font: strings.TrimPrefix(m["fontname"], "@")}
	if row.Name == "" {
		return ASSStyleRow{}, false
	}
	if v, err := strconv.ParseFloat(m["fontsize"], 64); err == nil && v > 0 && v <= 1000 {
		row.Size = v
	}
	row.Color = parseASSColour(m["primarycolour"])
	row.OutlineColor = parseASSColour(m["outlinecolour"])
	row.BackColor = parseASSColour(m["backcolour"])
	if v, ok := m["bold"]; ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			row.HasBold, row.Bold = true, n != 0
		}
	}
	if v, err := strconv.ParseFloat(m["outline"], 64); err == nil && v >= 0 && v <= 100 {
		row.Outline, row.HasOutline = v, true
	}
	if v, err := strconv.ParseFloat(m["shadow"], 64); err == nil && v >= 0 && v <= 100 {
		row.Shadow, row.HasShadow = v, true
	}
	if v, err := strconv.Atoi(m["borderstyle"]); err == nil && (v == 1 || v == 3) {
		row.BorderStyle, row.HasBorderStyle = v, true
	}
	if v, err := strconv.Atoi(m["alignment"]); err == nil && v >= 1 && v <= 9 {
		row.Alignment, row.HasAlignment = v, true
	}
	if v, err := strconv.Atoi(m["marginv"]); err == nil && v >= 0 && v <= 2000 {
		row.MarginV, row.HasMargin = v, true
	}
	return row, true
}

// parseASSColour 解析 ASS 的顏色（&HAABBGGRR、&HBBGGRR 或十進位整數）成 #RRGGBBAA；不合法回傳空字串。
func parseASSColour(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var v uint64
	var err error
	if up := strings.ToUpper(s); strings.HasPrefix(up, "&H") || strings.HasPrefix(up, "H") {
		hex := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(up, "&"), "H"), "&")
		if hex == "" || len(hex) > 8 {
			return ""
		}
		v, err = strconv.ParseUint(hex, 16, 32)
	} else {
		v, err = strconv.ParseUint(s, 10, 32)
	}
	if err != nil {
		return ""
	}
	return fmt.Sprintf("#%02X%02X%02X%02X", v&0xFF, (v>>8)&0xFF, (v>>16)&0xFF, 255-((v>>24)&0xFF))
}

func parseEvent(cols []string, val string) (ASSEvent, error) {
	m := fieldsByCols(cols, val)
	start, err := ParseTimecode(m["start"])
	if err != nil {
		return ASSEvent{}, fmt.Errorf("invalid start time")
	}
	end, err := ParseTimecode(m["end"])
	if err != nil {
		return ASSEvent{}, fmt.Errorf("invalid end time")
	}
	if end < start {
		return ASSEvent{}, fmt.Errorf("end is earlier than start")
	}
	text := m["text"]
	if hasDrawing(text) {
		text = ""
	}
	return ASSEvent{StartMS: start, EndMS: end, Style: m["style"], Text: assPlainText(text)}, nil
}

// hasDrawing 判斷事件是不是向量繪圖（{\p1}…）：它的「文字」是繪圖指令，不是字幕。
func hasDrawing(text string) bool {
	for rest := text; ; {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			return false
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return false
		}
		block := rest[i+1 : i+j]
		for k := 0; k+2 < len(block); k++ {
			if block[k] == '\\' && block[k+1] == 'p' && block[k+2] >= '1' && block[k+2] <= '9' {
				return true
			}
		}
		rest = rest[i+j+1:]
	}
}

// assPlainText 移除覆寫碼區塊（{…}），把 \N、\n 換成換行、\h 換成空白；多餘的空白整理掉。
func assPlainText(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == '{':
			if j := strings.IndexByte(text[i:], '}'); j >= 0 {
				i += j + 1
				continue
			}
			b.WriteByte(c)
			i++
		case c == '\\' && i+1 < len(text) && (text[i+1] == 'N' || text[i+1] == 'n'):
			b.WriteByte('\n')
			i += 2
		case c == '\\' && i+1 < len(text) && text[i+1] == 'h':
			b.WriteByte(' ')
			i += 2
		default:
			b.WriteByte(c)
			i++
		}
	}
	lines := strings.Split(b.String(), "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
