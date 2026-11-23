// Package frames 是關鍵畫面（FRM）的確定性部分：解析場景偵測的輸出、補上密度下限、
// 以縮圖去重、決定張數、規則選圖、驗證 vision 模型的選擇與組出拼圖。
//
// 這個套件不執行任何外部程式，也不讀寫檔案；ffmpeg 的呼叫在 internal/media，步驟的流程在 internal/pipeline。
// ffmpeg 的 stderr 與模型的輸出都是不可信的輸入，解析時只取需要的欄位，不合法的部分略過或回報錯誤。
package frames

import (
	"bufio"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Params 是 stages.frames 中影響候選與選圖的參數（FRM 介面）；零值用 Defaults 補上。
type Params struct {
	SceneThreshold  float64
	MinIntervalS    int
	DedupeWindow    int
	DedupeThreshold float64
	MaxFrames       int
	PerMinute       float64
	HintWords       []string
}

// DefaultHintWords 是 FRM-R5 的預設提示詞。
var DefaultHintWords = []string{"看這裡", "如圖", "這張圖", "look at this", "as you can see", "this diagram", "this chart"}

// Defaults 回傳 FRM 規格的預設參數（F2：暫定值）。
func Defaults() Params {
	return Params{
		SceneThreshold: 0.3, MinIntervalS: 60, DedupeWindow: 5, DedupeThreshold: 0.05,
		MaxFrames: 20, PerMinute: 1, HintWords: append([]string(nil), DefaultHintWords...),
	}
}

// HintBonus 是候選附近有提示詞時加的分數（FRM-R5）；HintWindowMS 是前後的範圍。
const (
	HintBonus    = 0.5
	HintWindowMS = 10_000
)

// Scene 是場景偵測回報的一個畫面變化。
type Scene struct {
	AtMS  int64
	Score float64
}

// maxScenes 是解析場景偵測輸出時最多保留的列數：不可信的輸出不能讓記憶體無限成長。
const maxScenes = 10_000

// ParseShowinfo 解析 ffmpeg `select='gt(scene,T)',showinfo,metadata=print` 的 stderr：
// showinfo 的 `pts_time:` 與其後 metadata 的 `lavfi.scene_score=` 配成一組。
// 只有時間、沒有分數的畫面分數為 0；時間或分數不是有限的非負數時略過。結果依時間排序並去掉重複的時間。
func ParseShowinfo(stderr string) []Scene {
	var out []Scene
	pending := -1 // 最近一個還沒有分數的畫面在 out 中的位置
	sc := bufio.NewScanner(strings.NewReader(stderr))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() && len(out) < maxScenes {
		line := sc.Text()
		switch {
		case strings.Contains(line, "Parsed_showinfo") && strings.Contains(line, " n:"):
			t, ok := fieldAfter(line, "pts_time:")
			if !ok {
				continue
			}
			out = append(out, Scene{AtMS: int64(math.Round(t * 1000))})
			pending = len(out) - 1
		case strings.Contains(line, "lavfi.scene_score="):
			v, ok := fieldAfter(line, "lavfi.scene_score=")
			if ok && pending >= 0 && v <= 1 {
				out[pending].Score = v
			}
			pending = -1
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AtMS < out[j].AtMS })
	dedup := out[:0]
	for _, s := range out {
		if n := len(dedup); n > 0 && dedup[n-1].AtMS == s.AtMS {
			continue
		}
		dedup = append(dedup, s)
	}
	return dedup
}

// fieldAfter 取出 key 之後到下一個空白之前的數字；必須是有限的非負數。
func fieldAfter(line, key string) (float64, bool) {
	i := strings.Index(line, key)
	if i < 0 {
		return 0, false
	}
	rest := line[i+len(key):]
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1e9 {
		return 0, false
	}
	return v, true
}

// Candidate 是一個候選畫面的時間與場景分數；Fill 為 true 代表是密度下限補上的（FRM-R2），分數為 0。
type Candidate struct {
	AtMS  int64
	Score float64
	Fill  bool
}

// maxCandidates 是候選數的上限（含補上的）：很長的影片配上很小的間隔時，避免擷取上萬張。
const maxCandidates = 2000

// Candidates 依 FRM-R2 組出候選：分數大於門檻的場景，再補上密度下限。
// 相鄰兩個候選（含片頭 0 與片尾 durationMS）相隔超過 minInterval 時，從前一個候選起每隔 minInterval 補一張，
// 直到距離下一個候選不到 minInterval。片長之後的場景不使用。
func Candidates(scenes []Scene, durationMS int64, p Params) []Candidate {
	interval := int64(p.MinIntervalS) * 1000
	var base []Candidate
	for _, s := range scenes {
		if s.Score > p.SceneThreshold && s.AtMS >= 0 && (durationMS <= 0 || s.AtMS < durationMS) {
			base = append(base, Candidate{AtMS: s.AtMS, Score: s.Score})
		}
	}
	if interval <= 0 || durationMS <= 0 {
		return capCandidates(base)
	}
	var out []Candidate
	prev := int64(0)
	for _, c := range base {
		out = appendFill(out, prev, c.AtMS, interval)
		out = append(out, c)
		prev = c.AtMS
		if len(out) >= maxCandidates {
			return capCandidates(out)
		}
	}
	return capCandidates(appendFill(out, prev, durationMS, interval))
}

// appendFill 在 (from, to) 之間補上密度下限的候選：相隔超過 interval 時從 from 起每隔 interval 補一張，
// 直到補上的最後一張與 to 相隔不超過 interval（FRM-R2 的例子：1 秒與片尾 300 秒之間補 61、121、181、241）。
// 片頭 0 只當作邊界，本身不是候選。
func appendFill(out []Candidate, from, to, interval int64) []Candidate {
	if to-from <= interval {
		return out
	}
	for t := from + interval; t < to && len(out) < maxCandidates; t += interval {
		out = append(out, Candidate{AtMS: t, Fill: true})
	}
	return out
}

func capCandidates(c []Candidate) []Candidate {
	if len(c) > maxCandidates {
		return c[:maxCandidates]
	}
	return c
}

// Band 是依片長建議的張數範圍（FRM-R4，沿用 Video Notes/AGENTS.md）。
type Band struct{ Min, Max int }

// BandFor 回傳片長（毫秒）對應的範圍：未滿 10 分鐘 5–8、10–30 分鐘 8–12、30–60 分鐘 12–16、60 分鐘以上 16–20。
func BandFor(durationMS int64) Band {
	m := float64(durationMS) / 60000
	switch {
	case m < 10:
		return Band{5, 8}
	case m < 30:
		return Band{8, 12}
	case m < 60:
		return Band{12, 16}
	}
	return Band{16, 20}
}

// Target 回傳目標張數 N = min(max_frames, ceil(per_minute × 分鐘數), 範圍上限)，至少 1（FRM-R4）。
func Target(durationMS int64, p Params) int {
	n := p.MaxFrames
	if per := int(math.Ceil(p.PerMinute * float64(durationMS) / 60000)); per < n {
		n = per
	}
	if b := BandFor(durationMS); b.Max < n {
		n = b.Max
	}
	return max(n, 1)
}
