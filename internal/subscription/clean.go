package subscription

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxTitleRunes 是清理後標題的長度上限（SUB-R11）。
const maxTitleRunes = 300

var (
	// scriptStyle 是 <script>、<style> 元素與其內容；沒有結束標籤時刪到結尾。
	scriptStyle = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?(?:</(?:script|style)\s*>|$)`)
	// tagRe 是其他標籤（保留文字）；也刪掉沒有關上的 "<…" 尾巴。
	tagRe = regexp.MustCompile(`(?s)</?[A-Za-z!/?][^>]*(?:>|$)`)
)

// CleanText 清理 feed 與探索結果中的標題與說明（SUB-R11）：解碼 HTML entity 一次、移除 <script>、<style>
// 與其內容、移除其他標籤（保留文字）、移除控制字元與雙向控制字元、合併連續空白並去掉前後空白，
// 最長 300 個字元（超過時截斷並加上「…」）。
func CleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = html.UnescapeString(s)
	s = scriptStyle.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r), isBidiControl(r), r == 0xFEFF:
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > maxTitleRunes {
		r := []rune(out)
		out = strings.TrimSpace(string(r[:maxTitleRunes])) + "…"
	}
	return out
}

// isBidiControl 是雙向控制字元：U+202A～U+202E、U+2066～U+2069，以及 LRM、RLM、ALM。
func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) || r == 0x200E || r == 0x200F || r == 0x061C
}
