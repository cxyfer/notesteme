package apierr

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 把一段繁中訊息換成其他語言（NFR-I18N-2）。三種來源，依序：
//
//   - 固定的句子：messages、domainMessages、hubMessages（以繁中原文為 key）。
//   - domain 套件以 fmt.Sprintf 組出的句子：formats 的繁中格式（%s、%d、%q、%v）編成錨定整句的正規式反解出參數，
//     再填進目標語言的範本（{0}、{1}…）。參數本身也是目錄中的句子時一併翻譯（例如欄位錯誤包著的說明）。
//   - 都不符合時照原樣（繁中）。
//
// 這樣 domain 套件與 handler 照舊寫繁中，既有場景的預期文字不變；目錄的涵蓋範圍由 catalog_test.go 與
// internal/api 的 i18n 測試檢查。

// listSep 分隔清單參數的項目（List）：填入範本時依語言換成「、」或「, 」。
const listSep = "\x1f"

// List 把多個項目組成一個參數：填入範本時依語言用「、」或「, 」連接（例如 /readyz 失敗的檢查項目）。
func List(items []string) string { return strings.Join(items, listSep) }

// format 是一則 domain 格式：hant 是 fmt 的格式字串，hans、en 是 {0}、{1}… 的範本。
type format struct {
	hant, hans, en string
	re             *regexp.Regexp
	// literal 是格式中不屬於參數的字元數：同一句同時符合多則格式時，取最具體的（字面最長的）。
	literal int
}

// compiledFormats 是 domainFormats 與 hubFormats 編譯後的結果（init 時建立）。
var compiledFormats []format

func init() {
	for _, tbl := range [][]struct{ hant, hans, en string }{domainFormats, hubFormats, humaFormats} {
		for _, f := range tbl {
			re, lit := compileFormat(f.hant)
			compiledFormats = append(compiledFormats, format{f.hant, f.hans, f.en, re, lit})
		}
	}
	sort.SliceStable(compiledFormats, func(i, j int) bool { return compiledFormats[i].literal > compiledFormats[j].literal })
}

// compileFormat 把 fmt 的格式字串編成錨定整句的正規式；回傳字面字元數。支援 %s %v %d %g %q %%。
func compileFormat(f string) (*regexp.Regexp, int) {
	var b strings.Builder
	b.WriteString("^")
	lit := 0
	for {
		i := strings.IndexByte(f, '%')
		if i < 0 || i+1 >= len(f) {
			b.WriteString(regexp.QuoteMeta(f))
			lit += utf8.RuneCountInString(f)
			break
		}
		b.WriteString(regexp.QuoteMeta(f[:i]))
		lit += utf8.RuneCountInString(f[:i])
		switch f[i+1] {
		case 's', 'v':
			b.WriteString("(.+?)")
		case 'd':
			b.WriteString("(-?[0-9]+)")
		case 'g':
			b.WriteString("(-?[0-9.]+(?:e[-+]?[0-9]+)?)")
		case 'q':
			b.WriteString(`("(?:[^"\\]|\\.)*")`)
		case '%':
			b.WriteString("%")
			lit++
		default:
			panic("apierr: 格式不支援 %" + string(f[i+1]) + "：" + f)
		}
		f = f[i+2:]
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()), lit
}

// lookupMessage 在固定句子的目錄中找 s。
func lookupMessage(s string) (translation, bool) {
	for _, m := range []map[string]translation{messages, domainMessages, hubMessages} {
		if t, ok := m[s]; ok {
			return t, true
		}
	}
	return translation{}, false
}

// translateText 把繁中的 s 換成 lang；目錄中沒有時 ok 為 false。depth 限制參數的巢狀翻譯。
func translateText(s, lang string, depth int) (string, bool) {
	if lang == LangZhHant || lang == "" || s == "" {
		return s, false
	}
	if t, ok := lookupMessage(s); ok {
		if lang == LangZhHans {
			return t.hans, true
		}
		return t.en, true
	}
	if depth > 2 {
		return s, false
	}
	for _, f := range compiledFormats {
		m := f.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		args := make([]string, len(m)-1)
		for i, a := range m[1:] {
			args[i] = translateArg(a, lang, depth+1)
		}
		tmpl := f.en
		if lang == LangZhHans {
			tmpl = f.hans
		}
		return fill(tmpl, args, lang), true
	}
	return s, false
}

// translateArg 翻譯參數：目錄中的句子換成 lang，其他（ID、數字、名稱、外部原文）不變。
func translateArg(a, lang string, depth int) string {
	if strings.Contains(a, listSep) {
		parts := strings.Split(a, listSep)
		for i, p := range parts {
			parts[i] = translateArg(p, lang, depth)
		}
		return strings.Join(parts, listSep)
	}
	if t, ok := translateText(a, lang, depth); ok {
		return t
	}
	// 以「、」連接的識別字清單（例如 strings.Join(vals, "、") 的 youtube、bilibili）：英文改用「, 」。
	// 只在每一項都不含中文字時才當成清單（其他文字中的「、」不動）。
	if lang == LangEn && strings.Contains(a, "、") {
		parts := strings.Split(a, "、")
		for _, p := range parts {
			if p == "" || hasHan(p) {
				return a
			}
		}
		return strings.Join(parts, ", ")
	}
	return a
}

// hasHan 回報 s 有沒有中文字。
func hasHan(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

// Translate 把一段繁中訊息換成 lang；目錄中沒有時 ok 為 false、回傳原文（NFR-I18N-2）。
// 給 handler 以外需要同一份目錄的地方（例如 Hub 寫出錯誤前、測試檢查涵蓋範圍）。
func Translate(s, lang string) (string, bool) { return translateText(s, lang, 0) }

// fill 把參數依序填進 {0}、{1}…；清單參數（List）依語言連接。
func fill(tmpl string, args []string, lang string) string {
	sep := "、"
	if lang == LangEn {
		sep = ", "
	}
	for i, a := range args {
		tmpl = strings.ReplaceAll(tmpl, "{"+strconv.Itoa(i)+"}", strings.ReplaceAll(a, listSep, sep))
	}
	return tmpl
}

// HasFormat 回報 domain 格式的目錄中有沒有這個 fmt 格式字串（給檢查呼叫點的測試）。
func HasFormat(f string) bool {
	for _, c := range compiledFormats {
		if c.hant == f {
			return true
		}
	}
	return false
}

// HasTemplate 回報有沒有這個訊息範本（給檢查呼叫點的測試）。
func HasTemplate(key string) bool { _, ok := templates[key]; return ok }
