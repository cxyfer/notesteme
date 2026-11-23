// Package search 是字幕全文搜尋（SRCH）不依賴資料庫的部分：查詢字串的驗證、
// 大小寫摺疊、FTS5 片語的引號處理與命中範圍（highlights）的計算。
//
// 查詢字串是不可信的輸入：一律當成一個字面上的片語（SRCH-R4），不會變成 FTS5 的查詢語法。
package search

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxQueryRunes 是查詢去掉前後空白之後的長度上限（code point，SRCH-R5）。
const MaxQueryRunes = 200

// IndexMinRunes 是使用 trigram 索引的最短查詢長度；更短的查詢改用掃描（SRCH-R3）。
const IndexMinRunes = 3

// 查詢長度與分頁的預設與上限（SRCH-R5）。
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// 查詢不合法時給使用者的訊息（SRCH-R5）。
const (
	MsgEmpty    = "請輸入要搜尋的文字。"
	MsgTooLong  = "搜尋文字最多 200 個字。"
	MsgBadChars = "搜尋文字含有無法使用的字元。"
)

// QueryError 是查詢字串不合法的原因；Error 回傳給使用者看的訊息。
type QueryError struct{ Msg string }

func (e *QueryError) Error() string { return e.Msg }

// NormalizeQuery 去掉前後空白並驗證查詢（SRCH-R5）：空字串、超過 200 個 code point、
// 不是合法的 UTF-8 或含控制字元（\p{Cc}，Tab 除外）都是錯誤。回傳去掉空白之後的查詢（尚未摺疊大小寫）。
func NormalizeQuery(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", &QueryError{MsgEmpty}
	}
	if !utf8.ValidString(q) {
		return "", &QueryError{MsgBadChars}
	}
	if utf8.RuneCountInString(q) > MaxQueryRunes {
		return "", &QueryError{MsgTooLong}
	}
	for _, r := range q {
		if r != '\t' && unicode.Is(unicode.Cc, r) {
			return "", &QueryError{MsgBadChars}
		}
	}
	return q, nil
}

// Fold 是索引與查詢共用的大小寫摺疊（SRCH S3）：Go 的 strings.ToLower，與 LIB-R3 一致。
// 它逐個 code point 轉換，所以摺疊前後的 code point 數相同，命中範圍可以直接對回原文。
// 不做 NFKC、不做簡繁轉換（SRCH-R9）。
func Fold(s string) string { return strings.ToLower(s) }

// UseIndex 回報這個（已摺疊的）查詢是否走 trigram 索引；否則用 instr 掃描（SRCH-R3）。
func UseIndex(folded string) bool { return utf8.RuneCountInString(folded) >= IndexMinRunes }

// Phrase 把查詢包成 FTS5 的一個字面片語（SRCH-R4）：內部的 " 換成 ""，整串包上一對 "。
// 不加欄位前綴、不加 *，所以 OR、NEAR(…)、text_fold:、^ 等都照字面比對。結果要以參數綁定傳給 MATCH。
func Phrase(folded string) string {
	return `"` + strings.ReplaceAll(folded, `"`, `""`) + `"`
}

// Span 是命中範圍在原文中的位移，單位是 UTF-16 code unit（SRCH-R6、S2），End 不含。
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Highlights 回傳 folded（已摺疊的查詢）在 text 中所有不重疊的命中位置，由左到右（SRCH-R6）。
// 比對的是 Fold(text)，位置以 code point 對回原文，再換算成 UTF-16 code unit。
// 查詢為空或沒有命中時回傳空的切片（不是 nil）。
func Highlights(text, folded string) []Span {
	out := []Span{}
	q := []rune(folded)
	if len(q) == 0 {
		return out
	}
	orig := []rune(text)
	low := []rune(Fold(text))
	if len(low) != len(orig) {
		// Fold 逐個 code point 轉換，長度不會改變；保險起見，對不上時不標示。
		return out
	}
	// offs[i] 是第 i 個 code point 之前的 UTF-16 長度。
	offs := make([]int, len(orig)+1)
	for i, r := range orig {
		n := utf16.RuneLen(r)
		if n < 0 {
			n = 1 // 無效的 code point 在 JSON 中會變成 U+FFFD（一個 code unit）
		}
		offs[i+1] = offs[i] + n
	}
	for i := 0; i+len(q) <= len(low); {
		if runesEqual(low[i:i+len(q)], q) {
			out = append(out, Span{Start: offs[i], End: offs[i+len(q)]})
			i += len(q)
			continue
		}
		i++
	}
	return out
}

func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// variantRank 是同一個語言中變體的優先順序，數字越小越優先（SRCH-R1、S8）。
// 不在表中的變體不索引。
var variantRank = map[string]int{
	"edited":     0,
	"corrected":  1,
	"segmented":  2,
	"normalized": 3,
	"translated": 4,
	"source":     5,
}

// Indexable 回報這個字幕變體是否可以被索引。
func Indexable(variant string) bool {
	_, ok := variantRank[variant]
	return ok
}

// Better 回報變體 a 的優先順序是否高於 b（SRCH-R1）。b 不可索引時，任何可索引的 a 都比較好。
func Better(a, b string) bool {
	ra, okA := variantRank[a]
	rb, okB := variantRank[b]
	if !okA {
		return false
	}
	return !okB || ra < rb
}
