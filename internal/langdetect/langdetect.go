// Package langdetect 是譯文驗證（TRN-R5）用的輕量語言判斷：只看文字系統與常見字元的比例，不做統計模型。
//
// 判斷的是「文字是不是用目標語言的文字系統寫的」：中文、日文、韓文之間用漢字、假名、諺文的比例區分，
// 中文的簡繁用 zhconv.DetectScript；拉丁字母的語言之間（en、fr、de…）無法區分，只確認是拉丁字母。
// 不認識的語言一律視為符合（保守：不因為判斷不了而讓翻譯失敗）。
package langdetect

import (
	"unicode"

	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

// Script 是文字系統的分類。
type Script string

const (
	Han        Script = "Hani"
	Kana       Script = "Kana" // 平假名與片假名
	Hangul     Script = "Hang"
	Latin      Script = "Latn"
	Cyrillic   Script = "Cyrl"
	Greek      Script = "Grek"
	Arabic     Script = "Arab"
	Hebrew     Script = "Hebr"
	Thai       Script = "Thai"
	Devanagari Script = "Deva"
	Other      Script = "Zyyy"
)

// Counts 是文字中各文字系統的數量：漢字、假名、諺文、泰文以字計；其他字母文字以詞（連續的字母）計，
// 一個英文詞與一個漢字的份量才相近。
type Counts map[Script]int

// Count 統計 text 的文字系統。數字、標點與空白不計。
func Count(text string) Counts {
	c := Counts{}
	var word Script // 目前所在的字母詞的文字系統；空字串代表不在詞中
	for _, r := range text {
		s, ok := classify(r)
		if !ok {
			word = ""
			continue
		}
		switch s {
		case Han, Kana, Hangul, Thai:
			c[s]++
			word = ""
		default:
			if word != s {
				c[s]++
				word = s
			}
		}
	}
	return c
}

// classify 回傳字母 r 的文字系統；不是字母（數字、標點、符號、空白）時 ok 為 false。
// 長音符「ー」與疊字符算假名，「々」算漢字。
func classify(r rune) (Script, bool) {
	switch {
	case r == 'ー' || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
		return Kana, true
	case r == '々' || unicode.Is(unicode.Han, r):
		return Han, true
	case unicode.Is(unicode.Hangul, r):
		return Hangul, true
	case !unicode.IsLetter(r):
		return "", false
	case unicode.Is(unicode.Latin, r):
		return Latin, true
	case unicode.Is(unicode.Cyrillic, r):
		return Cyrillic, true
	case unicode.Is(unicode.Greek, r):
		return Greek, true
	case unicode.Is(unicode.Arabic, r):
		return Arabic, true
	case unicode.Is(unicode.Hebrew, r):
		return Hebrew, true
	case unicode.Is(unicode.Thai, r):
		return Thai, true
	case unicode.Is(unicode.Devanagari, r):
		return Devanagari, true
	}
	return Other, true
}

// 語言對應的文字系統（沒有明確指定 script 時）。不在表內的語言不檢查。
var langScripts = map[string]Script{
	"zh": Han, "ja": Kana, "ko": Hangul,
	"ru": Cyrillic, "uk": Cyrillic, "bg": Cyrillic, "be": Cyrillic, "mk": Cyrillic, "sr": Cyrillic,
	"kk": Cyrillic, "ky": Cyrillic, "mn": Cyrillic, "tg": Cyrillic,
	"el": Greek, "ar": Arabic, "fa": Arabic, "ur": Arabic, "he": Hebrew, "yi": Hebrew, "th": Thai,
	"hi": Devanagari, "mr": Devanagari, "ne": Devanagari,
}

// latinLangs 是常見的拉丁字母語言。
var latinLangs = map[string]bool{
	"en": true, "fr": true, "de": true, "es": true, "it": true, "pt": true, "nl": true, "sv": true, "no": true, "nb": true,
	"nn": true, "da": true, "fi": true, "pl": true, "cs": true, "sk": true, "sl": true, "hr": true, "bs": true, "ro": true,
	"hu": true, "tr": true, "vi": true, "id": true, "ms": true, "tl": true, "fil": true, "ca": true, "eu": true, "gl": true,
	"et": true, "lv": true, "lt": true, "sq": true, "is": true, "ga": true, "cy": true, "af": true, "sw": true, "eo": true,
	"la": true, "mt": true, "lb": true, "az": true, "uz": true,
}

// scriptCodes 是 BCP-47 的 script 子標籤對應的文字系統。
var scriptCodes = map[string]Script{
	"Hans": Han, "Hant": Han, "Hani": Han, "Jpan": Kana, "Kore": Hangul, "Hang": Hangul,
	"Latn": Latin, "Cyrl": Cyrillic, "Grek": Greek, "Arab": Arabic, "Hebr": Hebrew, "Thai": Thai, "Deva": Devanagari,
}

// Expected 回傳目標語言應該使用的文字系統；ok 為 false 代表不認識，不檢查。
func Expected(target string) (Script, bool) {
	t := langtag.Parse(target)
	if s, ok := scriptCodes[t.Script]; ok {
		return s, true
	}
	if s, ok := langScripts[t.Lang]; ok {
		return s, true
	}
	if latinLangs[t.Lang] {
		return Latin, true
	}
	return "", false
}

// Matches 回報 text 看起來是不是目標語言（TRN-R5 的語言偵測）。判斷不了（不認識的語言、文字沒有任何字母）時回傳 true。
//
//   - 目標文字系統的份量至少要是其他文字系統的一半（中文譯文夾雜英文專有名詞是正常的）。
//   - 中文：假名的份量達到漢字的五分之一時視為日文；目標指定了簡體或繁體、而文字明確是另一種字形時不符。
//   - 日文：漢字與假名都算目標文字；有 4 個以上漢字而完全沒有假名時視為中文。
func Matches(target, text string) bool {
	want, ok := Expected(target)
	if !ok {
		return true
	}
	c := Count(text)
	total := 0
	for _, n := range c {
		total += n
	}
	if total == 0 {
		return true
	}
	var mine int
	switch want {
	case Han:
		if c[Kana] > 0 && c[Kana]*5 >= c[Han] {
			return false
		}
		mine = c[Han]
		if script := langtag.ChineseScript(target); script != "" {
			if got := zhconv.DetectScript(text); got != "" && got != script {
				return false
			}
		}
	case Kana:
		if c[Kana] == 0 && c[Han] >= 4 {
			return false
		}
		mine = c[Han] + c[Kana]
	default:
		mine = c[want]
	}
	return mine > 0 && 2*mine >= total-mine
}
