package steps

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 字幕規則（plan 第 8.5 節）在斷言這一側獨立計算，不呼叫 production 的規則檢查，否則斷言恆真。
// CJK 每行字數是範圍 20–25，斷言取寬鬆的一端 25；production 的預設值不能比它寬鬆。
var stepSubtitleRules = map[string]struct {
	maxChars, maxLines int
	minMS, maxMS       int64
}{
	"latin": {maxChars: 42, maxLines: 2, minMS: 800, maxMS: 7000},
	"cjk":   {maxChars: 25, maxLines: 2, minMS: 800, maxMS: 7000},
}

// everyCueFollowsRules 實作 `every cue of … follows the subtitle rules for …`：每個 cue 的時長在範圍內，
// 文字依每行字數排版（拉丁文字依空白換行，CJK 依字數）不超過行數上限。沒有任何 cue 視為失敗。
func (s *Scenario) everyCueFollowsRules(variant, ref, script string) error {
	rules, ok := stepSubtitleRules[script]
	if !ok {
		return fmt.Errorf("文字系統 %q 不正確，應是 latin 或 cjk", script)
	}
	id, err := s.itemID(ref)
	if err != nil {
		return err
	}
	var got cuesJSON
	if err := s.apiJSON(http.MethodGet, "/api/items/"+url.PathEscape(id)+"/cues?"+url.Values{"variant": {variant}}.Encode(), nil, &got); err != nil {
		return fmt.Errorf("取得 %s 的 %s 字幕：%w", ref, variant, err)
	}
	if len(got.Cues) == 0 {
		return fmt.Errorf("%s 的 %s 字幕沒有任何 cue，無法確認字幕規則", ref, variant)
	}
	for i, c := range got.Cues {
		start, err1 := subtitle.ParseTimecode(c.Start)
		end, err2 := subtitle.ParseTimecode(c.End)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("%s 的 %s 字幕第 %d 句的時間不正確：%s-%s", ref, variant, i+1, c.Start, c.End)
		}
		if d := end - start; d < rules.minMS || d > rules.maxMS {
			return fmt.Errorf("%s 的 %s 字幕第 %d 句顯示 %d ms，不在 %d–%d ms 之內：%q", ref, variant, i+1, d, rules.minMS, rules.maxMS, c.Text)
		}
		lines := stepLines(c.Text, rules.maxChars, script == "cjk")
		if lines > rules.maxLines {
			return fmt.Errorf("%s 的 %s 字幕第 %d 句每行 %d 字要排成 %d 行，超過 %d 行：%q", ref, variant, i+1, rules.maxChars, lines, rules.maxLines, c.Text)
		}
	}
	return nil
}

// stepLines 計算排版行數：CJK 依去掉空白後的字數；拉丁文字依空白貪婪換行（單一字詞超過一行時依字數切）。
func stepLines(text string, maxChars int, cjk bool) int {
	total := 0
	for _, line := range strings.Split(text, "\n") {
		if cjk {
			n := utf8.RuneCountInString(strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return -1
				}
				return r
			}, line))
			total += (n + maxChars - 1) / maxChars
			continue
		}
		cur := 0
		for _, w := range strings.Fields(line) {
			n := utf8.RuneCountInString(w)
			if cur > 0 && cur+1+n <= maxChars {
				cur += 1 + n
				continue
			}
			if cur > 0 {
				total++
			}
			for n > maxChars {
				total++
				n -= maxChars
			}
			cur = n
		}
		if cur > 0 {
			total++
		}
	}
	return total
}
