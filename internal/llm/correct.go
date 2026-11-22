package llm

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// correct 的退回原因（LLM-R5、COR-R1）。
const (
	ReasonCueCount = "cue_count" // 輸出的句數與輸入不同
	ReasonCueIndex = "cue_index" // 輸出的句號與輸入不一致（順序不同或編號錯誤）
	// ReasonSimilarity：有句子校正後和原文的相似度低於門檻（COR-R2）。
	ReasonSimilarity = "similarity"
)

// cuesSchema 是 {cues: [{i, text}]} 的 schema（OpenAI strict 相容）。
func cuesSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cues": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"i":    map[string]any{"type": "integer"},
						"text": map[string]any{"type": "string"},
					},
					"required":             []any{"i", "text"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []any{"cues"},
		"additionalProperties": false,
	}
}

// CueOutput 是 {cues: [{i, text}]} 形式的輸出（correct 與之後一對一的步驟共用）。
type CueOutput struct {
	Cues []Cue `json:"cues"`
}

// DecodeCues 解出已通過驗證的 {cues} 輸出。
func DecodeCues(out json.RawMessage) ([]Cue, error) {
	var o CueOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, err
	}
	return o.Cues, nil
}

// 術語候選的上限（COR-R6）：模型輸出不可信，寫入前再依這些限制清理（見 SanitizeTermCandidates）。
const (
	MaxTermCandidatesPerChunk = 20
	MaxTermRunes              = 64
)

// correctSchema 是 correct 的輸出 schema：{cues: [{i, text}], term_candidates: [{term}]}（OpenAI strict 相容）。
// term_candidates 在 strict 模式必須列進 required，但對產品是選填（見 Spec.Optional）。
func correctSchema() map[string]any {
	s := cuesSchema()
	props := s["properties"].(map[string]any)
	props["term_candidates"] = map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"term": map[string]any{"type": "string"}},
			"required":             []any{"term"},
			"additionalProperties": false,
		},
	}
	s["required"] = []any{"cues", "term_candidates"}
	return s
}

// CorrectOutput 是 correct 的輸出。
type CorrectOutput struct {
	Cues           []Cue `json:"cues"`
	TermCandidates []struct {
		Term string `json:"term"`
	} `json:"term_candidates"`
}

// DecodeTermCandidates 解出已通過驗證的輸出中的術語候選（沒有這個欄位時回傳 nil）。
func DecodeTermCandidates(out json.RawMessage) ([]string, error) {
	var o CorrectOutput
	if err := json.Unmarshal(out, &o); err != nil {
		return nil, err
	}
	terms := make([]string, 0, len(o.TermCandidates))
	for _, c := range o.TermCandidates {
		terms = append(terms, c.Term)
	}
	return terms, nil
}

// MixedLanguageRule 是原文語言標記為 mixed 時加進系統指示的規則（COR-R5）。
const MixedLanguageRule = "字幕中夾雜多種語言，保留原本的語言，不要翻譯。"

// CorrectSpec 是 correct 步驟的 Spec，相似度門檻用預設值。
func CorrectSpec() *Spec { return CorrectSpecWith(DefaultMinSimilarity) }

// CorrectSpecWith 是 correct 步驟的 Spec：輸出與輸入的 cue 一對一、句號依序相同（COR-R1），
// 每句校正前後的相似度不得低於 minSimilarity（COR-R2）；term_candidates 選填（COR-R6）。
func CorrectSpecWith(minSimilarity float64) *Spec {
	return &Spec{
		Stage:  "correct",
		Schema: correctSchema(),
		OutputRules: "輸出 {\"cues\": [{\"i\", \"text\"}], \"term_candidates\": [{\"term\"}]}：cues 與輸入的 cues 一一對應，句號 i 與順序都相同，" +
			"不要合併或拆分句子，也不要輸出 context；term_candidates 列出字幕中出現、但詞彙表沒有的專有名詞或術語（正確寫法，最多 " +
			fmt.Sprint(MaxTermCandidatesPerChunk) + " 個），沒有就輸出空陣列。",
		Optional:  []string{"term_candidates"},
		MixedRule: MixedLanguageRule,
		Check:     checkCorrect(minSimilarity),
	}
}

