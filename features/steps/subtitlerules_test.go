package steps

import (
	"strings"
	"testing"
)

// 斷言的排版要能抓到超過行數的句子，否則 `follows the subtitle rules` 恆真。
func TestStepLines(t *testing.T) {
	cases := []struct {
		text string
		max  int
		cjk  bool
		want int
	}{
		{"Welcome everyone, today we talk about running streaming pipelines at scale.", 42, false, 2},
		{strings.Repeat("word ", 30), 42, false, 4},
		{"short", 42, false, 1},
		{"大家好，今天想聊聊我们团队怎么拆分服务。", 25, true, 1},
		{strings.Repeat("服务", 30), 25, true, 3},
		{"a\nb", 42, false, 2},
	}
	for _, tc := range cases {
		if got := stepLines(tc.text, tc.max, tc.cjk); got != tc.want {
			t.Errorf("stepLines(%q) = %d，預期 %d", tc.text, got, tc.want)
		}
	}
}
