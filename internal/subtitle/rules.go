package subtitle

import (
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/langtag"
)

// Script 是字幕規則依文字系統分的類別（plan 第 8.5 節、處理方案的 subtitle_rules.<script>）。
type Script string

const (
	ScriptCJK   Script = "cjk"
	ScriptLatin Script = "latin"
)

// Rules 是一個文字系統的字幕規則（plan 第 8.5 節）。時間以毫秒表示。
type Rules struct {
	MaxCharsPerLine int
	MaxLines        int
	MinDurationMS   int64
	MaxDurationMS   int64
}

// DefaultRules 回傳 plan 第 8.5 節的預設值。CJK 每行字數的範圍是 20–25，暫取 22（SEG 待決問題，M0 量測後調整）。
func DefaultRules(s Script) Rules {
	r := Rules{MaxCharsPerLine: 42, MaxLines: 2, MinDurationMS: 800, MaxDurationMS: 7000}
	if s == ScriptCJK {
		r.MaxCharsPerLine = 22
	}
	return r
}

// ScriptOf 依語言標記判斷文字系統：中文、日文、韓文是 cjk，其他是 latin。
// 語言不明時依文字判斷：漢字與假名佔可見字元一半以上時是 cjk。
func ScriptOf(lang string, texts ...string) Script {
	switch langtag.Parse(lang).Lang {
	case "zh", "ja", "ko", "yue", "cmn", "wuu":
		return ScriptCJK
	case "":
	default:
		return ScriptLatin
	}
	cjk, total := 0, 0
	for _, t := range texts {
		for _, r := range t {
			if unicode.IsSpace(r) || unicode.IsPunct(r) {
				continue
			}
			total++
			if IsCJK(r) {
				cjk++
			}
		}
	}
	if total > 0 && 2*cjk >= total {
		return ScriptCJK
	}
	return ScriptLatin
}

// IsCJK 回報 r 是不是漢字或假名：這些字元之間沒有空白，任何兩字之間都可以斷句或換行。
func IsCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
}

// LinesNeeded 回傳 text 依每行最多 maxChars 個字元排版時需要的行數（不改變文字，只計算）。
// 已有的換行照原樣斷開；拉丁文字依空白換行（單一個字詞超過一行時依字元數切），CJK 依字元數。
func LinesNeeded(text string, maxChars int) int {
	if maxChars <= 0 {
		return 0
	}
	n := 0
	for _, line := range strings.Split(text, "\n") {
		n += wrapCount(line, maxChars)
	}
	return n
}

// wrapUnit 是換行的最小單位：拉丁文字是以空白分開的字詞，CJK 是一個字元（連同緊接在後的標點）。
// space 為 true 時，與同一行前一個單位之間要有一個空白。
type wrapUnit struct {
	n     int // 字元數
	space bool
}

// wrapUnits 把一行切成換行單位。
func wrapUnits(line string) []wrapUnit {
	var out []wrapUnit
	for _, field := range strings.Fields(line) {
		first, n := true, 0 // n 是目前累積中的拉丁片段的字元數
		flush := func() {
			if n > 0 {
				out = append(out, wrapUnit{n: n, space: first})
				first, n = false, 0
			}
		}
		for _, r := range field {
			switch {
			case IsCJK(r):
				flush()
				out = append(out, wrapUnit{n: 1, space: first})
				first = false
			case n == 0 && !first && unicode.IsPunct(r):
				// 緊接在 CJK 字元之後的標點跟著那個字，不另起一個單位。
				out[len(out)-1].n++
			default:
				n++
			}
		}
		flush()
	}
	return out
}

// wrapCount 是一行文字的貪婪換行行數；空行算 0 行。單一單位超過一行時依字元數切開。
func wrapCount(line string, maxChars int) int {
	lines, cur := 0, 0 // cur 是目前這一行的字元數
	for _, u := range wrapUnits(line) {
		need := u.n
		if cur > 0 && u.space {
			need++
		}
		if cur > 0 && cur+need <= maxChars {
			cur += need
			continue
		}
		if cur > 0 {
			lines++
		}
		w := u.n
		for w > maxChars {
			lines++
			w -= maxChars
		}
		cur = w
	}
	if cur > 0 {
		lines++
	}
	return lines
}

// Violation 是一句字幕違反的規則。
type Violation struct {
	Rule string // max_lines、max_duration、min_duration
	// Value、Limit 是量到的值與上限（行數或毫秒）。
	Value int64
	Limit int64
}

// 規則名稱（產出 warnings 的 rule）。
const (
	RuleMaxLines    = "max_lines"
	RuleMaxDuration = "max_duration"
	RuleMinDuration = "min_duration"
)

// Check 回傳 c 違反的規則（每行字數與行數合併成 max_lines：排版後的行數超過上限）。
func (r Rules) Check(c Cue) []Violation {
	var out []Violation
	if n := LinesNeeded(c.Text, r.MaxCharsPerLine); r.MaxLines > 0 && n > r.MaxLines {
		out = append(out, Violation{Rule: RuleMaxLines, Value: int64(n), Limit: int64(r.MaxLines)})
	}
	d := c.EndMS - c.StartMS
	if r.MaxDurationMS > 0 && d > r.MaxDurationMS {
		out = append(out, Violation{Rule: RuleMaxDuration, Value: d, Limit: r.MaxDurationMS})
	}
	if r.MinDurationMS > 0 && d < r.MinDurationMS {
		out = append(out, Violation{Rule: RuleMinDuration, Value: d, Limit: r.MinDurationMS})
	}
	return out
}

// TooLong 回報 c 是否超過長度或時長的上限（SEG-R7 when_needed 與 SEG-R10 的強制切分條件）；太短不算。
func (r Rules) TooLong(c Cue) bool {
	for _, v := range r.Check(c) {
		if v.Rule != RuleMinDuration {
			return true
		}
	}
	return false
}
