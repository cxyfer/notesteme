package pipeline

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/subtitle"
)

// SRC-R4 的品質檢查原因（source_probes.reason，state 為 rejected）。
const (
	reasonLowCoverage = "low_coverage"
	reasonRepetition  = "repetition"
	reasonMusicOnly   = "music_only"
)

// SRC-R4 的預設門檻（依 M0 結果調整）。比較都不含等號：剛好 50%、30%、80% 都算通過。
const (
	// minCoverage：cue 涵蓋的時間（聯集）低於片長的這個比例時為 low_coverage。
	minCoverage = 0.5
	// maxRepetition：重複句（與前面某一句的文字相同）的比例超過這個值時為 repetition。
	maxRepetition = 0.3
	// maxMusicOnly：只有音樂標記的 cue 比例超過這個值時為 music_only。
	maxMusicOnly = 0.8
	// minRepetitionCues：判斷重複句至少要有幾句（不含音樂標記）；太短的結果一句重複就超過 30%，不判斷。
	minRepetitionCues = 5
)

// qualityReason 依 SRC-R4 檢查一個來源的結果，回傳不採用的原因；通過時回傳空字串。
// durationS 是涵蓋率的分母（片長，秒）；不知道（0、負數或不是有限值）時不檢查涵蓋率。
// 檢查順序固定：music_only → low_coverage → repetition（音樂標記不算重複句，所以先判斷）。
// language_mismatch 由呼叫端在這之前檢查。
func qualityReason(cues []subtitle.Cue, durationS float64) string {
	if len(cues) == 0 {
		return ""
	}
	music := 0
	for _, c := range cues {
		if musicOnly(c.Text) {
			music++
		}
	}
	if float64(music)/float64(len(cues)) > maxMusicOnly {
		return reasonMusicOnly
	}
	if r, ok := coverage(cues, durationS); ok && r < minCoverage {
		return reasonLowCoverage
	}
	if repetitionRatio(cues) > maxRepetition {
		return reasonRepetition
	}
	return ""
}

// coverage 回傳 cue 時間區間的聯集占片長的比例；區間先裁到 [0, durationS]，重疊只算一次。
// durationS 不合理時 ok 為 false。
func coverage(cues []subtitle.Cue, durationS float64) (ratio float64, ok bool) {
	if !(durationS > 0) || math.IsInf(durationS, 0) {
		return 0, false
	}
	limit := int64(math.Round(durationS * 1000))
	type span struct{ a, b int64 }
	spans := make([]span, 0, len(cues))
	for _, c := range cues {
		a, b := max(c.StartMS, 0), min(c.EndMS, limit)
		if b > a {
			spans = append(spans, span{a, b})
		}
	}
	slices.SortFunc(spans, func(x, y span) int {
		switch {
		case x.a < y.a:
			return -1
		case x.a > y.a:
			return 1
		}
		return 0
	})
	var total, end int64
	for _, s := range spans {
		if s.a > end {
			total += s.b - s.a
			end = s.b
		} else if s.b > end {
			total += s.b - end
			end = s.b
		}
	}
	return float64(total) / float64(limit), true
}

// repetitionRatio 回傳重複句的比例：文字（去掉前後空白、壓縮空白、轉小寫；數字與標點保留）
// 與前面某一句相同的句子數，除以不含音樂標記、不是空白的句子數。句子少於 minRepetitionCues 時為 0。
func repetitionRatio(cues []subtitle.Cue) float64 {
	seen := map[string]bool{}
	n, dup := 0, 0
	for _, c := range cues {
		if musicOnly(c.Text) {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(c.Text), " "))
		if key == "" {
			continue
		}
		n++
		if seen[key] {
			dup++
		}
		seen[key] = true
	}
	if n < minRepetitionCues {
		return 0
	}
	return float64(dup) / float64(n)
}

// bracketed 是一組括號內的文字（半形、全形與 CJK 括號）。
var bracketed = regexp.MustCompile(`[\[(（【〔［「『]([^\]\)）】〕］」』]*)[\]\)）】〕］」』]`)

// musicWords 是括號內代表音樂的標記（小寫），例如 [Music]、（音樂）、[BGM]。
var musicWords = map[string]bool{
	"music": true, "musique": true, "música": true, "musica": true, "musik": true, "muziek": true, "музыка": true,
	"音樂": true, "音乐": true, "音楽": true, "음악": true, "bgm": true, "背景音樂": true, "背景音乐": true,
	"music playing": true, "upbeat music": true, "instrumental": true, "applause and music": true,
}

// musicNotes 是常見的音符符號。
const musicNotes = "♪♫♬♩🎵🎶"

// musicOnly 回報一句是否只有音樂標記：去掉代表音樂的括號標記與音符、標點與空白之後什麼都不剩，
// 而且至少去掉了一個標記。「♪ 歌詞 ♪」還有歌詞，不算。
func musicOnly(text string) bool {
	marked := false
	rest := bracketed.ReplaceAllStringFunc(text, func(m string) string {
		inner := bracketed.FindStringSubmatch(m)[1]
		inner = strings.ToLower(strings.Join(strings.Fields(strings.Trim(inner, musicNotes+" ")), " "))
		if musicWords[inner] {
			marked = true
			return " "
		}
		return m
	})
	for _, r := range rest {
		switch {
		case strings.ContainsRune(musicNotes, r):
			marked = true
		case unicode.IsSpace(r), unicode.IsPunct(r), unicode.IsSymbol(r):
		default:
			return false
		}
	}
	return marked
}
