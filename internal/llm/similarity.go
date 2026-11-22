package llm

import "strings"

// DefaultMinSimilarity 是 stages.correct.min_similarity 的預設值（COR-R2）。
const DefaultMinSimilarity = 0.6

// maxSimilarityCells 是 Similarity 願意計算的 rune 數乘積上限（約 4000 × 4000）：
// 輸出來自模型（不可信），過長的句子不值得花 O(n·m) 的時間，直接視為差異過大。
const maxSimilarityCells = 16_000_000

// Similarity 回傳兩段文字的相似度（0～1）：以 rune 為單位的 normalized Levenshtein，
// 1 - 編輯距離 / 較長者的長度，比較時忽略大小寫（只改大小寫的校正不算差異）。
// 兩邊都是空字串為 1；只有一邊是空字串為 0；兩段文字太長（超過 maxSimilarityCells）而且不相同時為 0。
// 暫定算法（COR 待決問題，待 M0 樣本與 owner 追認）。
func Similarity(a, b string) float64 {
	if a == b {
		return 1
	}
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	// 現在 ra 是較長者。
	if len(ra) == 0 {
		return 1
	}
	if len(rb) == 0 {
		return 0
	}
	if len(ra)*len(rb) > maxSimilarityCells {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(len(ra))
}
