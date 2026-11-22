package llm

import (
	"encoding/json"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// segment 的退回原因（SEG-R3、R4）。
const (
	ReasonAlignment   = "alignment"    // 斷點對不回輸入的文字（位置超出範圍、不遞增、切在字詞中間、產生空的字幕）
	ReasonTextChanged = "text_changed" // 去掉空白與標點後，text 與輸入的文字不同
)

// 不可信輸出的上限：text 的字元數最多是輸入的 maxSegmentTextFactor 倍再加 maxSegmentTextSlack，
// 超過時不再逐字比對。比對與對齊都是線性時間，上限讓惡意的長輸出無法拖慢驗證。
const (
	maxSegmentTextFactor = 4
	maxSegmentTextSlack  = 256
	// maxQuotedText 是錯誤說明中引用的文字片段的最大字元數。
	maxQuotedText = 24
)

// segmentSchema 是 {text, breaks} 的 schema（OpenAI strict 相容）：沒有任何時間欄位（SEG-R2）。
func segmentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":   map[string]any{"type": "string"},
			"breaks": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
		},
		"required":             []any{"text", "breaks"},
		"additionalProperties": false,
	}
}

// SegmentOutput 是 segment 的輸出：模型依序重寫的 chunk 文字，以及每個新字幕在 text 中開始的字元（rune）位置。
type SegmentOutput struct {
	Text   string `json:"text"`
	Breaks []int  `json:"breaks"`
}

// SegmentSpec 是 segment 步驟的 Spec（SEG-R2～R4）。LLM 只決定斷點，時間一律由程式依來源計算（ADR-0003）。
func SegmentSpec() *Spec {
	return &Spec{
		Stage:  "segment",
		Schema: segmentSchema(),
		OutputRules: "輸出 {\"text\", \"breaks\"}：text 是把 cues 的 text 依序接成的一段文字（只可以調整空白與標點，不可增刪、改寫或重排任何字，不要包含 context）；" +
			"breaks 是每一句新字幕在 text 中開始的位置（以 Unicode 字元計、從 0 起算、嚴格遞增，第一句的 0 不用寫），只能落在字詞之間；不要輸出任何時間。",
		Check: checkSegment,
	}
}

func checkSegment(in ChunkInput, out json.RawMessage) *ValidationError {
	_, verr := SegmentMarks(in, out)
	return verr
}

// CueMarks 是一句輸入字幕的斷句結果（與 chunk 的 cues 一一對應）。
type CueMarks struct {
	I int `json:"i"`
	// Join 為 true 時，這句與前一句之間沒有斷點（合併，SEG-R5）。chunk 的第一句一律是 false。
	Join bool `json:"join"`
	// Cuts 是句內的斷點：新字幕在這句文字中開始的字元（rune）位置，嚴格遞增，0 < cut < 字元數。
	// 斷點前的文字去掉結尾空白、斷點後的文字去掉開頭空白；斷點與前一個字之間的標點歸前一段。
	Cuts []int `json:"cuts"`
}

// KeepMarks 回傳維持原本切法的斷句結果（每句獨立、沒有句內斷點）：重試用盡時的退回（SEG-R3）。
func KeepMarks(cues []Cue) []CueMarks {
	out := make([]CueMarks, len(cues))
	for k, c := range cues {
		out[k] = CueMarks{I: c.I}
	}
	return out
}

// IsSegmentKey 回報 r 是不是比對文字時會比較的字元：空白、標點與格式字元（例如零寬空白）以外的字元。
// 模型可以改動被忽略的字元（SEG-R3），其餘必須與輸入完全相同（SEG-R4）。
func IsSegmentKey(r rune) bool {
	return !unicode.IsSpace(r) && !unicode.IsPunct(r) && !unicode.Is(unicode.Cf, r)
}

// srcKey 是輸入文字中的一個比對字元：所在的句子（chunk 內從 0 起算）與在句子文字中的字元位置。
type srcKey struct {
	r   rune
	cue int
	off int
}

