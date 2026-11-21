package subtitle

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 單字級 segment 合併成 cue 的門檻：兩個字之間停頓超過 wordGapMS，或一句超過 maxCueMS 就斷開。
const (
	wordGapMS = 1000
	maxCueMS  = 7000
)

type podcastJSON struct {
	Segments []struct {
		StartTime float64 `json:"startTime"`
		EndTime   float64 `json:"endTime"`
		Body      string  `json:"body"`
	} `json:"segments"`
}

type podcastSeg struct {
	start, end int64
	body       string // 去掉前後空白；換行資訊保留在 breakAfter
	breakAfter bool   // 原文在這個 segment 之後換行
}

// maxSegTimeS 是 segment 時間的上限（秒）：超過 24 小時視為不合法，也避免轉成毫秒時 int64 溢位。
const maxSegTimeS = 24 * 3600

// validSegTime 回報時間是不是有限、非負且不超過 maxSegTimeS。
func validSegTime(sec float64) bool {
	return !math.IsNaN(sec) && !math.IsInf(sec, 0) && sec >= 0 && sec <= maxSegTimeS
}

// ParsePodcastJSON 解析 Podcasting 2.0 的 JSON 字幕（{"segments":[{"startTime","endTime","body"}]}，單位為秒）。
//
// 每個 segment 的 body 都是單一字詞（無空白；含 CJK 時不超過 4 個字）時，視為字詞級的時間：依句末標點、換行、停頓超過 1 秒或一句超過 7 秒
// 合併成 cue，並填入 Words（wordLevel 為 true）。否則一個 segment 一個 cue。
// body 是空白的 segment 略過；結束時間早於開始時間時取開始時間。JSON 不合法，或時間為負、不是有限值、超過 24 小時時回傳錯誤。
func ParsePodcastJSON(data []byte) (cues []Cue, wordLevel bool, err error) {
	var doc podcastJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, fmt.Errorf("subtitle: JSON 字幕不合法：%w", err)
	}
	var segs []podcastSeg
	for i, s := range doc.Segments {
		if !validSegTime(s.StartTime) || !validSegTime(s.EndTime) {
			return nil, false, fmt.Errorf("subtitle: JSON 字幕第 %d 段的時間不合法", i+1)
		}
		body := strings.TrimSpace(s.Body)
		if body == "" {
			continue
		}
		start, end := int64(math.Round(s.StartTime*1000)), int64(math.Round(s.EndTime*1000))
		end = max(end, start)
		segs = append(segs, podcastSeg{start: start, end: end, body: body, breakAfter: strings.HasSuffix(strings.TrimRight(s.Body, " \t"), "\n")})
	}
	slices.SortStableFunc(segs, func(a, b podcastSeg) int {
		switch {
		case a.start < b.start:
			return -1
		case a.start > b.start:
			return 1
		}
		return 0
	})
	if len(segs) == 0 {
		return nil, false, nil
	}

	wordLevel = true
	for _, s := range segs {
		if !isWordBody(s.body) {
			wordLevel = false
			break
		}
	}
	if !wordLevel {
		for _, s := range segs {
			cues = append(cues, Cue{Index: len(cues) + 1, StartMS: s.start, EndMS: s.end, Text: s.body, Timing: TimingSource})
		}
		return cues, false, nil
	}
	return mergeWordSegs(segs), true, nil
}

// mergeWordSegs 把單字級的 segment 合併成 cue。
func mergeWordSegs(segs []podcastSeg) []Cue {
	var (
		cues  []Cue
		words []Word
	)
	flush := func() {
		if len(words) == 0 {
			return
		}
		cues = append(cues, Cue{
			Index: len(cues) + 1, StartMS: words[0].StartMS, EndMS: words[len(words)-1].EndMS,
			Text: joinWords(words), Words: words, Timing: TimingSource,
		})
		words = nil
	}
	for i, s := range segs {
		if len(words) > 0 {
			last := words[len(words)-1]
			if s.start-last.EndMS > wordGapMS || s.end-words[0].StartMS > maxCueMS {
				flush()
			}
		}
		words = append(words, Word{Text: s.body, StartMS: s.start, EndMS: s.end})
		if s.breakAfter || endsSentence(s.body) || i == len(segs)-1 {
			flush()
		}
	}
	return cues
}

// maxCJKWordRunes 是字詞級 segment 中，含 CJK 的 body 最多可有的字數；超過就視為整句。
const maxCJKWordRunes = 4

// isCJK 回報 r 是不是漢字、平假名、片假名或諺文。
func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// isWordBody 回報 body 能不能當成「一個字詞」：沒有空白，且含 CJK 時不超過 maxCJKWordRunes 個字。
// CJK 整句沒有空白可以分辨，所以只要不確定就當成整句（一個 segment 一個 cue）。
func isWordBody(body string) bool {
	if strings.IndexFunc(body, unicode.IsSpace) >= 0 {
		return false
	}
	if strings.IndexFunc(body, isCJK) >= 0 {
		return utf8.RuneCountInString(body) <= maxCJKWordRunes
	}
	return true
}

// joinWords 把字詞接成 cue 的文字：以空格分隔，但相鄰的兩個字只要有一邊是 CJK 就不插入空格。
func joinWords(words []Word) string {
	var b strings.Builder
	var prev rune
	for i, w := range words {
		first, _ := utf8.DecodeRuneInString(w.Text)
		if i > 0 && !isCJK(prev) && !isCJK(first) {
			b.WriteByte(' ')
		}
		b.WriteString(w.Text)
		prev, _ = utf8.DecodeLastRuneInString(w.Text)
	}
	return b.String()
}

// endsSentence 回報字是否以句末標點結尾（收尾的引號與括號不影響）。
func endsSentence(w string) bool {
	w = strings.TrimRight(w, "\"'”’)）」』]")
	if w == "" {
		return false
	}
	switch r := []rune(w)[len([]rune(w))-1]; r {
	case '.', '!', '?', '。', '！', '？', '…':
		return true
	}
	return false
}
