package llm

import (
	"encoding/json"
	"fmt"
)

// correct 的退回原因（LLM-R5、COR-R1）。
const (
	ReasonCueCount = "cue_count" // 輸出的句數與輸入不同
	ReasonCueIndex = "cue_index" // 輸出的句號與輸入不一致（順序不同或編號錯誤）
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

// CorrectSpec 是 correct 步驟的最小 Spec（S3）：輸出與輸入的 cue 一對一（COR-R1），句號依序相同。
// 詞彙表、相似度門檻（COR-R2、R3）與術語候選（COR-R6）屬於 COR 切片，會在這個 Spec 上擴充。
func CorrectSpec() *Spec {
	return &Spec{
		Stage:       "correct",
		Schema:      cuesSchema(),
		OutputRules: "輸出 {\"cues\": [{\"i\", \"text\"}]}：cues 與輸入的 cues 一一對應，句號 i 與順序都相同，不要合併或拆分句子，也不要輸出 context。",
		Check:       checkOneToOne,
	}
}

// checkOneToOne 檢查輸出的句子與輸入一一對應：句數相同、句號依序相同。
func checkOneToOne(in ChunkInput, out json.RawMessage) *ValidationError {
	cues, err := DecodeCues(out)
	if err != nil {
		return &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	if len(cues) != len(in.Cues) {
		return &ValidationError{Reason: ReasonCueCount, Message: fmt.Sprintf(
			"output has %d cues but the input has %d; return exactly one cue for every input cue, with the same i", len(cues), len(in.Cues))}
	}
	for k, c := range cues {
		if c.I != in.Cues[k].I {
			return &ValidationError{Reason: ReasonCueIndex, Message: fmt.Sprintf(
				"cue %d of the output has i=%d but the input cue at that position has i=%d; keep every i and the order unchanged", k+1, c.I, in.Cues[k].I)}
		}
	}
	return nil
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
