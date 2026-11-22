package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// Check 在輸出符合 schema 之後，對照 chunk 的輸入做步驟自己的驗證（例如 correct 的句數）；nil 代表沒有。
	Check func(in ChunkInput, out json.RawMessage) *ValidationError
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
	if err := checkSchema(s.Schema, v, "$"); err != nil {
		return nil, &ValidationError{Reason: ReasonSchema, Message: "output does not match the schema: " + err.Error()}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, &ValidationError{Reason: ReasonInvalidJSON, Message: "output was not valid JSON: " + err.Error()}
	}
	if s.Check != nil {
		if verr := s.Check(in, buf.Bytes()); verr != nil {
			return nil, verr
		}
	}
	return buf.Bytes(), nil
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
	typ, _ := schema["type"].(string)
	switch typ {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		props, _ := schema["properties"].(map[string]any)
		if ap, ok := schema["additionalProperties"].(bool); ok && !ap {
			var extra []string
			for k := range obj {
				if _, known := props[k]; !known {
					extra = append(extra, k)
				}
			}
			if len(extra) > 0 {
				sort.Strings(extra)
				return fmt.Errorf("%s has fields outside the schema: %s", path, strings.Join(extra, ", "))
			}
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
	switch schema["type"] {
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
