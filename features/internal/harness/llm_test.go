package harness

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/features/internal/fakellm"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// llmPost 經 fakenet 與 netguard，以正式主機名稱呼叫 LLM provider 的端點。
func llmPost(t *testing.T, w *World, url string, header map[string]string, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	c := &http.Client{Transport: netguard.Transport(w.Net.Deps()), Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const (
	anthropicBody = `{"model":"m","messages":[{"role":"user","content":"hi"}],` +
		`"tools":[{"name":"notesteme_correct"}],"tool_choice":{"type":"tool","name":"notesteme_correct"}}`
	openaiBody = `{"model":"m","messages":[{"role":"user","content":"hi"}],` +
		`"response_format":{"type":"json_schema","json_schema":{"name":"notesteme_correct","strict":true,"schema":{"type":"object"}}}}`
)

// 預設有 anthropic 與 openai 的 key：環境變數帶入測試金鑰，fake 的主機要求各自的憑證，違規會讓 Check 失敗。
func TestNewHasLLMProviders(t *testing.T) {
	w := NewT(t)
	if w.Env[AnthropicAPIKeyEnv] != fakellm.TestAnthropicKey || AnthropicAPIKeyEnv != "NOTESTEME_ANTHROPIC_API_KEY" ||
		w.Env[OpenAIAPIKeyEnv] != fakellm.TestOpenAIKey || OpenAIAPIKeyEnv != "NOTESTEME_OPENAI_API_KEY" {
		t.Fatalf("Env = %v", w.Env)
	}
	if err := w.LLM.Answer("anthropic", "correct", "not-json"); err != nil {
		t.Fatal(err)
	}
	if err := w.LLM.Answer("openai", "correct", "not-json"); err != nil {
		t.Fatal(err)
	}
	anth := map[string]string{"anthropic-version": fakellm.AnthropicVersion, "x-api-key": fakellm.TestAnthropicKey}
	oa := map[string]string{"Authorization": "Bearer " + fakellm.TestOpenAIKey}

	if code, body := llmPost(t, w, "https://api.anthropic.com/v1/messages", anth, anthropicBody); code != 200 || !strings.Contains(body, "this is not json") {
		t.Errorf("anthropic：%d %s", code, body)
	}
	if code, body := llmPost(t, w, "https://api.openai.com/v1/chat/completions", oa, openaiBody); code != 200 || !strings.Contains(body, "this is not json") {
		t.Errorf("openai：%d %s", code, body)
	}
	if code, _ := llmPost(t, w, "https://api.anthropic.com/v1/messages", map[string]string{"anthropic-version": fakellm.AnthropicVersion}, anthropicBody); code != 401 {
		t.Errorf("anthropic 沒有 key 應為 401：%d", code)
	}
	if code, _ := llmPost(t, w, "https://api.openai.com/v1/chat/completions", map[string]string{"Authorization": "Bearer wrong"}, openaiBody); code != 401 {
		t.Errorf("openai 錯誤的 key 應為 401：%d", code)
	}
	if err := w.Check(); err != nil {
		t.Errorf("正常與 401 的請求不是違規：%v", err)
	}

	// 沒有腳本的請求是違規，Check 回報。
	if code, _ := llmPost(t, w, "https://api.anthropic.com/v1/messages", anth,
		strings.ReplaceAll(anthropicBody, "notesteme_correct", "notesteme_segment")); code != 500 {
		t.Errorf("沒有腳本應為 500：%d", code)
	}
	if err := w.Check(); err == nil || !strings.Contains(err.Error(), "fakellm") {
		t.Errorf("Check() = %v，預期含 fakellm 違規", err)
	}
}

// RemoveAPIKey 取消 fakellm 的 key 要求：沒帶 key 的請求拿到正常的回答。
func TestRemoveAPIKeyLiftsLLMRequirement(t *testing.T) {
	w := NewT(t)
	_ = w.LLM.Answer("openai", "correct", "not-json")
	if err := w.RemoveAPIKey("openai"); err != nil {
		t.Fatal(err)
	}
	if code, _ := llmPost(t, w, "https://api.openai.com/v1/chat/completions", nil, openaiBody); code != 200 {
		t.Errorf("取消 key 要求後：%d，預期 200", code)
	}
}

// harness 一律開啟 SerialLLM。
func TestAppEnvEnablesSerialLLM(t *testing.T) {
	if !NewT(t).AppEnv().Test.SerialLLM {
		t.Error("AppEnv().Test.SerialLLM 應該是 true")
	}
}
