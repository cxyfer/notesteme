package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// chapters 的退回原因（CHAP-R3）。
const (
	ReasonChapterCue   = "chapter_cue"   // cue 不在這個 chunk 的輸入中，或沒有嚴格遞增
	ReasonChapterCount = "chapter_count" // 章節數不在 1～max_chapters
	ReasonChapterTitle = "chapter_title" // 標題是空的、超過上限或含換行與控制字元
)

// 章節的上限（CHAP-R3、H4）。
const (
	DefaultMaxChapters = 20
	MaxChapterTitle    = 80 // 去掉頭尾空白後的字元（rune）數
)

// chaptersSchema 是 {chapters: [{cue, title}]} 的 schema（OpenAI strict 相容）：沒有任何時間欄位（ADR-0003）。
func chaptersSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cue":   map[string]any{"type": "integer"},
						"title": map[string]any{"type": "string"},
					},
					"required":             []any{"cue", "title"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"chapters"},
		"additionalProperties": false,
	}
}

// Chapter 是模型提出的一個章節：起點的句號與標題。
type Chapter struct {
	Cue   int    `json:"cue"`
	Title string `json:"title"`
}

// ChaptersOutput 是 chapters 的輸出。
type ChaptersOutput struct {
	Chapters []Chapter `json:"chapters"`
}

// ChaptersSpec 是 chapters 步驟的 Spec（CHAP-R2、R3）。maxChapters 小於 1 時用預設值。
// LLM 只回傳起點的 cue 與標題，時間一律由程式依 cue 換算（ADR-0003、CHAP-R5）。
func ChaptersSpec(maxChapters int) *Spec {
	if maxChapters < 1 {
		maxChapters = DefaultMaxChapters
	}
	return &Spec{
		Stage:  "chapters",
		Schema: chaptersSchema(),
		OutputRules: fmt.Sprintf("輸出 {\"chapters\": [{\"cue\", \"title\"}]}：cue 是章節第一句的句號（cues 中的 i，只能用這次 cues 中出現的句號，依時間順序嚴格遞增）；"+
			"title 是章節標題（1～%d 字，不含換行），用字幕的語言；章節 1～%d 個；不要輸出任何時間。", MaxChapterTitle, maxChapters),
		Check: func(in ChunkInput, out json.RawMessage) *ValidationError {
			_, verr := CheckChapters(in, out, maxChapters)
			return verr
		},
	}
}

// chapterError 建立驗證錯誤；訊息同時記在 Detail 的 error，重試用盡時步驟用它說明原因（CHAP-R4）。
func chapterError(reason, msg string) *ValidationError {
	return &ValidationError{Reason: reason, Message: msg, Detail: map[string]any{"error": msg}}
}

// CheckChapters 驗證 chapters 的輸出並回傳去掉頭尾空白的章節（CHAP-R3）：
// cue 必須是這個 chunk 的 cues 中存在的句號、嚴格遞增；章節數 1～maxChapters；
// 標題去掉頭尾空白後 1～80 字，不含換行與控制字元。
func CheckChapters(in ChunkInput, out json.RawMessage, maxChapters int) ([]Chapter, *ValidationError) {
	var o ChaptersOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	if len(o.Chapters) == 0 {
		return nil, chapterError(ReasonChapterCount, "chapters 至少要有 1 個")
	}
	if len(o.Chapters) > maxChapters {
		return nil, chapterError(ReasonChapterCount, fmt.Sprintf("chapters 有 %d 個，最多 %d 個", len(o.Chapters), maxChapters))
	}
	present := make(map[int]bool, len(in.Cues))
	for _, c := range in.Cues {
		present[c.I] = true
	}
	out2 := make([]Chapter, len(o.Chapters))
	for k, c := range o.Chapters {
		if !present[c.Cue] {
			return nil, chapterError(ReasonChapterCue, fmt.Sprintf("chapters[%d].cue %d 不在輸入中", k, c.Cue))
		}
		if k > 0 && c.Cue <= o.Chapters[k-1].Cue {
			return nil, chapterError(ReasonChapterCue, fmt.Sprintf("chapters[%d].cue 必須遞增", k))
		}
		title := strings.TrimSpace(c.Title)
		switch n := utf8.RuneCountInString(title); {
		case n == 0:
			return nil, chapterError(ReasonChapterTitle, fmt.Sprintf("chapters[%d].title 是空的", k))
		case n > MaxChapterTitle:
			return nil, chapterError(ReasonChapterTitle, fmt.Sprintf("chapters[%d].title 超過 %d 字", k, MaxChapterTitle))
		case strings.ContainsFunc(title, isTitleControl):
			return nil, chapterError(ReasonChapterTitle, fmt.Sprintf("chapters[%d].title 不能含換行或控制字元", k))
		}
		out2[k] = Chapter{Cue: c.Cue, Title: title}
	}
	return out2, nil
}

// isTitleControl 回報 r 是不是標題不能含的字元：換行、控制字元與格式字元（例如雙向文字覆寫）。
// 零寬連接符（U+200D）是組合 emoji 用的，不算。
func isTitleControl(r rune) bool {
	if r == '‍' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' || r == utf8.RuneError
}
