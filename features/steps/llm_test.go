package steps

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/cxyfer/notesteme/features/internal/fakellm"
	"github.com/cxyfer/notesteme/features/internal/harness"
)

// llmUserText 組出 user message：說明加上一行 JSON 的資料區塊。
func llmUserText(chunk int, note string) string {
	blk, _ := json.Marshal(map[string]any{
		"chunk": chunk, "chunks": 2, "context": []any{},
		"cues": []any{map[string]any{"i": chunk, "text": "hello " + note}},
	})
	return "請校正：" + note + "\n" + fakellm.DataOpen + "\n" + string(blk) + "\n" + fakellm.DataClose
}

// llmSend 以 anthropic 的 wire format 對場景的 fakellm 送出一個請求（不經過網路，直接呼叫 handler）。
// stage 是強制的 tool 名稱 notesteme_<stage>；user 是 user message 的文字。回傳 HTTP status。
func llmSend(t *testing.T, s *Scenario, stage, user string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":       "fixture-model",
		"system":      "固定的系統指示 cache_control",
		"messages":    []any{map[string]any{"role": "user", "content": user}},
		"tools":       []any{map[string]any{"name": fakellm.ToolPrefix + stage}},
		"tool_choice": map[string]any{"type": "tool", "name": fakellm.ToolPrefix + stage},
	})
	r := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(string(body)))
	r.Header.Set("anthropic-version", fakellm.AnthropicVersion)
	r.Header.Set("x-api-key", fakellm.TestAnthropicKey)
	w := httptest.NewRecorder()
	s.World.LLM.Handler("anthropic").ServeHTTP(w, r)
	return w.Code
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustFail(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s：應該失敗", what)
	}
}

// 請求不存在時，`contains` 與 `does not contain` 都要失敗（後者不能恆真）。
func TestLLMRequestStepsFailWhenRequestIsMissing(t *testing.T) {
	s := newTestScenario(t)
	mustFail(t, "沒有任何請求：contains", s.llmRequestContains(1, "anthropic", "x"))
	mustFail(t, "沒有任何請求：does not contain", s.llmRequestDoesNotContain(1, "anthropic", "x"))
	mustFail(t, "沒有任何請求：share prefix", s.llmSharePrefix(1, 2, "anthropic"))

	mustNil(t, s.World.LLM.Answer("anthropic", "correct", "correct-echo"))
	if code := llmSend(t, s, "correct", llmUserText(1, "one")); code != 200 {
		t.Fatalf("status = %d", code)
	}
	// 第 1 個存在；第 2 個、第 0 個不存在。
	mustNil(t, s.llmRequestContains(1, "anthropic", "one"))
	mustNil(t, s.llmRequestDoesNotContain(1, "anthropic", "不存在的文字"))
	for _, n := range []int{0, 2, -1} {
		mustFail(t, fmt.Sprintf("請求 %d 不存在：contains", n), s.llmRequestContains(n, "anthropic", "one"))
		mustFail(t, fmt.Sprintf("請求 %d 不存在：does not contain", n), s.llmRequestDoesNotContain(n, "anthropic", "不存在的文字"))
	}
	// 另一個 provider 沒有請求。
	mustFail(t, "openai 沒有請求：does not contain", s.llmRequestDoesNotContain(1, "openai", "x"))
	// 不認識的 provider。
	mustFail(t, "不認識的 provider", s.llmRequestDoesNotContain(1, "gemini", "x"))
}

func TestLLMRequestContains(t *testing.T) {
	s := newTestScenario(t)
	mustNil(t, s.World.LLM.Answer("anthropic", "correct", "correct-echo"))
	llmSend(t, s, "correct", llmUserText(1, "alpha"))

	mustNil(t, s.llmRequestContains(1, "anthropic", "alpha"))
	mustNil(t, s.llmRequestContains(1, "anthropic", "<transcript_data>"))
	mustNil(t, s.llmRequestContains(1, "anthropic", "cache_control")) // 系統指示的文字
	mustFail(t, "不含的文字：contains", s.llmRequestContains(1, "anthropic", "beta"))
	mustFail(t, "含有的文字：does not contain", s.llmRequestDoesNotContain(1, "anthropic", "alpha"))
	mustNil(t, s.llmRequestDoesNotContain(1, "anthropic", "beta"))
}

