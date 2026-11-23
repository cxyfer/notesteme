package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// 退回原因（產出的 fallbacks[].reason，LLM-R5）。步驟可以定義自己的原因（例如 correct 的 cue_count）。
const (
	ReasonInvalidJSON = "invalid_json" // 不是 JSON 物件，或沒有強制的 tool_use
	ReasonSchema      = "schema"       // 不符合 schema，包含資料區塊以外的欄位（LLM-R9）
)

// ValidationError 是輸出沒有通過驗證：Reason 是退回時記錄的原因，Message 是附在下一次請求中的錯誤說明（英文，給模型看）。
type ValidationError struct {
	Reason  string
	Message string
	// Detail 是退回時一併記錄的補充資料（例如 correct 的相似度）；沒有時是 nil。
	Detail map[string]any
	// Output 是符合 schema、但沒有通過步驟 Check 的輸出（compact）；JSON 或 schema 錯誤時是 nil。
	// 退回時步驟可以保留其中通過驗證的部分（例如 translate 只把沒有通過的句子退回原文，TRN-R7）。
	Output json.RawMessage
}

func (e *ValidationError) Error() string { return e.Reason + ": " + e.Message }

// ChunkInput 是一個 chunk 送給模型的資料，也是驗證輸出時的對照。
type ChunkInput struct {
	Chunk   Chunk
	Chunks  int
	Context []Cue
	Cues    []Cue
}

// Spec 是一個 LLM 步驟自己的部分：輸出的 JSON schema、輸出規則與驗證（LLM 規格的「各步驟只定義自己的輸出格式與驗證規則」）。
// 之後的切片（segment、translate、correct 的相似度與術語候選）各自建立 Spec，共用同一套執行機制。
type Spec struct {
	// Stage 是步驟名稱，強制的 tool 或 schema 名稱是 notesteme_<Stage>。
	Stage string
	// Schema 是輸出的 JSON schema。OpenAI strict 模式要求每一層 object 都是 additionalProperties: false，
	// 而且 required 列出所有屬性（ValidateStrictSchema 檢查）。
	Schema map[string]any
	// OutputRules 是附在系統指示中的輸出規則（一行）。
	OutputRules string
	// Optional 是 schema 的 required 中、但產品視為選填的最上層欄位：strict 模式要求 required 列出所有屬性，
	// 所以 schema 照列；驗證時輸出缺少這些欄位仍然通過。
	Optional []string
	// MixedRule 是原文語言為 mixed 時加進系統指示的規則（例如 correct 的「不要翻譯」）；空字串代表這個步驟沒有。
	MixedRule string
	// Check 在輸出符合 schema 之後，對照 chunk 的輸入做步驟自己的驗證（例如 correct 的句數）；nil 代表沒有。
	Check func(in ChunkInput, out json.RawMessage) *ValidationError
	// GeminiSchema 是送給 Gemini 的 responseSchema（OpenAPI 的子集：沒有 additionalProperties，型別用大寫，GEM-R4）；
	// 只有可以由 type 為 gemini 的 provider 執行的步驟才有，驗證仍用 Schema。
	GeminiSchema map[string]any
}

// Ready 檢查 Spec 可以使用：有步驟名稱，schema 符合 OpenAI strict 模式（ValidateStrictSchema）。
// 登記步驟時呼叫（pipeline.llmStages），不符時是程式錯誤。
func (s *Spec) Ready() error {
	if s == nil || s.Stage == "" {
		return errors.New("llm: Spec 沒有步驟名稱")
	}
	if err := ValidateStrictSchema(s.Schema); err != nil {
		return fmt.Errorf("llm: %s 的 schema 不符合 strict 模式：%w", s.Stage, err)
	}
	return nil
}

// ToolName 回傳強制的 tool 或 schema 名稱。
func (s *Spec) ToolName() string { return ToolPrefix + s.Stage }

// Validate 驗證模型的輸出（LLM-R4、R9）：必須是 JSON 物件、符合 schema（不能有 schema 以外的欄位），
// 再交給步驟的 Check。通過時回傳 compact 的輸出。agent 提交結果時也走這個函式（AGT）。
func (s *Spec) Validate(in ChunkInput, out []byte) (json.RawMessage, *ValidationError) {
	v, err := decodeJSON(out)
	if err != nil {
		return nil, &ValidationError{Reason: ReasonInvalidJSON, Message: "output was not valid JSON: " + err.Error()}
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, &ValidationError{Reason: ReasonInvalidJSON, Message: "output was not valid JSON: the top level must be an object"}
	}
	if err := checkSchema(s.validationSchema(), v, "$"); err != nil {
		return nil, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, &ValidationError{Reason: ReasonInvalidJSON, Message: "output was not valid JSON: " + err.Error()}
	}
	if s.Check != nil {
		if verr := s.Check(in, buf.Bytes()); verr != nil {
			if verr.Output == nil {
				verr.Output = bytes.Clone(buf.Bytes())
			}
			return nil, verr
		}
	}
	return buf.Bytes(), nil
}

// validationSchema 是驗證輸出用的 schema：Optional 的欄位從最上層的 required 拿掉。
func (s *Spec) validationSchema() map[string]any {
	if len(s.Optional) == 0 {
		return s.Schema
	}
	out := make(map[string]any, len(s.Schema))
	for k, v := range s.Schema {
		out[k] = v
	}
	var req []any
	for _, r := range stringList(s.Schema["required"]) {
		if !slices.Contains(s.Optional, r) {
			req = append(req, r)
		}
	}
	out["required"] = req
	return out
}