// checkCorrect 檢查 correct 的輸出：一對一（句數、句號），再逐句檢查相似度。
func checkCorrect(minSimilarity float64) func(ChunkInput, json.RawMessage) *ValidationError {
	return func(in ChunkInput, out json.RawMessage) *ValidationError {
		cues, verr := oneToOne(in, out)
		if verr != nil {
			return verr
		}
		lowest, at := 1.0, 0
		for k, c := range cues {
			if sim := Similarity(in.Cues[k].Text, c.Text); sim < lowest {
				lowest, at = sim, c.I
			}
		}
		if lowest < minSimilarity {
			return &ValidationError{
				Reason: ReasonSimilarity,
				Message: fmt.Sprintf("cue i=%d is too different from the input (similarity %.2f, the minimum is %.2f); "+
					"fix only typos, names and punctuation, and keep the original wording and meaning", at, lowest, minSimilarity),
				Detail: map[string]any{"similarity": math.Round(lowest*100) / 100, "cue": at},
			}
		}
		return nil
	}
}

// oneToOne 檢查輸出的句子與輸入一一對應：句數相同、句號依序相同；回傳輸出的句子。
func oneToOne(in ChunkInput, out json.RawMessage) ([]Cue, *ValidationError) {
	cues, err := DecodeCues(out)
	if err != nil {
		return nil, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	if len(cues) != len(in.Cues) {
		return nil, &ValidationError{Reason: ReasonCueCount, Message: fmt.Sprintf(
			"output has %d cues but the input has %d; return exactly one cue for every input cue, with the same i", len(cues), len(in.Cues))}
	}
	for k, c := range cues {
		if c.I != in.Cues[k].I {
			return nil, &ValidationError{Reason: ReasonCueIndex, Message: fmt.Sprintf(
				"cue %d of the output has i=%d but the input cue at that position has i=%d; keep every i and the order unchanged", k+1, c.I, in.Cues[k].I)}
		}
	}
	return cues, nil
}

// MergeCues 依 chunk 的順序合併各 chunk 的一對一輸出：每個 chunk 開頭的重疊句以前一個 chunk 為準（LLM-R1）。
// outputs[k] 是 chunks[k] 的輸出（句數必須等於 chunk 的句數）；回傳的句子依句號排序、不重複。
func MergeCues(chunks []Chunk, outputs [][]Cue) ([]Cue, error) {
	if len(chunks) != len(outputs) {
		return nil, fmt.Errorf("chunk 數 %d 與輸出數 %d 不同", len(chunks), len(outputs))
	}
	var out []Cue
	next := 1
	for k, c := range chunks {
		if len(outputs[k]) != c.Len() {
			return nil, fmt.Errorf("chunk %d 的輸出有 %d 句，預期 %d 句", c.Index, len(outputs[k]), c.Len())
		}
		for j, cue := range outputs[k] {
			n := c.From + j
			if n < next {
				continue // 重疊區：前一個 chunk 已經輸出
			}
			if n != next {
				return nil, fmt.Errorf("chunk %d 從第 %d 句開始，前面缺少第 %d 句", c.Index, n, next)
			}
			out = append(out, Cue{I: n, Text: cue.Text})
			next++
		}
	}
	return out, nil
}

// SanitizeTermCandidates 清理模型提出的術語候選（COR-R6，模型輸出是不可信輸入）：
// 控制字元（含換行與 tab）換成空白、合併連續空白並去掉前後空白；空字串、超過 MaxTermRunes 個字元的丟棄；
// 大小寫相同的重複只留第一個；最多留 limit 個。
func SanitizeTermCandidates(terms []string, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range terms {
		t = cleanTerm(t)
		key := strings.ToLower(t)
		if t == "" || utf8.RuneCountInString(t) > MaxTermRunes || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// cleanTerm 把控制字元與格式字元（例如 RTL 標記）換成空白，再合併連續空白。
func cleanTerm(s string) string {
	b := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(b), " ")
}
