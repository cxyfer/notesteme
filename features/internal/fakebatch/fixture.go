package fakebatch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// toolPrefix 是強制的 tool 或 schema 名稱的前綴（同 fakellm.ToolPrefix）：notesteme_<stage>。
const toolPrefix = "notesteme_"

// FixtureAnswerer 直接讀 features/fixtures/llm/<名稱>.json 的 {"output": …} 回答請求，給單元測試用；
// harness 改接 fakellm（共用 steps.md 4.6 的 answers）。步驟取自 tool（Anthropic）或 json_schema（OpenAI）的名稱。
type FixtureAnswerer struct {
	// Dir 是 features/fixtures 的路徑（fixture 讀自其下的 llm/）。
	Dir string
	// InputTokens、OutputTokens 是回應的 usage（COST 場景用固定的數字）。
	InputTokens, OutputTokens int

	mu     sync.Mutex
	stages map[string]string // stage → fixture 名稱
}

// Set 讓這個步驟的請求都回答 fixture（取代先前的設定）。
func (a *FixtureAnswerer) Set(stage, fixture string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stages == nil {
		a.stages = map[string]string{}
	}
	a.stages[stage] = fixture
}

// Answer 實作 Answerer：找不到步驟或 fixture 時回 500。
func (a *FixtureAnswerer) Answer(provider string, body []byte) (int, []byte) {
	var req struct {
		Model string `json:"model"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		ToolChoice *struct {
			Name string `json:"name"`
		} `json:"tool_choice"`
		ResponseFormat *struct {
			JSONSchema *struct {
				Name string `json:"name"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return fail(provider, "body 不是合法的 JSON")
	}
	name := ""
	switch {
	case provider == Anthropic && req.ToolChoice != nil:
		name = req.ToolChoice.Name
	case provider == OpenAI && req.ResponseFormat != nil && req.ResponseFormat.JSONSchema != nil:
		name = req.ResponseFormat.JSONSchema.Name
	}
	stage, ok := strings.CutPrefix(name, toolPrefix)
	if !ok {
		return fail(provider, fmt.Sprintf("找不到強制的 tool 或 schema 名稱（%q）", name))
	}
	a.mu.Lock()
	fixture := a.stages[stage]
	a.mu.Unlock()
	if fixture == "" {
		return fail(provider, fmt.Sprintf("步驟 %q 沒有登記 fixture", stage))
	}
	output, err := a.load(fixture)
	if err != nil {
		return fail(provider, err.Error())
	}
	usage := map[string]any{"input_tokens": a.InputTokens, "output_tokens": a.OutputTokens}
	var v any
	if provider == Anthropic {
		v = map[string]any{
			"id": "msg_fake_batch", "type": "message", "role": "assistant", "model": req.Model,
			"content":     []any{map[string]any{"type": "tool_use", "id": "toolu_fake_batch", "name": name, "input": output}},
			"stop_reason": "tool_use", "stop_sequence": nil, "usage": usage,
		}
	} else {
		v = map[string]any{
			"id": "chatcmpl-fake-batch", "object": "chat.completion", "model": req.Model,
			"choices": []any{map[string]any{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": string(output)},
			}},
			"usage": map[string]any{"prompt_tokens": a.InputTokens, "completion_tokens": a.OutputTokens,
				"total_tokens": a.InputTokens + a.OutputTokens},
		}
	}
	b, _ := json.Marshal(v)
	return http.StatusOK, b
}

// load 讀出 fixture 的 output（compact JSON）；只接受 {"output": …}。
func (a *FixtureAnswerer) load(name string) (json.RawMessage, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return nil, fmt.Errorf("LLM fixture 名稱 %q 不正確（不可含路徑）", name)
	}
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	b, err := os.ReadFile(filepath.Join(a.Dir, "llm", name))
	if err != nil {
		return nil, fmt.Errorf("LLM fixture %q 不存在或無法讀取：%w", name, err)
	}
	var f struct {
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Output) == 0 {
		return nil, fmt.Errorf("LLM fixture %q 沒有 output（fakebatch 只支援 {\"output\": …}）", name)
	}
	return f.Output, nil
}

func fail(provider, msg string) (int, []byte) {
	b, _ := json.Marshal(errorBody(provider, "api_error", msg))
	return http.StatusInternalServerError, b
}
