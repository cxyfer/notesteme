package langdetect

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/zhconv"
)

// 這個檔案是原文的文字語言偵測（SRC-R11 第三層）：不連網、不用統計模型，依文字系統的份量判斷語言，
// 拉丁字母與西里爾字母的語言再用常見字詞區分。份量的單位同 Count：漢字、假名、諺文、泰文一個字一份，
// 其他字母文字一個詞一份（一個英文詞與一個漢字的份量相近，中英夾雜時不會因為英文字母多而偏向英文）。

// MixedShare 是兩種語言都要達到的份量比例（SRC-R11：各占 20% 以上）。
const MixedShare = 0.2

// MinUnits 是偵測需要的最少份量；太短的文字（只有一兩個詞）不判斷。
const MinUnits = 8

// Detection 是一段文字的語言偵測結果。
type Detection struct {
	// Lang 是主要語言（BCP-47 的主語言，例如 zh、ja、en）；拉丁字母但認不出是哪個語言時為空字串。
	Lang string
	// Script 是中文的字形（Hans、Hant）；不是中文或判斷不了時為空字串。
	Script string
	// Mixed 為 true 時有兩種語言都各占 MixedShare 以上（SRC-R11 的 mixed）。
	Mixed bool
	// Second 是 Mixed 時的第二種語言；認不出的拉丁字母語言視為 en（夾雜的多半是英文術語）。
	Second string
	// Shares 是各語言的份量比例（鍵是語言；認不出的拉丁字母語言是 "Latn"）。
	Shares map[string]float64
}

// latinWords 是區分拉丁字母語言用的常見字詞（小寫）。只列各語言特有、不容易和其他語言重複的功能詞。
var latinWords = map[string][]string{
	"en": {"the", "and", "is", "are", "of", "to", "that", "this", "with", "for", "you", "we", "it", "what", "why", "how", "was", "be", "have", "not", "so", "where", "there", "they"},
	"fr": {"le", "les", "des", "est", "et", "une", "pour", "dans", "pas", "nous", "vous", "qui", "avec", "sur", "mais", "très", "c'est"},
	"de": {"der", "die", "das", "und", "ist", "nicht", "ich", "wir", "mit", "für", "auf", "ein", "eine", "auch", "sie", "es", "zu"},
	"es": {"el", "los", "las", "es", "y", "que", "una", "por", "para", "con", "no", "como", "pero", "muy", "está", "del"},
	"pt": {"os", "as", "é", "não", "uma", "para", "com", "que", "mas", "muito", "está", "você", "do", "da"},
	"it": {"il", "gli", "è", "non", "che", "una", "per", "con", "sono", "della", "anche", "questo"},
}

// cyrillicWords 同上，區分俄文與烏克蘭文。
var cyrillicWords = map[string][]string{
	"ru": {"и", "в", "не", "что", "это", "мы", "как", "он", "она", "на", "с", "по", "для", "но", "есть"},
	"uk": {"і", "що", "це", "ми", "як", "він", "вона", "на", "з", "по", "для", "але", "є", "та"},
}

// scriptLangs 是只對應一種語言的文字系統。
var scriptLangs = map[Script]string{Hangul: "ko", Greek: "el", Arabic: "ar", Hebrew: "he", Thai: "th", Devanagari: "hi"}

// Detect 偵測 text 的語言（SRC-R11 第三層）；份量不到 MinUnits 時 ok 為 false。
//
//   - 假名的份量達到漢字的五分之一時，漢字與假名都算日文；否則漢字算中文、少量的假名仍算日文。
//   - 拉丁字母、西里爾字母的語言依常見字詞的出現次數決定；一個都沒對到時語言為空字串（只知道是拉丁字母）。
//   - 主要語言是份量最多的；第二多的也達到 MixedShare 時標為 Mixed。
func Detect(text string) (Detection, bool) {
	c := Count(text)
	total := 0
	for _, n := range c {
		total += n
	}
	if total < MinUnits {
		return Detection{}, false
	}
	units := map[string]int{}
	han, kana := c[Han], c[Kana]
	switch {
	case kana > 0 && kana*5 >= han:
		units["ja"] += han + kana
	default:
		units["zh"] += han
		units["ja"] += kana
	}
	for s, lang := range scriptLangs {
		units[lang] += c[s]
	}
	if n := c[Latin]; n > 0 {
		units[cmp.Or(guessByWords(text, latinWords, isLatinWord), "Latn")] += n
	}
	if n := c[Cyrillic]; n > 0 {
		units[cmp.Or(guessByWords(text, cyrillicWords, isCyrillicWord), "ru")] += n
	}
	units["und"] += c[Other]

	type share struct {
		lang string
		n    int
	}
	var list []share
	shares := map[string]float64{}
	for lang, n := range units {
		if n == 0 {
			continue
		}
		list = append(list, share{lang, n})
		shares[lang] = float64(n) / float64(total)
	}
	// 份量相同時依語言排序，結果才是確定的。
	slices.SortFunc(list, func(a, b share) int { return cmp.Or(b.n-a.n, strings.Compare(a.lang, b.lang)) })
	d := Detection{Shares: shares}
	d.Lang = langOf(list[0].lang)
	if len(list) > 1 && float64(list[1].n) >= MixedShare*float64(total) {
		d.Mixed, d.Second = true, langOf(list[1].lang)
		if list[1].lang == "Latn" {
			// 夾雜在其他語言中的拉丁字母多半是英文術語（沒有功能詞可以判斷），第二語言視為 en。
			d.Second = "en"
		}
	}
	if d.Lang == "zh" {
		d.Script = zhconv.DetectScript(text)
	}
	return d, true
}

// langOf 把內部的分類換成回傳的語言：認不出的拉丁字母與其他文字是空字串。
func langOf(k string) string {
	if k == "Latn" || k == "und" {
		return ""
	}
	return k
}

// guessByWords 回傳常見字詞出現最多次的語言；都沒有出現時回傳空字串。次數相同時依語言排序取第一個。
func guessByWords(text string, table map[string][]string, inWord func(rune) bool) string {
	counts := map[string]int{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !inWord(r) }) {
		for lang, words := range table {
			if slices.Contains(words, w) {
				counts[lang]++
			}
		}
	}
	best, bestN := "", 0
	langs := make([]string, 0, len(counts))
	for l := range counts {
		langs = append(langs, l)
	}
	slices.Sort(langs)
	for _, l := range langs {
		if counts[l] > bestN {
			best, bestN = l, counts[l]
		}
	}
	return best
}

func isLatinWord(r rune) bool    { return unicode.Is(unicode.Latin, r) || r == '\'' }
func isCyrillicWord(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }
