package subtitle

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// minCueMS 為 cue 的最短長度（NRM-R2、NRM-R4）。
	minCueMS int64 = 300
	// overlapGapMS 為重疊修正時，前一句 end 與後一句 start 之間保留的間隔（NRM-R4）。
	overlapGapMS int64 = 40
	// rollingGapMS 為滾動字幕去重（NRM-R1）允許的最大間隔：後一句的 start 不晚於前一句的 end 加此值。
	// 暫定值，M0 量測後再調整。
	rollingGapMS int64 = 1000
)

var (
	// htmlTag 只匹配真正的字幕格式標籤，避免吃掉 "a<b and b>c" 這類一般文字：
	//   - i、b、u、s、c、ruby、rt：不帶屬性，只允許 VTT class（<c.color>）
	//   - font、v、lang：可帶屬性或註記（<font color=..>、<v Bob>、<lang en>）
	//   - VTT 字詞時間標記 <00:00:01.000>
	htmlTag = regexp.MustCompile(`(?i)</?(?:(?:i|b|u|s|c|ruby|rt)(?:\.[^>\s]*)*|(?:font|v|lang)(?:[.\s][^>]*)?)>|<(?:\d{1,4}:)?\d{1,2}:\d{2}[.,]\d{1,3}>`)
	assTag  = regexp.MustCompile(`\{\\[^}]*\}`)
)

// Normalize 把原始 cue 整理成乾淨、時間單調的序列，不修改輸入。
// 處理順序：
//  1. NRM-R3 去除格式標記與重複空白（保留 [Music]、♪）
//  2. NRM-R5 移除空白 cue
//  3. NRM-R4 依 start 排序
//  4. NRM-R1 滾動字幕去重（要在 R2、R4 之前：滾動字幕彼此重疊，先去重才不會被 R4 當成重疊而截短）
//  5. NRM-R2 碎句併入前一句
//  6. NRM-R4 修正重疊，修正後過短的與後一句合併
//  7. 重新編號（從 1 起算）
//
// 合併與修正都保留各字詞原本的時間（NRM-R6）。
func Normalize(cues []Cue) []Cue {
	out := cloneCues(cues)
	for _, step := range normalizeSteps {
		out = step(out)
	}
	for i := range out {
		out[i].Index = i + 1
	}
	return out
}

// normalizeSteps 為依序執行的步驟。
var normalizeSteps = []func([]Cue) []Cue{
	cleanText,     // NRM-R3
	dropEmpty,     // NRM-R5
	sortByStart,   // NRM-R4
	mergeRolling,  // NRM-R1
	mergeFragment, // NRM-R2
	fixOverlaps,   // NRM-R4
}

func cloneCues(in []Cue) []Cue {
	out := make([]Cue, len(in))
	for i, c := range in {
		if c.Words != nil {
			c.Words = append([]Word(nil), c.Words...)
		}
		if c.Speaker != nil {
			s := *c.Speaker
			c.Speaker = &s
		}
		if c.Timing == "" {
			c.Timing = TimingSource
		}
		out[i] = c
	}
	return out
}

// cleanText 實作 NRM-R3，並確保 end 不早於 start。
func cleanText(cues []Cue) []Cue {
	for i := range cues {
		t := assTag.ReplaceAllString(cues[i].Text, "")
		t = htmlTag.ReplaceAllString(t, "")
		cues[i].Text = strings.Join(strings.Fields(t), " ")
		if cues[i].EndMS < cues[i].StartMS {
			cues[i].EndMS = cues[i].StartMS
		}
	}
	return cues
}

// dropEmpty 實作 NRM-R5。
func dropEmpty(cues []Cue) []Cue {
	out := cues[:0]
	for _, c := range cues {
		if c.Text != "" {
			out = append(out, c)
		}
	}
	return out
}

// sortByStart 依 start、end 穩定排序。
func sortByStart(cues []Cue) []Cue {
	sort.SliceStable(cues, func(i, j int) bool {
		if cues[i].StartMS != cues[j].StartMS {
			return cues[i].StartMS < cues[j].StartMS
		}
		return cues[i].EndMS < cues[j].EndMS
	})
	return cues
}

// mergeRolling 實作 NRM-R1：後一句的文字以前一句的文字開頭（包括完全相同），
// 且前綴結束在字詞邊界、兩句時間重疊或相鄰（見 rollingGapMS）時，兩句合併成一句，
// 文字取後一句，時間取前一句的 start 到後一句的 end；連續多句依序合併（合併結果再和下一句比對）。
// 字詞時間依 NRM-R6 原樣保留並去重。
func mergeRolling(cues []Cue) []Cue {
	out := make([]Cue, 0, len(cues))
	for _, c := range cues {
		if n := len(out); n > 0 && isRollingContinuation(out[n-1], c) {
			prev := out[n-1]
			merged := mergeCues(prev, c)
			merged.Text = c.Text
			out[n-1] = merged
			continue
		}
		out = append(out, c)
	}
	return out
}

