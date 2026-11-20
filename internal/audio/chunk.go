// Package audio 是長音訊切塊與接回的純邏輯（SRC-R12、ADR-0016）：
// 決定塊數與切點、算出每塊的時間範圍，並把各塊的轉錄結果接成單一時間軸。
// 這裡不呼叫任何外部工具；ffmpeg 的呼叫在 internal/media。
package audio

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 切塊的預設參數（M0 用真實的 ffmpeg 與 Groq 驗證）。
const (
	// Safety 是塊數估算時對單檔上限的安全係數：以上限的 90% 估算，避免壓縮後的塊剛好超過上限。
	Safety = 0.9
	// CutWindow 是找靜音的視窗寬度：切點前後各 CutWindow/2 秒。
	CutWindow = 60.0
	// Overlap 是相鄰兩塊的重疊秒數：每個切點前後各多取 Overlap/2 秒。
	Overlap = 10.0
)

// ChunkCount 回傳大小 size 的音訊要切成幾塊才不超過 limit：以 limit*safety 估算，至少 1 塊。
// limit 或 safety 不合理（<= 0）時回傳 1。
func ChunkCount(size, limit int64, safety float64) int {
	per := float64(limit) * safety
	if size <= 0 || per <= 0 {
		return 1
	}
	return max(1, int(math.Ceil(float64(size)/per)))
}

// PickCut 在 target 前後各 window/2 秒內，回傳離 target 最近的靜音中點；視窗內沒有靜音時回傳 target。
// 距離相同時取較早的一個。
func PickCut(target float64, silences []media.Silence, window float64) float64 {
	best, bestDist := target, math.Inf(1)
	for _, s := range silences {
		mid := (s.StartS + s.EndS) / 2
		d := math.Abs(mid - target)
		if d > window/2 {
			continue
		}
		if d < bestDist || (d == bestDist && mid < best) {
			best, bestDist = mid, d
		}
	}
	return best
}

// Chunk 是切出的一塊：StartS 與 EndS 是實際取出的範圍（含重疊），
// OwnStartS 與 OwnEndS 是這一塊負責的範圍（[OwnStartS, OwnEndS)，最後一塊含終點）。
type Chunk struct {
	StartS, EndS       float64
	OwnStartS, OwnEndS float64
}

// Chunks 依總長 d 與切點 cuts 算出各塊的範圍；每個切點前後各多取 overlap/2 秒。
// n 個切點得到 n+1 塊：第一塊取 [0, B1+overlap/2]，中間的取 [Bk-overlap/2, Bk+1+overlap/2]，
// 最後一塊取 [Bn-overlap/2, d]；負責範圍是 [Bk, Bk+1)。
// cuts 會先排序，落在 (0, d) 之外與重複的切點被略過；沒有有效的切點時只有一塊。
func Chunks(d float64, cuts []float64, overlap float64) []Chunk {
	var bs []float64
	for _, c := range slices.Sorted(slices.Values(cuts)) {
		if c <= 0 || c >= d || (len(bs) > 0 && c <= bs[len(bs)-1]) {
			continue
		}
		bs = append(bs, c)
	}
	half := overlap / 2
	edges := append(append([]float64{0}, bs...), d)
	out := make([]Chunk, 0, len(edges)-1)
	for i := 0; i+1 < len(edges); i++ {
		// 範圍不超出 [0, d]。
		out = append(out, Chunk{
			StartS: max(0, edges[i]-half), EndS: min(d, edges[i+1]+half),
			OwnStartS: edges[i], OwnEndS: edges[i+1],
		})
	}
	return out
}

// Stitch 把各塊的轉錄結果接成單一時間軸；parts[i] 是 chunks[i] 的 cue，時間相對該塊的起點 StartS。
//
//   - 每個 cue 加上該塊的 StartS，並依中點歸屬：中點落在 Own 區間的才保留，重疊區的重複轉錄因此被丟掉。
//     第一塊的下界與最後一塊的上界不設限。
//   - 時間重疊、而且正規化（忽略大小寫、標點與空白）後文字相同的連續 cue 只留第一句。
//   - 時間單調：start 不早於前一句的 end，end 不早於 start。
//   - 重新編號 1..N。
func Stitch(chunks []Chunk, parts [][]subtitle.Cue) []subtitle.Cue {
	var out []subtitle.Cue
	for i, c := range chunks {
		if i >= len(parts) {
			break
		}
		shift := int64(math.Round(c.StartS * 1000))
		lo, hi := int64(math.Round(c.OwnStartS*1000)), int64(math.Round(c.OwnEndS*1000))
		for _, cue := range parts[i] {
			cue.StartMS += shift
			cue.EndMS += shift
			if len(cue.Words) > 0 {
				ws := make([]subtitle.Word, len(cue.Words))
				for j, w := range cue.Words {
					w.StartMS += shift
					w.EndMS += shift
					ws[j] = w
				}
				cue.Words = ws
			}
			mid := (cue.StartMS + cue.EndMS) / 2
			if (i > 0 && mid < lo) || (i < len(chunks)-1 && mid >= hi) {
				continue
			}
			if n := len(out); n > 0 && cue.StartMS < out[n-1].EndMS && normalize(cue.Text) == normalize(out[n-1].Text) {
				out[n-1].EndMS = max(out[n-1].EndMS, cue.EndMS)
				continue
			}
			out = append(out, cue)
		}
	}
	var prevEnd int64
	for i := range out {
		out[i].StartMS = max(out[i].StartMS, prevEnd)
		out[i].EndMS = max(out[i].EndMS, out[i].StartMS)
		out[i].Index = i + 1
		prevEnd = out[i].EndMS
	}
	return out
}

// normalize 回傳比對用的文字：小寫，只留字母與數字。
func normalize(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}