// decodeJSON 解出一個 JSON 值（數字保留為 json.Number）；之後還有其他內容時視為錯誤。
func decodeJSON(b []byte) (any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("empty output")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

// checkSchema 檢查 v 符合 schema。只支援步驟會用到的子集：type（object、array、string、integer、number、boolean）、
// properties、required、additionalProperties: false、items、enum。schema 本身不合法時也回傳錯誤（程式錯誤，由單元測試把關）。
func checkSchema(schema map[string]any, v any, path string) error {
	typ, nullable := schemaType(schema)
	if nullable && v == nil {
		return nil
	}
	switch typ {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		props, _ := schema["properties"].(map[string]any)
		// schema 以外的欄位一律拒絕（LLM-R9），不依賴 schema 明寫 additionalProperties: false。
		var extra []string
		for k := range obj {
			if _, known := props[k]; !known {
				extra = append(extra, k)
			}
		}
		if len(extra) > 0 {
			return fmt.Errorf("%s has fields outside the schema: %s", path, quoteKeys(extra))
		}
		for _, r := range stringList(schema["required"]) {
			if _, ok := obj[r]; !ok {
				return fmt.Errorf("%s.%s is required", path, r)
			}
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val, ok := obj[k]
			if !ok {
				continue
			}
			sub, _ := props[k].(map[string]any)
			if err := checkSchema(sub, val, path+"."+k); err != nil {
				return err
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		items, _ := schema["items"].(map[string]any)
		for i, e := range arr {
			if err := checkSchema(items, e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be an integer", path)
		}
		if _, err := n.Int64(); err != nil {
			return fmt.Errorf("%s must be an integer", path)
		}
	case "number":
		if _, ok := v.(json.Number); !ok {
			return fmt.Errorf("%s must be a number", path)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	default:
		return fmt.Errorf("schema at %s has unsupported type %q", path, typ)
	}
	if enum, ok := schema["enum"].([]any); ok {
		for _, e := range enum {
			if fmt.Sprint(e) == fmt.Sprint(v) {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %v", path, enum)
	}
	return nil
}

// 欄位名稱來自模型的輸出（不可信）：錯誤說明會附在重試的請求中、也會寫進 log，所以加上引號、限制數量與長度。
const (
	maxQuotedKeys   = 5
	maxQuotedKeyLen = 40
)

// quoteKeys 把模型輸出的欄位名稱寫成 "a", "b"：依字母順序最多 maxQuotedKeys 個，每個最多 maxQuotedKeyLen 個字元（%q 跳脫控制字元與引號）。
func quoteKeys(keys []string) string {
	sort.Strings(keys)
	n := min(len(keys), maxQuotedKeys)
	parts := make([]string, 0, n+1)
	for _, k := range keys[:n] {
		if r := []rune(k); len(r) > maxQuotedKeyLen {
			k = string(r[:maxQuotedKeyLen]) + "…"
		}
		parts = append(parts, fmt.Sprintf("%q", k))
	}
	if more := len(keys) - n; more > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", more))
	}
	return strings.Join(parts, ", ")
}

func stringList(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, e := range l {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ValidateStrictSchema 檢查 schema 符合 OpenAI strict 模式：每一層 object 都是 additionalProperties: false，
// 而且 required 列出所有屬性。真實 API 不符時回 400；fakellm 只檢查最上層，深層由這裡（單元測試）把關。
func ValidateStrictSchema(schema map[string]any) error {
	return strictAt(schema, "$")
}

func strictAt(schema map[string]any, path string) error {
	typ, nullable := schemaType(schema)
	if nullable && (typ == "object" || typ == "array") {
		return fmt.Errorf("%s: 只有純量可以是 null", path)
	}
	switch typ {
	case "object":
		if ap, ok := schema["additionalProperties"].(bool); !ok || ap {
			return fmt.Errorf("%s: additionalProperties 必須是 false", path)
		}
		props, _ := schema["properties"].(map[string]any)
		req := map[string]bool{}
		for _, r := range stringList(schema["required"]) {
			req[r] = true
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !req[k] {
				return fmt.Errorf("%s: required 缺少 %q", path, k)
			}
			sub, ok := props[k].(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: schema 必須是物件", path, k)
			}
			if err := strictAt(sub, path+"."+k); err != nil {
				return err
			}
		}
	case "array":
		items, ok := schema["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: array 必須有 items", path)
		}
		return strictAt(items, path+"[]")
	case "string", "integer", "number", "boolean":
	default:
		return fmt.Errorf("%s: 不支援的 type %v", path, schema["type"])
	}
	return nil
}

// schemaType 回傳 schema 的 type：字串，或 [<type>, "null"]（可以是 null 的純量，OpenAI strict 支援的寫法）。
// 其他形狀回傳空字串（不支援的 type）。
func schemaType(schema map[string]any) (typ string, nullable bool) {
	switch t := schema["type"].(type) {
	case string:
		return t, false
	case []any:
		if len(t) != 2 {
			return "", false
		}
		a, _ := t[0].(string)
		b, _ := t[1].(string)
		switch {
		case b == "null" && a != "null":
			return a, true
		case a == "null" && b != "null":
			return b, true
		}
	}
	return "", false
}
