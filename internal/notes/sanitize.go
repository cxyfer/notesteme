// Package notes 產生影片筆記的 Markdown（NOTE）：不可信內容的處理（NOTE-R8）、frontmatter（NOTE-R2）、
// 時間連結（NOTE-R4）、畫面嵌入（NOTE-R9）、模板（NOTE-R11）與自動檢查（NOTE-R7）。
//
// 這個套件只有純函式，不讀寫檔案、不碰資料庫；pipeline 組好輸入後呼叫 Render 與 RunChecks。
package notes

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// dangerousFenceLangs 是會在 Obsidian 中執行的程式碼區塊語言（NOTE-R8）：改成 text。
var dangerousFenceLangs = map[string]bool{
	"dataview": true, "dataviewjs": true, "templater": true, "js-engine": true,
	"button": true, "meta-bind": true, "meta-bind-button": true, "meta-bind-js-view": true,
	"run-js": true, "tasks": true, "query": true,
}

var (
	fenceOpenRe = regexp.MustCompile("^( {0,3})(`{3,}|~{3,})(.*)$")
	headingRe   = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$`)
	setextRe    = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	refDefRe    = regexp.MustCompile(`^( {0,3})\[([^\]]+)\]:`)
	wikiRe      = regexp.MustCompile(`!?\[\[([^\[\]]*)\]\]`)
	cueRefRe    = regexp.MustCompile(`^cue:([0-9]{1,9})$`)
	// CueRefRe 找出內文中程式保留的 [[cue:N]]（NOTE-R4）。
	CueRefRe = regexp.MustCompile(`\[\[cue:([0-9]{1,9})\]\]`)
)

// fenceOpen 回報這一行是不是程式碼區塊的開頭（CommonMark：``` 的資訊字串不能含 `）；
// 是的時候回傳 [整行, 縮排, 標記, 資訊字串]。SanitizeBody 與自動檢查共用，兩邊的判斷才一致。
func fenceOpen(line string) []string {
	m := fenceOpenRe.FindStringSubmatch(line)
	if m == nil || (m[2][0] == '`' && strings.Contains(m[3], "`")) {
		return nil
	}
	return m
}

// fenceClose 回報這一行是不是 fence 這個程式碼區塊的結尾。
func fenceClose(line, fence string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, fence[:1]) && strings.Trim(t, fence[:1]) == "" && len(t) >= len(fence) && len(line)-len(strings.TrimLeft(line, " ")) <= 3
}

// escapeHTML 把原始 HTML 跳脫成文字（NOTE-R8）：< 與 > 換成實體，Templater 的 <% %> 也因此失效。
func escapeHTML(s string) string {
	if !strings.ContainsAny(s, "<>") {
		return s
	}
	return strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(s)
}

// cleanControl 把控制字元與雙向文字控制字元換成空白；tab 與換行由呼叫端決定。
func cleanControl(s string, keepNewline bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNewline:
			return r
		case r == '\t':
			return ' '
		case r == '‍':
			return r
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r), r == ' ', r == ' ':
			return ' '
		}
		return r
	}, s)
}

// SanitizeBody 處理 LLM 寫的內文（NOTE-R8）：原始 HTML 跳脫、`#` 標題改成粗體、setext 標題失效、
// 連結只留 http／https／mailto、`[[…]]` 與 `![[…]]` 只留 keepCue 回報為 true 的 `[[cue:N]]`、
// 會執行的程式碼區塊改成 text、行內的 Dataview 查詢失效；沒有結束的程式碼區塊補上結尾。
func SanitizeBody(s string, keepCue func(n int) bool) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = cleanControl(s, true)
	var out []string
	var text []string // 目前累積、程式碼區塊以外的行
	var fence string  // 開啟中的程式碼區塊標記（``` 或 ~~~），空字串代表不在區塊中
	flush := func() {
		for _, l := range sanitizeText(text, keepCue) {
			if setextRe.MatchString(l) && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				// 上一行接著 === 或 --- 會變成 setext 標題：先空一行，讓它成為分隔線或文字。
				out = append(out, "")
			}
			out = append(out, l)
		}
		text = text[:0]
	}
	for _, line := range strings.Split(s, "\n") {
		if fence != "" {
			if fenceClose(line, fence) {
				out = append(out, strings.TrimSpace(line))
				fence = ""
				continue
			}
			out = append(out, escapeHTML(line))
			continue
		}
		if m := fenceOpen(line); m != nil {
			flush()
			fence = m[2]
			info := strings.TrimSpace(m[3])
			lang, _, _ := strings.Cut(info, " ")
			if dangerousFenceLangs[strings.ToLower(strings.Trim(lang, "{}."))] || strings.HasPrefix(lang, "{") {
				info = "text"
			}
			if info != "" {
				info = " " + escapeHTML(info) // 和標記之間留空白：~~~ 接上 ~ 會變成另一個長度的標記
			}
			out = append(out, m[1]+m[2]+info)
			continue
		}
		text = append(text, line)
	}
	flush()
	if fence != "" {
		out = append(out, fence)
	}
	// 只去掉頭尾的空行：第一行的縮排不能去掉（四個空白的縮排是程式碼，去掉之後可能變成標題）。
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// sanitizeText 處理程式碼區塊以外連續的幾行：先逐行處理，再以整段處理跨行的連結（CommonMark 允許 `(` 後面換行），
// 改變了就再逐行處理一次，直到不再改變（每次改變字串都變短，所以會結束）。
func sanitizeText(lines []string, keepCue func(n int) bool) []string {
	cur := make([]string, len(lines))
	for i, l := range lines {
		cur[i] = sanitizeLine(l, keepCue)
	}
	for {
		joined := strings.Join(cur, "\n")
		again := sanitizeLinks(joined)
		if again == joined {
			return cur
		}
		cur = strings.Split(again, "\n")
		for i, l := range cur {
			cur[i] = sanitizeLine(l, keepCue)
		}
	}
}

