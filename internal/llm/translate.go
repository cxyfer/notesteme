package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/langdetect"
	"github.com/cxyfer/notesteme/internal/langtag"
)

// SplitMarker 是句群譯文中的切分標記（TRN-R3）：句群有 n 個 cue 時，譯文要有 n-1 個標記。
const SplitMarker = "⟨|⟩"

// translate 的驗證與退回原因（TRN-R5、R7），也是產出 fallbacks[].reason 的值。
const (
	ReasonEmpty         = "empty"
	ReasonUntranslated  = "untranslated"
	ReasonWrongLanguage = "wrong_language"
	ReasonLengthRatio   = "length_ratio"
	// ReasonReadingSpeed 只是警告（TRN-R6），不是驗證失敗。
	ReasonReadingSpeed = "reading_speed"
)

// translate 的兩種模式（TRN-R9）。
const (
	ModeTranslate = "translate"
	ModeLocalize  = "localize"
)

// 預設值與上限。
const (
	DefaultMinRatio = 0.3
	DefaultMaxRatio = 3.0
	// DefaultLocalizeSimilarity 是 localize 的相似度下限（以 OpenCC 轉換後的文字為基準，TRN-R9）。
	// 在地化會改寫用詞（「這個視頻的質量」→「這部影片的品質」相似度約 0.44），所以比 COR 的 0.6 寬鬆。
	DefaultLocalizeSimilarity = 0.3
	// MaxGroupCues 是一個句群最多的 cue 數：沒有標點的長段落不組成一個巨大的句群。
	MaxGroupCues = 5
	// maxListedProblems 是驗證錯誤說明中最多列出的句子數。
	maxListedProblems = 5
)

// TranslateOptions 是 translate 的 Spec 需要的設定：目標語言、模式與驗證門檻。
type TranslateOptions struct {
	Target   string
	Mode     string // ModeTranslate 或 ModeLocalize
	MinRatio float64
	MaxRatio float64
	// MinSimilarity 只用於 localize。
	MinSimilarity float64
	// Glossary 的詞條視為專有名詞：原文就是詞條時，譯文可以與原文相同。
	Glossary []string
}

// TranslateSpec 是 translate 的 Spec：輸出 {cues: [{i, text}]}，與輸入的翻譯單位一一對應（i 是句群第一個 cue 的句號）；
// 每一句再做 TRN-R5 的驗證。切分標記數不符不算驗證失敗（TRN-R4，由呼叫端改成逐 cue 翻譯）。
func TranslateSpec(o TranslateOptions) *Spec {
	target := langCode(o.Target)
	rules := "輸出 {\"cues\": [{\"i\", \"text\"}]}：cues 與輸入的 cues 一一對應，句號 i 與順序都相同，不要合併或拆分，也不要輸出 context。" +
		"目標語言：" + target + "。"
	if o.Mode == ModeLocalize {
		rules += "輸入已經是目標字形的文字，只修正用詞與語氣，不要翻譯成其他語言。"
	} else {
		rules += "輸入的 text 含有切分標記 " + SplitMarker + " 時，代表一句話跨越多行字幕：請整句翻譯，並在譯文中對應的位置放入相同數量的 " +
			SplitMarker + "，不要增減。"
	}
	return &Spec{
		Stage:       "translate",
		Schema:      cuesSchema(),
		OutputRules: rules,
		Check: func(in ChunkInput, out json.RawMessage) *ValidationError {
			if _, verr := oneToOne(in, out); verr != nil {
				return verr
			}
			cues, _ := DecodeCues(out)
			var first string
			var problems []string
			for k, c := range cues {
				reason, detail := o.CheckUnit(in.Cues[k].Text, c.Text)
				if reason == "" {
					continue
				}
				if first == "" {
					first = reason
				}
				if len(problems) < maxListedProblems {
					problems = append(problems, fmt.Sprintf("cue %d: %s (%s)", c.I, reason, detail))
				}
			}
			if first == "" {
				return nil
			}
			return &ValidationError{Reason: first, Message: strings.Join(problems, "; ")}
		},
	}
}

// CheckUnit 驗證一句（或一個句群）的譯文（TRN-R5）；通過時 reason 是空字串。src 是送出的原文
// （localize 時是 OpenCC 轉換後的文字），兩邊的切分標記都先拿掉再比較。detail 是給模型看的英文說明。
func (o TranslateOptions) CheckUnit(src, out string) (reason, detail string) {
	src = strings.TrimSpace(strings.ReplaceAll(src, SplitMarker, " "))
	text := strings.TrimSpace(strings.ReplaceAll(out, SplitMarker, " "))
	if text == "" {
		return ReasonEmpty, "the translation is empty"
	}
	short := utf8.RuneCountInString(src) <= 3
	if o.Mode == ModeLocalize {
		if !langdetect.Matches(o.Target, text) {
			return ReasonWrongLanguage, "the text is not in " + langCode(o.Target)
		}
		if !short {
			if r, ok := o.ratioOK(src, text); !ok {
				return ReasonLengthRatio, r
			}
		}
		if s := Similarity(src, text); s < o.MinSimilarity {
			return ReasonSimilarity, fmt.Sprintf("the text changed too much (similarity %.2f, at least %.2f); only adjust wording", s, o.MinSimilarity)
		}
		return "", ""
	}
	if collapse(text) == collapse(src) {
		// 不可和原文相同，除非原文是專有名詞或不超過 3 個字元：這時語言與長度也不檢查。
		if short || o.properNoun(src) {
			return "", ""
		}
		return ReasonUntranslated, "the text is the same as the source; translate it into " + langCode(o.Target)
	}
	if !langdetect.Matches(o.Target, text) {
		return ReasonWrongLanguage, "the translation is not in " + langCode(o.Target)
	}
	if !short {
		if r, ok := o.ratioOK(src, text); !ok {
			return ReasonLengthRatio, r
		}
	}
	return "", ""
}

