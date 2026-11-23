package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// StageSummary 是摘要的 LLM 步驟名稱（tool 或 schema 名稱 notesteme_summary，DIG-R3）。
const StageSummary = "summary"

// summary 的退回原因（DIG-R3）。
const (
	ReasonSummaryText   = "summary_text"   // summary 是空的、太長或含控制字元
	ReasonSummaryPoints = "summary_points" // key_points 的數量或內容不符
	ReasonSummaryTitle  = "summary_title"  // title_translation 是空字串、太長或不是單行
)

// 摘要的上限（DIG-R3）。長度一律以去掉頭尾空白後的字元（rune）數計算。
const (
	MaxSummaryText     = 1200
	MaxSummaryPoints   = 7
	MaxSummaryPoint    = 200
	MaxSummaryTitleLen = 200
)

// summarySchema 是 {summary, key_points, title_translation} 的 schema（OpenAI strict 相容；title_translation 可以是 null）。
func summarySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":           map[string]any{"type": "string"},
			"key_points":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"title_translation": map[string]any{"type": []any{"string", "null"}},
		},
		"required":             []any{"summary", "key_points", "title_translation"},
		"additionalProperties": false,
	}
}

// SummaryOutput 是 summary 的輸出（已去掉頭尾空白）。
type SummaryOutput struct {
	Summary          string   `json:"summary"`
	KeyPoints        []string `json:"key_points"`
	TitleTranslation *string  `json:"title_translation"`
}

// SummarySpec 是 summary 步驟的 Spec（DIG-R2、R3）：一部影音只送一個請求，輸出是摘要、重點與標題譯文。
func SummarySpec() *Spec {
	return &Spec{
		Stage:  StageSummary,
		Schema: summarySchema(),
		OutputRules: fmt.Sprintf("輸出 {\"summary\", \"key_points\", \"title_translation\"}：summary 是整部影片的摘要（1～%d 字，可以換行）；"+
			"key_points 是 0～%d 個重點（每個 1～%d 字、單行）；title_translation 是影片標題翻成摘要語言的譯文（1～%d 字、單行），"+
			"標題已經是摘要語言時給 null。摘要、重點與譯文都用系統指示中的摘要語言；不要輸出任何時間。",
			MaxSummaryText, MaxSummaryPoints, MaxSummaryPoint, MaxSummaryTitleLen),
		Check: func(_ ChunkInput, out json.RawMessage) *ValidationError {
			_, verr := CheckSummary(out)
			return verr
		},
	}
}

// summaryError 建立驗證錯誤；訊息同時記在 Detail 的 error，重試用盡時步驟用它說明原因（DIG-R4）。
func summaryError(reason, msg string) *ValidationError {
	return &ValidationError{Reason: reason, Message: msg, Detail: map[string]any{"error": msg}}
}

// CheckSummary 驗證 summary 的輸出（DIG-R3）並回傳去掉頭尾空白的結果：
// summary 1～1200 字，除了換行之外不含控制字元；key_points 0～7 項，每項 1～200 字、單行；
// title_translation 是 null 或 1～200 字、單行。
func CheckSummary(out json.RawMessage) (SummaryOutput, *ValidationError) {
	var o SummaryOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return SummaryOutput{}, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	text := strings.TrimSpace(o.Summary)
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return SummaryOutput{}, summaryError(ReasonSummaryText, "summary 是空的")
	case n > MaxSummaryText:
		return SummaryOutput{}, summaryError(ReasonSummaryText, fmt.Sprintf("summary 有 %d 字，上限是 %d 字", n, MaxSummaryText))
	case strings.ContainsFunc(text, isSummaryControl):
		return SummaryOutput{}, summaryError(ReasonSummaryText, "summary 不能含換行以外的控制字元")
	}
	if len(o.KeyPoints) > MaxSummaryPoints {
		return SummaryOutput{}, summaryError(ReasonSummaryPoints, fmt.Sprintf("key_points 有 %d 項，上限是 %d 項", len(o.KeyPoints), MaxSummaryPoints))
	}
	points := make([]string, len(o.KeyPoints))
	for k, p := range o.KeyPoints {
		if msg := checkLine(p, MaxSummaryPoint); msg != "" {
			return SummaryOutput{}, summaryError(ReasonSummaryPoints, fmt.Sprintf("key_points[%d] %s", k, msg))
		}
		points[k] = strings.TrimSpace(p)
	}
	var title *string
	if o.TitleTranslation != nil {
		if msg := checkLine(*o.TitleTranslation, MaxSummaryTitleLen); msg != "" {
			return SummaryOutput{}, summaryError(ReasonSummaryTitle, "title_translation "+msg)
		}
		t := strings.TrimSpace(*o.TitleTranslation)
		title = &t
	}
	return SummaryOutput{Summary: text, KeyPoints: points, TitleTranslation: title}, nil
}

// checkLine 檢查單行文字：去掉頭尾空白後 1～limit 字，不含換行與控制字元；不符時回傳說明。
func checkLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	switch n := utf8.RuneCountInString(s); {
	case n == 0:
		return "是空的"
	case n > limit:
		return fmt.Sprintf("有 %d 字，上限是 %d 字", n, limit)
	case strings.ContainsFunc(s, isTitleControl):
		return "不能含換行或控制字元"
	}
	return ""
}

// isSummaryControl 回報 r 是不是摘要不能含的字元：換行（\n）以外的控制字元、格式字元與無效的 UTF-8。
func isSummaryControl(r rune) bool {
	if r == '\n' || r == '\u200d' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError
}