// sanitizeLine 處理程式碼區塊以外的一行。
func sanitizeLine(line string, keepCue func(n int) bool) string {
	line = escapeHTML(line)
	line = stripLinks(line, keepCue)
	// 標題放在連結之後處理：去掉連結之後才露出來的 # 也要改成粗體。
	if m := headingRe.FindStringSubmatch(line); m != nil {
		text := strings.TrimSpace(strings.TrimRight(m[2], "# \t"))
		if text == "" {
			line = ""
		} else {
			line = "**" + text + "**"
		}
	}
	if m := refDefRe.FindStringSubmatchIndex(line); m != nil {
		// 參考式連結的定義：讓它變成文字，連結的網址就不會被套用。
		line = line[:m[3]] + `\` + line[m[3]:]
	}
	if fenceOpen(line) != nil {
		// 去掉連結之後變成程式碼區塊的開頭：跳脫成文字。
		i := len(line) - len(strings.TrimLeft(line, " "))
		line = line[:i] + `\` + line[i:]
	}
	return neutralizeInlineQueries(line)
}

// stripLinks 反覆處理 wikilink 與連結直到不再改變：去掉連結之後可能露出新的 [[ 或連結。
// 超過次數仍在改變時（刻意構造的輸入），把剩下的方括號全部跳脫成文字（保留的 [[cue:N]] 除外）。
func stripLinks(s string, keepCue func(n int) bool) string {
	for range 32 {
		next := sanitizeLinks(stripWikilinks(s, keepCue))
		if next == s {
			return s
		}
		s = next
	}
	return escapeBrackets(s)
}

// escapeBrackets 把方括號跳脫成文字，保留的 [[cue:N]] 不變。
func escapeBrackets(s string) string {
	var kept []string
	s = CueRefRe.ReplaceAllStringFunc(s, func(m string) string {
		kept = append(kept, m)
		return "\x00" + strconv.Itoa(len(kept)-1) + "\x00"
	})
	s = strings.NewReplacer(`\[`, `\[`, `\]`, `\]`, "[", `\[`, "]", `\]`).Replace(s)
	for k, m := range kept {
		s = strings.Replace(s, "\x00"+strconv.Itoa(k)+"\x00", m, 1)
	}
	return s
}

// stripWikilinks 去掉 LLM 寫的 `[[…]]` 與 `![[…]]`，只留 keepCue 允許的 `[[cue:N]]`。
func stripWikilinks(s string, keepCue func(n int) bool) string {
	// 每一輪去掉最內層；有改變時字串一定變短，所以迴圈會結束。
	for {
		changed := false
		s = wikiRe.ReplaceAllStringFunc(s, func(m string) string {
			inner := wikiRe.FindStringSubmatch(m)[1]
			if !strings.HasPrefix(m, "!") {
				if c := cueRefRe.FindStringSubmatch(inner); c != nil && keepCue != nil {
					if n, err := strconv.Atoi(c[1]); err == nil && keepCue(n) {
						return m
					}
				}
			}
			changed = true
			return inner
		})
		if !changed {
			break
		}
	}
	if !strings.Contains(s, "[[") && !strings.Contains(s, "]]") {
		return s
	}
	// 剩下的 [[、]]（例如包住保留的 cue 引用）跳脫成文字；保留的 [[cue:N]] 先換成佔位字串（控制字元已在前面清掉）。
	var kept []string
	s = CueRefRe.ReplaceAllStringFunc(s, func(m string) string {
		kept = append(kept, m)
		return "\x00" + strconv.Itoa(len(kept)-1) + "\x00"
	})
	s = strings.NewReplacer("[[", `\[\[`, "]]", `\]\]`).Replace(s)
	for k, m := range kept {
		s = strings.Replace(s, "\x00"+strconv.Itoa(k)+"\x00", m, 1)
	}
	return s
}

// neutralizeInlineQueries 讓行內的 Dataview 查詢（`= …`、`$= …`）不會執行。
func neutralizeInlineQueries(s string) string {
	if !strings.Contains(s, "`") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteByte(s[i])
		if s[i] != '`' {
			continue
		}
		rest := s[i+1:]
		switch {
		case strings.HasPrefix(rest, "="):
			b.WriteByte('\\')
		case strings.HasPrefix(rest, "$="):
			b.WriteByte('\\')
		}
	}
	return b.String()
}