func (o TranslateOptions) ratioOK(src, text string) (string, bool) {
	lo, hi := o.MinRatio, o.MaxRatio
	if lo <= 0 {
		lo = DefaultMinRatio
	}
	if hi <= 0 {
		hi = DefaultMaxRatio
	}
	n := utf8.RuneCountInString(src)
	if n == 0 {
		return "", true
	}
	r := float64(utf8.RuneCountInString(text)) / float64(n)
	if r < lo || r > hi {
		return fmt.Sprintf("length ratio %.2f is outside %.2f–%.2f", r, lo, hi), false
	}
	return "", true
}

// properNoun 回報原文是不是專有名詞：等於詞彙表的詞條，或是不超過 5 個、每個都以大寫字母或數字開頭的詞（例如 Kafka.）。
func (o TranslateOptions) properNoun(src string) bool {
	bare := strings.TrimFunc(src, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	for _, g := range o.Glossary {
		if strings.EqualFold(strings.TrimSpace(g), bare) && bare != "" {
			return true
		}
	}
	words := strings.Fields(bare)
	if len(words) == 0 || len(words) > 5 {
		return false
	}
	for _, w := range words {
		w = strings.TrimFunc(w, unicode.IsPunct)
		r, _ := utf8.DecodeRuneInString(w)
		if w == "" || !(unicode.IsUpper(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// collapse 把連續的空白縮成一個，比較「與原文相同」時不受空白差異影響。
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// SplitParts 依切分標記把句群的譯文分回 n 個 cue（TRN-R3）。標記數不是 n-1、或分出的某一段是空的時 ok 為 false（TRN-R4）。
// 只做線性的計數與切分，輸出大小受 provider 回應的上限限制。
func SplitParts(text string, n int) (parts []string, ok bool) {
	if n < 1 || strings.Count(text, SplitMarker) != n-1 {
		return nil, false
	}
	parts = strings.Split(text, SplitMarker)
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
		if parts[i] == "" {
			return nil, false
		}
	}
	return parts, true
}

// Unit 是一個翻譯單位（TRN-R2）：從第 First 個 cue（從 0 起算）開始的 Count 個 cue。Count > 1 是句群。
type Unit struct {
	First int
	Count int
}

// BuildUnits 依句末標點把 cue 組成翻譯單位：一句話跨越多個 cue 時組成句群，其餘一個 cue 一個單位。
//   - 字幕完全沒有句末標點（例如沒有標點的 ASR）時不組句群：每個 cue 都會被當成同一句。
//   - 句群最多 MaxGroupCues 個 cue；更長的一句話依序切成多個句群。
//   - 一句話的任何一個 cue 含有切分標記時，這句話的 cue 全部逐 cue 翻譯（標記會與原文混在一起）。
func BuildUnits(texts []string) []Unit {
	units := make([]Unit, 0, len(texts))
	anyFinal := false
	for _, t := range texts {
		if endsSentence(t) {
			anyFinal = true
			break
		}
	}
	singles := func(from, to int) {
		for k := from; k < to; k++ {
			units = append(units, Unit{First: k, Count: 1})
		}
	}
	if !anyFinal {
		singles(0, len(texts))
		return units
	}
	start := 0
	for k := range texts {
		if !endsSentence(texts[k]) && k < len(texts)-1 {
			continue
		}
		// texts[start..k] 是一句話
		hasMarker := false
		for j := start; j <= k; j++ {
			if strings.Contains(texts[j], SplitMarker) {
				hasMarker = true
			}
		}
		if hasMarker {
			singles(start, k+1)
		} else {
			for s := start; s <= k; s += MaxGroupCues {
				units = append(units, Unit{First: s, Count: min(MaxGroupCues, k+1-s)})
			}
		}
		start = k + 1
	}
	return units
}

// closers 是句末標點之後可能出現的右引號與右括號。
const closers = "\"'”’」』）)]】》>"

// endsSentence 回報文字是不是以句末標點結束（忽略結尾的空白與右引號、右括號）。
func endsSentence(s string) bool {
	s = strings.TrimRightFunc(s, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(closers, r) })
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".!?。！？…‼⁇⁈⁉", r)
}

// ReadingSpeedLimit 是目標語言的閱讀速度上限（字／秒，plan 第 8.5 節）：中文、日文、韓文約 9，其他約 17。
func ReadingSpeedLimit(target string) float64 {
	switch langtag.Parse(target).Lang {
	case "zh", "ja", "ko":
		return 9
	}
	return 17
}

// ReadingChars 是計算閱讀速度的字數：字母與數字（標點與空白不算）。
func ReadingChars(text string) int {
	n := 0
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			n++
		}
	}
	return n
}
