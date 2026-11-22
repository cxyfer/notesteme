package pipeline

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

// sanitizeAgentText 處理 Agent 送來的 model 與 reason（不可信輸入，NFR-SEC-6）：
// 不會 panic，結果是合法的 UTF-8、沒有控制字元與 Cf、rune 數不超過 limit。
func FuzzSanitizeAgentText(f *testing.F) {
	for _, s := range []string{"", "claude-x", " a\tb\n", "\x1b[31mred", "a​b‮", "\xff\xfe", "字字字字字"} {
		f.Add(s, 4)
	}
	f.Fuzz(func(t *testing.T, s string, limit int) {
		if limit < 0 || limit > 1<<12 {
			return
		}
		got := sanitizeAgentText(s, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("不是合法的 UTF-8：%q", got)
		}
		if n := utf8.RuneCountInString(got); n > limit {
			t.Fatalf("rune 數 %d 超過 %d：%q", n, limit, got)
		}
		for _, r := range got {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("含有控制字元 %U：%q", r, got)
			}
		}
	})
}