// allowedScheme 回報連結的網址是不是 http、https 或 mailto（NOTE-R8）。
func allowedScheme(dest string) bool {
	d := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, dest))
	return strings.HasPrefix(d, "http://") || strings.HasPrefix(d, "https://") || strings.HasPrefix(d, "mailto:")
}

// sanitizeLinks 處理行內的 [文字](網址) 與 ![替代文字](網址)：網址不是 http／https／mailto 時只留下文字；
// 圖片不嵌入外部網址，改成一般連結。
func sanitizeLinks(s string) string {
	if !strings.Contains(s, "](") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			// 跳脫的字元（例如 \[）不會開始連結。
			b.WriteString(s[i : i+2])
			i += 2
			continue
		}
		if s[i] != '[' {
			b.WriteByte(s[i])
			i++
			continue
		}
		image := i > 0 && s[i-1] == '!'
		closeText := matchBracket(s, i)
		if closeText < 0 || closeText+1 >= len(s) || s[closeText+1] != '(' {
			b.WriteByte(s[i])
			i++
			continue
		}
		closeDest := matchParen(s, closeText+1)
		if closeDest < 0 {
			b.WriteByte(s[i])
			i++
			continue
		}
		text := sanitizeLinks(s[i+1 : closeText])
		inside := strings.TrimSpace(s[closeText+2 : closeDest])
		dest, _, _ := strings.Cut(inside, " ")
		dest = strings.Trim(dest, "<>")
		if image {
			// 去掉前面已經寫出的 '!'。
			str := b.String()
			b.Reset()
			b.WriteString(str[:len(str)-1])
		}
		if allowedScheme(dest) && !strings.ContainsAny(dest, "()") {
			b.WriteString("[" + text + "](" + dest + ")")
		} else {
			b.WriteString(text)
		}
		i = closeDest + 1
	}
	return b.String()
}

// matchBracket 回傳 s[open] 的 '[' 對應的 ']' 位置；沒有時回傳 -1。
func matchBracket(s string, open int) int {
	depth := 0
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// matchParen 回傳 s[open] 的 '(' 對應的 ')' 位置（允許巢狀的括號）；沒有時回傳 -1。
func matchParen(s string, open int) int {
	depth := 0
	for j := open; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// SanitizeInline 處理放在一行之內的不可信文字（標題、頻道、圖說、自我檢驗的項目）：
// 換行與控制字元換成空白、原始 HTML 跳脫、去掉 `[[…]]`、連結只留文字或安全的網址，再合併連續空白。
func SanitizeInline(s string) string {
	s = cleanControl(s, false)
	s = strings.ReplaceAll(s, "\n", " ")
	s = escapeHTML(s)
	s = stripLinks(s, nil)
	s = neutralizeInlineQueries(s)
	return strings.Join(strings.Fields(s), " ")
}

// TableCell 是放進 Markdown 表格的一格：SanitizeInline 之後再跳脫 `|`。
func TableCell(s string) string {
	return strings.ReplaceAll(SanitizeInline(s), "|", `\|`)
}

// Emphasis 是放進斜體（*…*）中的文字：SanitizeInline 之後跳脫 `*` 與 `_`。
func Emphasis(s string) string {
	return strings.NewReplacer("*", `\*`, "_", `\_`).Replace(SanitizeInline(s))
}
