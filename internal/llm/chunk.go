package llm

import "time"

// 切 chunk 的預設值（LLM-R1、R2）。
const (
	DefaultMaxTokens   = 4000
	DefaultOverlapCues = 3
	DefaultContextCues = 5
	// DefaultMaxValidationRetries 是驗證失敗後最多再試的次數（LLM-R5）。
	DefaultMaxValidationRetries = 2
)

// ChunkParams 是 llm.chunk 的設定。
type ChunkParams struct {
	MaxTokens   int `json:"max_tokens"`
	OverlapCues int `json:"overlap_cues"`
	ContextCues int `json:"context_cues"`
}

// DefaultChunkParams 回傳預設值。
func DefaultChunkParams() ChunkParams {
	return ChunkParams{MaxTokens: DefaultMaxTokens, OverlapCues: DefaultOverlapCues, ContextCues: DefaultContextCues}
}

// PlanCue 是切 chunk 需要的一句字幕：開始時間（判斷屬於哪個章節）與文字（估算 token）。
type PlanCue struct {
	Start time.Duration
	Text  string
}

// Chunk 是一個 chunk 的範圍。句號從 1 起算，範圍含兩端。
type Chunk struct {
	Index int `json:"chunk"`
	// From、To 是這個 chunk 要處理的句子，包含開頭與前一個 chunk 重疊的 Overlap 句。
	From int `json:"from"`
	To   int `json:"to"`
	// Overlap 是開頭與前一個 chunk 重疊的句數；重疊區的輸出以前一個 chunk 為準（LLM-R1）。
	Overlap int `json:"overlap"`
	// ContextFrom、ContextTo 是前文（只供參考，不屬於輸出範圍，LLM-R2）；沒有前文時都是 0。
	ContextFrom int `json:"context_from"`
	ContextTo   int `json:"context_to"`
}

// Len 回傳這個 chunk 要處理的句數（含重疊句）。
func (c Chunk) Len() int { return c.To - c.From + 1 }

// HasContext 回報有沒有前文。
func (c Chunk) HasContext() bool { return c.ContextFrom > 0 }

// PlanChunks 依 LLM-R1、R2 切 chunk：
//   - 有章節時，先依章節分段：每句依開始時間歸到「開始時間不晚於它的最後一個章節」（第一個章節之前的句子歸第一章）；
//     章節之間不重疊。單一章節超過 max_tokens 時，章節內再依 token 切。
//   - 依 token 切時，每個 chunk 新的句子合計不超過 max_tokens（至少一句），重疊的句子不計入（L2）；
//     相鄰 chunk 重疊 overlap_cues 句，但不會越過前一個 chunk 的開頭。
//   - 每個 chunk 的前文是它開頭之前的 context_cues 句（不分章節）。
//
// chapters 是章節的開始時間，依時間排序；空的代表沒有章節。
func PlanChunks(cues []PlanCue, chapters []time.Duration, p ChunkParams) []Chunk {
	if len(cues) == 0 {
		return nil
	}
	if p.MaxTokens <= 0 {
		p.MaxTokens = DefaultMaxTokens
	}
	p.OverlapCues = max(p.OverlapCues, 0)
	p.ContextCues = max(p.ContextCues, 0)

	var out []Chunk
	for _, seg := range segments(cues, chapters) {
		out = splitSegment(out, cues, seg[0], seg[1], p)
	}
	for i := range out {
		out[i].Index = i + 1
		if p.ContextCues > 0 && out[i].From > 1 {
			out[i].ContextFrom = max(1, out[i].From-p.ContextCues)
			out[i].ContextTo = out[i].From - 1
		}
	}
	return out
}

// segments 依章節把句子分成連續的區段（句號從 1 起算，含兩端）；沒有章節時整份是一段。
func segments(cues []PlanCue, chapters []time.Duration) [][2]int {
	if len(chapters) == 0 {
		return [][2]int{{1, len(cues)}}
	}
	chapterOf := func(start time.Duration) int {
		ch := 0
		for i, s := range chapters {
			if s <= start {
				ch = i
			}
		}
		return ch
	}
	var segs [][2]int
	cur := chapterOf(cues[0].Start)
	from := 1
	for i := 1; i < len(cues); i++ {
		// 章節只往後推進：字幕時間倒退（不應發生）時仍留在目前的章節，區段才會是連續的。
		if ch := chapterOf(cues[i].Start); ch > cur {
			segs = append(segs, [2]int{from, i})
			cur, from = ch, i+1
		}
	}
	return append(segs, [2]int{from, len(cues)})
}

// splitSegment 把區段 [a, b] 依 token 切成 chunk，接在 out 之後。區段的第一個 chunk 不重疊。
func splitSegment(out []Chunk, cues []PlanCue, a, b int, p ChunkParams) []Chunk {
	tok := func(n int) int { return EstimateTokens(cues[n-1].Text) }
	start, prevFrom := a, 0
	for start <= b {
		c := Chunk{From: start}
		if prevFrom > 0 {
			// 重疊不越過前一個 chunk 的開頭，chunk 才會一直往前推進。
			c.Overlap = min(p.OverlapCues, start-1-prevFrom)
			c.From = start - c.Overlap
		}
		end, sum := start, tok(start)
		for end+1 <= b && sum+tok(end+1) <= p.MaxTokens {
			end++
			sum += tok(end)
		}
		c.To = end
		out = append(out, c)
		prevFrom, start = c.From, end+1
	}
	return out
}