// 請求數：失敗的請求也算；「for」依步驟分開計數。
func TestLLMReceivedCounts(t *testing.T) {
	s := newTestScenario(t)
	mustNil(t, s.llmReceived("anthropic", 0))
	mustFail(t, "還沒有請求：預期 1 個", s.llmReceived("anthropic", 1))

	mustNil(t, s.World.LLM.Answer("anthropic", "correct", "correct-echo"))
	mustNil(t, s.World.LLM.Answer("anthropic", "segment", "correct-echo"))
	mustNil(t, s.llmFailsOnce("anthropic", 429))
	if code := llmSend(t, s, "correct", llmUserText(1, "a")); code != 429 {
		t.Fatalf("status = %d，預期 429", code)
	}
	llmSend(t, s, "correct", llmUserText(1, "a"))
	llmSend(t, s, "correct", llmUserText(2, "b"))
	llmSend(t, s, "segment", llmUserText(1, "c"))

	mustNil(t, s.llmReceived("anthropic", 4)) // 429 那一次也算
	mustFail(t, "總數 3", s.llmReceived("anthropic", 3))
	mustNil(t, s.llmReceivedFor("anthropic", 3, "correct"))
	mustNil(t, s.llmReceivedFor("anthropic", 1, "segment"))
	mustNil(t, s.llmReceivedFor("anthropic", 0, "translate"))
	// 不分步驟的實作會把 correct 算成 4 個；這幾個必須失敗。
	mustFail(t, "correct 4 個", s.llmReceivedFor("anthropic", 4, "correct"))
	mustFail(t, "segment 4 個", s.llmReceivedFor("anthropic", 4, "segment"))
	mustFail(t, "translate 1 個", s.llmReceivedFor("anthropic", 1, "translate"))
	mustNil(t, s.llmReceived("openai", 0))
	mustFail(t, "不認識的 provider", s.llmReceived("gemini", 0))
}

func TestLLMSharePrefix(t *testing.T) {
	s := newTestScenario(t)
	mustNil(t, s.World.LLM.Answer("anthropic", "correct", "correct-echo"))
	// 1、2：同一個固定前綴（user message 的說明相同），3：說明不同。4：沒有資料區塊。
	llmSend(t, s, "correct", llmUserText(1, "same"))
	llmSend(t, s, "correct", llmUserText(2, "same"))
	llmSend(t, s, "correct", llmUserText(2, "different"))
	llmSend(t, s, "correct", "只有說明，沒有資料區塊")

	mustNil(t, s.llmSharePrefix(1, 2, "anthropic"))
	err := s.llmSharePrefix(1, 3, "anthropic")
	mustFail(t, "前綴不同", err)
	if err != nil && !strings.Contains(err.Error(), "前綴不同") {
		t.Errorf("err = %v", err)
	}
	mustFail(t, "沒有資料區塊", s.llmSharePrefix(1, 4, "anthropic"))
	mustFail(t, "請求不存在", s.llmSharePrefix(1, 9, "anthropic"))
}

func TestLLMAnswerAndFailSteps(t *testing.T) {
	s := newTestScenario(t)
	mustNil(t, s.llmAnswers("anthropic", "correct", "correct-ok.json"))
	mustNil(t, s.llmAnswers("openai", "correct", "correct-ok-alt.json"))
	mustNil(t, s.llmAnswersThen("anthropic", "correct", "not-json", "correct-echo"))
	mustNil(t, s.llmAnswersChunk("anthropic", "correct", 2, "correct-wrong-count"))

	// fixture 不存在時，步驟失敗（不是 pending）。
	err := s.llmAnswers("anthropic", "correct", "no-such-fixture")
	if err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("fixture 不存在：err = %v，預期明確失敗", err)
	}
	mustFail(t, "不認識的 provider：answers", s.llmAnswers("gemini", "correct", "correct-echo"))
	mustFail(t, "不認識的 provider：chunk", s.llmAnswersChunk("gemini", "correct", 1, "correct-echo"))
	mustFail(t, "不認識的 provider：fails", s.llmFails("gemini", 500))
	mustFail(t, "chunk 0", s.llmAnswersChunk("anthropic", "correct", 0, "correct-echo"))
	mustFail(t, "HTTP 200 不是錯誤", s.llmFails("anthropic", 200))

	mustNil(t, s.llmFails("anthropic", 503))
	for range 2 {
		if code := llmSend(t, s, "correct", llmUserText(1, "x")); code != 503 {
			t.Errorf("一直失敗：status = %d", code)
		}
	}
	mustNil(t, s.llmFailsOnce("anthropic", 404))
	if code := llmSend(t, s, "correct", llmUserText(1, "x")); code != 404 {
		t.Errorf("once：第一個請求 status = %d", code)
	}
	if code := llmSend(t, s, "correct", llmUserText(1, "x")); code != 200 {
		t.Errorf("once：第二個請求 status = %d，預期 Fail 已用完", code)
	}
}