// isRollingContinuation 判斷 next 是否為 prev 的滾動延伸（NRM-R1 的前綴、字詞邊界與時間條件）。
func isRollingContinuation(prev, next Cue) bool {
	if !strings.HasPrefix(next.Text, prev.Text) {
		return false
	}
	// cue 已依 start 排序，所以 next.StartMS <= prev.EndMS 即為時間重疊。
	if next.StartMS > prev.EndMS+rollingGapMS {
		return false
	}
	return endsAtWordBoundary(prev.Text, next.Text[len(prev.Text):])
}

// endsAtWordBoundary 判斷前綴 prefix 之後接 rest 時是否落在字詞邊界：
// rest 為空、以空白或標點開頭，或前綴的最後一個字、rest 的第一個字是不以空白分詞的文字。
// 這類文字包含中文（Han）、日文（Hiragana、Katakana），
// 另外也把韓文（Hangul）當成邊界：現代韓文雖以空白分詞，但助詞黏在字詞後面，
// 滾動字幕可能在助詞前後截斷，以空白判斷會漏掉合併。
func endsAtWordBoundary(prefix, rest string) bool {
	if rest == "" {
		return true
	}
	first, _ := utf8.DecodeRuneInString(rest)
	if unicode.IsSpace(first) || unicode.IsPunct(first) {
		return true
	}
	last, _ := utf8.DecodeLastRuneInString(prefix)
	return isUnspacedScript(first) || isUnspacedScript(last)
}

// isUnspacedScript 回傳 r 是否為中日韓文字。
func isUnspacedScript(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// mergeFragment 實作 NRM-R2：長度不到 300ms 或只有標點的 cue 併入前一句。
// 第一句沒有前一句可併，維持原樣。
func mergeFragment(cues []Cue) []Cue {
	out := make([]Cue, 0, len(cues))
	for _, c := range cues {
		if n := len(out); n > 0 && (c.EndMS-c.StartMS < minCueMS || isPunctOnly(c.Text)) {
			out[n-1] = mergeCues(out[n-1], c)
			continue
		}
		out = append(out, c)
	}
	return out
}

// fixOverlaps 實作 NRM-R4 的重疊修正：前一句 end 改為後一句 start 減 40ms；
// 修正後短於 300ms 就和後一句合併。結果中每句的 start 都不早於前一句的 end。
func fixOverlaps(cues []Cue) []Cue {
	out := make([]Cue, 0, len(cues))
	for _, c := range cues {
		for len(out) > 0 && out[len(out)-1].EndMS > c.StartMS {
			prev := out[len(out)-1]
			origEnd := prev.EndMS
			prev.EndMS = c.StartMS - overlapGapMS
			if prev.EndMS-prev.StartMS >= minCueMS {
				out[len(out)-1] = prev
				break
			}
			// 太短：與後一句合併，合併結果再和更前面的 cue 比對。
			out = out[:len(out)-1]
			prev.EndMS = origEnd
			c = mergeCues(prev, c)
		}
		out = append(out, c)
	}
	return out
}

// mergeCues 把 b 併入 a：時間取 a.start 到較晚的 end，文字接合，
// 字詞時間原樣保留（文字與時間都相同的重複字詞只留一個），Timing 標為 merged。
func mergeCues(a, b Cue) Cue {
	a.Text = joinText(a.Text, b.Text)
	if b.EndMS > a.EndMS {
		a.EndMS = b.EndMS
	}
	a.Words = mergeWords(a.Words, b.Words)
	a.Timing = TimingMerged
	return a
}

// joinText 接合文字：只有標點時直接接在後面，其餘以一個空白連接。
func joinText(a, b string) string {
	if isPunctOnly(b) {
		return a + b
	}
	return a + " " + b
}

func isPunctOnly(s string) bool {
	found := false
	for _, r := range s {
		switch {
		case unicode.IsPunct(r):
			found = true
		case unicode.IsSpace(r):
		default:
			return false
		}
	}
	return found
}

// mergeWords 串接兩組字詞並去除文字與時間都相同的重複字詞。
func mergeWords(a, b []Word) []Word {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[Word]bool, len(a)+len(b))
	out := make([]Word, 0, len(a)+len(b))
	for _, w := range append(append([]Word(nil), a...), b...) {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
