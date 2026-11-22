// Package llm 是所有 LLM 步驟共用的執行機制（LLM 規格、ADR-0002、ADR-0019）：
// 切 chunk、組 prompt、約束與驗證輸出、重試與退回、provider 的 wire format 與限流。
//
// 這個套件不依賴 pipeline 與資料庫：切 chunk（PlanChunks）、組 prompt（BuildPrompt）、
// 驗證輸出（Spec.Validate）與執行一個 chunk（RunChunk）都可以單獨呼叫，
// 之後的 Agent executor（AGT）在 agent 提交結果時走同一個驗證函式。
package llm

import (
	"unicode"
	"unicode/utf8"
)

// EstimateTokens 依 L2 估算一句的 token 數（LLM 待決問題 L2，owner 已同意）：
// CJK（漢字、假名、諺文）一字算 1；其他字元每 4 個算 1，無條件進位；每句再加 1。
// 只是切 chunk 用的估算，不是 provider 實際計費的 token 數。
func EstimateTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if r == utf8.RuneError {
			other++
			continue
		}
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4 + 1
}

// isCJK 回報 r 是不是漢字、假名或諺文。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// EstimateText 估算一段任意文字（例如整個 prompt）的 token 數，給限流的 TPM 預扣用；規則同 EstimateTokens，但不加每句的 1。
func EstimateText(text string) int {
	return EstimateTokens(text) - 1
}