// 所有 LLM 步驟的句型都已登記；只有 chunk 切分維持 pending。
func TestLLMStepRegistration(t *testing.T) {
	var found int
	for _, d := range Definitions() {
		if !strings.Contains(d.Template, "LLM provider") && !strings.HasPrefix(d.Template, "request") && !strings.Contains(d.Template, "chunks of") {
			continue
		}
		found++
		if f, ok := d.handler.(func(string, string, *godog.Table) error); ok && strings.Contains(d.Template, "chunks of") {
			if err := f("correct", "yt:x", nil); !errors.Is(err, godog.ErrPending) {
				t.Errorf("chunks of … are: 應該維持 pending，得到 %v", err)
			}
		}
	}
	if found != 11 {
		t.Errorf("LLM 步驟數 = %d，預期 11", found)
	}
}

// `the provider "anthropic" has no API key`：移除環境變數與 fakellm 的 key 要求。
func TestProviderHasNoAPIKeyForLLM(t *testing.T) {
	for _, tc := range []struct{ provider, env, key string }{
		{"anthropic", harness.AnthropicAPIKeyEnv, fakellm.TestAnthropicKey},
		{"openai", harness.OpenAIAPIKeyEnv, fakellm.TestOpenAIKey},
	} {
		s := newTestScenario(t)
		if s.World.Env[tc.env] != tc.key {
			t.Fatalf("%s：預設環境應該有測試用的 key", tc.provider)
		}
		err := s.providerHasNoAPIKey(tc.provider)
		if err != nil {
			t.Fatalf("%s：%v", tc.provider, err)
		}
		if _, ok := s.World.Env[tc.env]; ok {
			t.Errorf("%s：環境變數 %s 應該被移除", tc.provider, tc.env)
		}
	}

	// 取消 key 要求：沒有帶 key 的請求拿到正常的回答（產品沒檢查 key 時，場景因此失敗）。
	s := newTestScenario(t)
	mustNil(t, s.World.LLM.Answer("anthropic", "correct", "correct-echo"))
	mustNil(t, s.providerHasNoAPIKey("anthropic"))
	body, _ := json.Marshal(map[string]any{
		"model": "m", "messages": []any{map[string]any{"role": "user", "content": llmUserText(1, "x")}},
		"tools":       []any{map[string]any{"name": "notesteme_correct"}},
		"tool_choice": map[string]any{"type": "tool", "name": "notesteme_correct"},
	})
	r := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", strings.NewReader(string(body)))
	r.Header.Set("anthropic-version", fakellm.AnthropicVersion)
	w := httptest.NewRecorder()
	s.World.LLM.Handler("anthropic").ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("取消 key 要求後，沒帶 key 的請求 status = %d，預期 200", w.Code)
	}

	// openai 沒有被取消。
	s2 := newTestScenario(t)
	mustNil(t, s2.providerHasNoAPIKey("anthropic"))
	if _, ok := s2.World.Env[harness.OpenAIAPIKeyEnv]; !ok {
		t.Error("openai 的 key 不該被移除")
	}

	// 不認識的 provider 明確失敗，不是 pending。
	s3 := newTestScenario(t)
	err := s3.providerHasNoAPIKey("gemini")
	if err == nil || errors.Is(err, godog.ErrPending) {
		t.Errorf("不認識的 provider：err = %v，預期明確失敗", err)
	}
}

// 違規（沒有腳本、不符 wire 契約）要讓場景失敗：沿用 harness 對未攔截連線的處理方式，
// 即使後面的步驟是 pending 也記進收集器。
func TestLLMViolationFailsScenario(t *testing.T) {
	steps := func(sc *godog.ScenarioContext, s *Scenario) {
		registerLLM(&Registry{sc: sc}, s)
		sc.Step(`^the program calls the LLM without a script$`, func() error {
			llmSend(t, s, "correct", llmUserText(1, "x"))
			return nil
		})
		sc.Step(`^something not implemented yet$`, func() error { return godog.ErrPending })
	}
	code, added := runHookSuite(t, `Feature: llm
  Scenario: no script then pending
    Given the program calls the LLM without a script
    Then something not implemented yet

  Scenario: scripted is clean
    Given the LLM provider "anthropic" answers "correct" with "correct-echo"
    And the program calls the LLM without a script
    Then the LLM provider "anthropic" received 1 request
`, steps)
	// 第二個場景有腳本，所以乾淨；第一個場景違規。
	if len(added) != 1 || !strings.Contains(added[0], "no script then pending") || !strings.Contains(added[0], "fakellm") {
		t.Fatalf("收集器應記錄第一個場景的 fakellm 違規，實際：%q", added)
	}
	_ = code

	// 直接檢查 World.Check：違規讓它回傳錯誤。
	s := newTestScenario(t)
	mustNil(t, s.World.Check())
	llmSend(t, s, "mystery_tool_that_has_no_script", llmUserText(1, "x"))
	err := s.World.Check()
	if err == nil || !strings.Contains(err.Error(), "fakellm") || !strings.Contains(err.Error(), "沒有腳本") {
		t.Errorf("World.Check() = %v", err)
	}
}
