// Package vocab 解析 docs/specs/steps.md 的步驟句型，並把句型轉成 regexp。
//
// 解析規則與 scripts/spec/check_steps.py 相同；句型轉換規則見 ADR-0015。
package vocab

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Pattern 是 steps.md 中的一個句型。
type Pattern struct {
	// Keyword 是 Given、When 或 Then。
	Keyword string
	// Template 是去掉關鍵字前綴的句型，例如 `the clock is at {string}`。
	Template string
}

// Full 回傳含關鍵字的完整句型，與 steps.md 的寫法相同。
func (p Pattern) Full() string { return p.Keyword + " " + p.Template }

var patternLine = regexp.MustCompile("(?m)^\\| `((?:Given|When|Then) [^`]+)` \\|")

// Parse 取出 steps.md 內容中的全部句型，依出現順序回傳。
func Parse(markdown string) []Pattern {
	var out []Pattern
	for _, m := range patternLine.FindAllStringSubmatch(markdown, -1) {
		kw, tmpl, _ := strings.Cut(m[1], " ")
		out = append(out, Pattern{Keyword: kw, Template: tmpl})
	}
	return out
}

// Load 讀取並解析 steps.md。
func Load(path string) ([]Pattern, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read steps vocabulary: %w", err)
	}
	return Parse(string(b)), nil
}

// Templates 回傳不重複的 Template 集合（Given 與 Then 句型相同時只算一個）。
func Templates(patterns []Pattern) map[string]bool {
	set := make(map[string]bool, len(patterns))
	for _, p := range patterns {
		set[p.Template] = true
	}
	return set
}

// countNouns 是接在 {int} 後面、單複數都接受的名詞（寫成複數形）。
// 用明確清單而不是「以 s 結尾的字」，避免誤把 is、fails 這類動詞當成名詞。
var countNouns = map[string]bool{
	"items": true, "cues": true, "versions": true, "tasks": true,
	"requests": true, "times": true, "attempts": true, "bytes": true,
	"parts": true, "days": true,
}

var placeholder = regexp.MustCompile(`\{(string|int|word)\}`)

// Compile 把句型轉成會完整比對步驟文字的 regexp：
//
//	{string} -> "([^"]*)"
//	{int}    -> (-?\d+)
//	{word}   -> (\S+)
//
// {int} 後面緊接的複數名詞（見 countNouns）的 s 可以省略，例如 `{int} items` 也比對 `1 item`。
// 句型中的其他文字逐字比對。
func Compile(template string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")

	rest := template
	afterInt := false
	for rest != "" {
		loc := placeholder.FindStringSubmatchIndex(rest)
		literal := rest
		if loc != nil {
			literal = rest[:loc[0]]
		}
		b.WriteString(quoteLiteral(literal, afterInt))
		if loc == nil {
			break
		}
		switch rest[loc[2]:loc[3]] {
		case "string":
			b.WriteString(`"([^"]*)"`)
		case "int":
			b.WriteString(`(-?\d+)`)
		case "word":
			b.WriteString(`(\S+)`)
		}
		afterInt = rest[loc[2]:loc[3]] == "int"
		rest = rest[loc[1]:]
	}

	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// quoteLiteral 跳脫字面文字；afterInt 為 true 時，開頭的 " <名詞>" 允許省略複數的 s。
func quoteLiteral(s string, afterInt bool) string {
	if afterInt && strings.HasPrefix(s, " ") {
		word, tail, found := strings.Cut(s[1:], " ")
		if countNouns[word] {
			if found {
				tail = " " + tail
			}
			return regexp.QuoteMeta(" "+strings.TrimSuffix(word, "s")) + "s?" + regexp.QuoteMeta(tail)
		}
	}
	return regexp.QuoteMeta(s)
}