// SegmentMarks 驗證 segment 的輸出並把斷點對回輸入的句子（SEG-R2～R4）：
//  1. 去掉空白與標點後，text 必須與 chunk 的 cues 依序接起來的文字完全相同，否則 text_changed（R4）。
//  2. 每個斷點必須在 (0, len(text)) 之內、嚴格遞增，對回輸入後不能產生空的字幕，
//     也不能切在拉丁文字的字詞中間（CJK 字元之間可以），否則 alignment（R3）。
//
// 比對只看非空白、非標點的字元，所以模型改動標點與空白也對得回去；整個過程是線性時間。
func SegmentMarks(in ChunkInput, out json.RawMessage) ([]CueMarks, *ValidationError) {
	var o SegmentOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	texts := make([][]rune, len(in.Cues))
	var src []srcKey
	srcRunes := 0
	for j, c := range in.Cues {
		texts[j] = []rune(c.Text)
		srcRunes += len(texts[j])
		for off, r := range texts[j] {
			if IsSegmentKey(r) {
				src = append(src, srcKey{r: r, cue: j, off: off})
			}
		}
	}
	textRunes := utf8.RuneCountInString(o.Text)
	if textRunes > maxSegmentTextFactor*srcRunes+maxSegmentTextSlack {
		return nil, &ValidationError{Reason: ReasonTextChanged, Message: fmt.Sprintf(
			"text has %d characters but the cues only have %d; text must be the cues joined in order, changing only spaces and punctuation", textRunes, srcRunes)}
	}
	if verr := checkBreakPositions(o.Breaks, textRunes); verr != nil {
		return nil, verr
	}

	// 逐字比對，同時算出每個斷點之前有幾個比對字元（breaks 已確認遞增，用一個指標往前走）。
	keyAt := make([]int, len(o.Breaks)) // keyAt[b] 是第 b 個斷點之前的比對字元數
	k, bi, pos := 0, 0, 0
	for _, r := range o.Text {
		for bi < len(o.Breaks) && o.Breaks[bi] == pos {
			keyAt[bi] = k
			bi++
		}
		pos++
		if !IsSegmentKey(r) {
			continue
		}
		if k >= len(src) || src[k].r != r {
			return nil, textChanged(texts, src, k, o.Text, pos-1)
		}
		k++
	}
	if k != len(src) {
		return nil, textChanged(texts, src, k, o.Text, pos)
	}

	marks := make([]CueMarks, len(in.Cues))
	for j, c := range in.Cues {
		marks[j] = CueMarks{I: c.I, Join: j > 0}
	}
	prev := 0
	for b, n := range keyAt {
		if n == 0 || n == len(src) || n == prev {
			return nil, &ValidationError{Reason: ReasonAlignment, Message: fmt.Sprintf(
				"break %d at position %d leaves a subtitle without any words; every break must start a new non-empty subtitle", b+1, o.Breaks[b])}
		}
		prev = n
		left, right := src[n-1], src[n]
		if left.cue != right.cue {
			marks[right.cue].Join = false
			continue
		}
		cut, ok := cutBetween(texts[left.cue], left.off, right.off)
		if !ok {
			return nil, &ValidationError{Reason: ReasonAlignment, Message: fmt.Sprintf(
				"break %d at position %d splits the word %s; only break between words", b+1, o.Breaks[b], quoteWord(texts[left.cue], left.off))}
		}
		marks[left.cue].Cuts = append(marks[left.cue].Cuts, cut)
	}
	return marks, nil
}

// checkBreakPositions 檢查斷點位置：在 (0, n) 之內、嚴格遞增（也就不會重複）。
func checkBreakPositions(breaks []int, n int) *ValidationError {
	if len(breaks) >= max(n, 1) {
		return &ValidationError{Reason: ReasonAlignment, Message: fmt.Sprintf("there are %d breaks but text only has %d characters", len(breaks), n)}
	}
	prev := 0
	for b, p := range breaks {
		switch {
		case p <= 0 || p >= n:
			return &ValidationError{Reason: ReasonAlignment, Message: fmt.Sprintf(
				"break %d is %d but must be between 1 and %d (a character position inside text)", b+1, p, n-1)}
		case p <= prev:
			return &ValidationError{Reason: ReasonAlignment, Message: fmt.Sprintf(
				"break %d is %d but breaks must be strictly increasing (the previous one is %d)", b+1, p, prev)}
		}
		prev = p
	}
	return nil
}

// cutBetween 回傳同一句中，比對字元 a 與 b（a < b，中間只有空白與標點）之間的斷點位置：
// 中間有空白時斷在第一個空白（之前的標點歸前一段）；沒有空白時，兩邊有一個是 CJK 字元才可以斷，斷在 b（標點歸前一段）。
func cutBetween(text []rune, a, b int) (int, bool) {
	for i := a + 1; i < b; i++ {
		if unicode.IsSpace(text[i]) {
			return i, true
		}
	}
	if subtitle.IsCJK(text[a]) || subtitle.IsCJK(text[b]) {
		return b, true
	}
	return 0, false
}

// quoteWord 引用 off 所在的字詞（以空白分開，最多 maxQuotedText 個字元，%q 跳脫）。
func quoteWord(text []rune, off int) string {
	from, to := off, off+1
	for from > 0 && !unicode.IsSpace(text[from-1]) && off-from < maxQuotedText/2 {
		from--
	}
	for to < len(text) && !unicode.IsSpace(text[to]) && to-off < maxQuotedText/2 {
		to++
	}
	return fmt.Sprintf("%q", string(text[from:to]))
}

// textChanged 說明第一個不同的地方（SEG-R4）：輸入的第 k 個比對字元附近，與 text 的第 pos 個字元附近。
func textChanged(texts [][]rune, src []srcKey, k int, text string, pos int) *ValidationError {
	want := "the end of the cues"
	if k < len(src) {
		want = quoteWord(texts[src[k].cue], src[k].off)
	}
	got := "the end of text"
	if r := []rune(text); pos < len(r) {
		got = quoteWord(r, pos)
	}
	return &ValidationError{Reason: ReasonTextChanged, Message: fmt.Sprintf(
		"text differs from the cues near %s (expected %s); only spaces and punctuation may change, do not add, remove or rewrite words", got, want)}
}
