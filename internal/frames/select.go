package frames

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Cue 是選圖用到的一句字幕（毫秒）。
type Cue struct {
	StartMS, EndMS int64
	Text           string
}

// Segment 是不參與選圖的片段（SponsorBlock，FRM-R6），[StartMS, EndMS)。
type Segment struct{ StartMS, EndMS int64 }

// InSegment 回報 at 是否落在任何片段內（含起點、不含終點）。
func InSegment(at int64, segs []Segment) bool {
	for _, s := range segs {
		if at >= s.StartMS && at < s.EndMS {
			return true
		}
	}
	return false
}

// HintScore 回傳候選的規則分數（FRM-R5）：場景分數，前後 HintWindowMS 內有 cue 含提示詞（不分大小寫）時加 HintBonus。
func HintScore(c Candidate, cues []Cue, hints []string) float64 {
	score := c.Score
	lo, hi := c.AtMS-HintWindowMS, c.AtMS+HintWindowMS
	for _, q := range cues {
		if q.EndMS < lo || q.StartMS > hi {
			continue
		}
		text := strings.ToLower(q.Text)
		for _, h := range hints {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" && strings.Contains(text, h) {
				return score + HintBonus
			}
		}
	}
	return score
}

// SelectRules 依分數取前 n 張（同分取較早的），回傳選中的索引並依時間（索引）排序。
// eligible[i] 為 false 的候選不參與（例如在 SponsorBlock 片段內）。
func SelectRules(scores []float64, eligible []bool, n int) []int {
	var idx []int
	for i := range scores {
		if i < len(eligible) && eligible[i] {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
	if len(idx) > n {
		idx = idx[:max(n, 0)]
	}
	sort.Ints(idx)
	return idx
}

// MaxCaptionRunes 是圖說的上限（FRM-R9）。
const MaxCaptionRunes = 120

// CleanCaption 去掉換行與控制字元，連續空白合成一個，超過 MaxCaptionRunes 時截斷。
func CleanCaption(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.Is(unicode.Cf, r) { // 零寬、方向控制等格式字元
			continue
		}
		if space {
			if n >= MaxCaptionRunes {
				break
			}
			b.WriteRune(' ')
			n++
			space = false
		}
		if n >= MaxCaptionRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// CueAt 回傳 at 時間的 cue 文字：涵蓋 at 的 cue，沒有時取開始時間最接近、相差不超過 HintWindowMS 的。
func CueAt(at int64, cues []Cue) string {
	best, bestD := "", int64(-1)
	for _, q := range cues {
		if at >= q.StartMS && at < max(q.EndMS, q.StartMS+1) {
			return q.Text
		}
		d := q.StartMS - at
		if d < 0 {
			d = at - q.EndMS
		}
		if d <= HintWindowMS && (bestD < 0 || d < bestD) {
			best, bestD = q.Text, d
		}
	}
	return best
}

// Chapter 是章節的起點與標題。
type Chapter struct {
	StartMS int64
	Title   string
}

// ChapterAt 回傳起點不晚於 at 的最後一個章節標題；沒有時為空（FRM-R9）。
func ChapterAt(at int64, chapters []Chapter) string {
	title, start := "", int64(-1)
	for _, c := range chapters {
		if c.StartMS <= at && c.StartMS >= start {
			title, start = c.Title, c.StartMS
		}
	}
	return title
}

// Pick 是 vision 模型的一個選擇（FRM-R7）。
type Pick struct {
	Sheet   int    `json:"sheet"`
	N       int    `json:"n"`
	Caption string `json:"caption"`
}

// maxPicksBytes 是模型輸出的大小上限（不可信的輸入）。
const maxPicksBytes = 256 << 10

// ParsePicks 解析 `{picks: [{sheet, n, caption}]}`；不是這個形狀時回傳錯誤（英文以外的說明給使用者，見 ValidatePicks）。
func ParsePicks(raw []byte) ([]Pick, error) {
	if len(raw) > maxPicksBytes {
		return nil, fmt.Errorf("output is larger than %d bytes", maxPicksBytes)
	}
	var v struct {
		Picks []Pick `json:"picks"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if v.Picks == nil {
		return nil, fmt.Errorf("picks is missing")
	}
	return v.Picks, nil
}

// ValidatePicks 對照拼圖檢查選擇（FRM-R7）：sheet 與 n 必須存在、不能重複、張數不能超過 n。
// sizes[k] 是第 k+1 張拼圖的格數。回傳的錯誤訊息會附在重試的請求中（例如「picks[0]：第 1 張拼圖沒有編號 12」）。
func ValidatePicks(picks []Pick, sizes []int, limit int) error {
	if len(picks) > limit {
		return fmt.Errorf("picks：選了 %d 張，最多 %d 張", len(picks), limit)
	}
	seen := map[[2]int]bool{}
	for i, p := range picks {
		switch {
		case p.Sheet < 1 || p.Sheet > len(sizes):
			return fmt.Errorf("picks[%d]：沒有第 %d 張拼圖（共 %d 張）", i, p.Sheet, len(sizes))
		case p.N < 1 || p.N > sizes[p.Sheet-1]:
			return fmt.Errorf("picks[%d]：第 %d 張拼圖沒有編號 %d", i, p.Sheet, p.N)
		case seen[[2]int{p.Sheet, p.N}]:
			return fmt.Errorf("picks[%d]：第 %d 張拼圖的編號 %d 重複", i, p.Sheet, p.N)
		}
		seen[[2]int{p.Sheet, p.N}] = true
	}
	return nil
}
