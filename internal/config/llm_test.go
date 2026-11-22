package config

import (
	"strings"
	"testing"
)

// LLM-R7 場景的設定檔，加上自訂 base_url 的 OpenAI 相容 provider。
func TestLoadLLMProviders(t *testing.T) {
	cfg, err := loadASR(t, `llm:
  providers:
    anthropic: { type: anthropic, rpm: 1 }
    local-vllm: { type: openai, base_url: "http://10.0.0.5:8000/v1", rpm: 60, tpm: 100000 }
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.LLMProviders["anthropic"]; got != (LLMProvider{ID: "anthropic", Type: LLMTypeAnthropic, RPM: 1}) {
		t.Errorf("anthropic = %+v", got)
	}
	if got := cfg.LLMProviders["local-vllm"]; got != (LLMProvider{ID: "local-vllm", Type: LLMTypeOpenAI, BaseURL: "http://10.0.0.5:8000/v1", RPM: 60, TPM: 100000}) {
		t.Errorf("local-vllm = %+v", got)
	}
}

func TestLoadLLMProvidersRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"llm:\n  chunk: { max_tokens: 10 }\n":                                        "llm.chunk",
		"llm:\n  providers:\n    x: { type: gemini }\n":                              "llm.providers.x.type",
		"llm:\n  providers:\n    x: { base_url: \"https://a\" }\n":                   "llm.providers.x.type",
		"llm:\n  providers:\n    x: { type: openai, base_url: \"ftp://a\" }\n":       "llm.providers.x.base_url",
		"llm:\n  providers:\n    x: { type: openai, base_url: \"https://u:p@a\" }\n": "llm.providers.x.base_url",
		"llm:\n  providers:\n    x: { type: openai, rpm: -1 }\n":                     "llm.providers.x.rpm",
		"llm:\n  providers:\n    x: { type: openai, tpm: 1.5 }\n":                    "llm.providers.x.tpm",
		"llm:\n  providers:\n    x: { type: openai, api_key: sk }\n":                 "llm.providers.x",
		"llm:\n  providers:\n    Bad_ID: { type: openai }\n":                         "不合法的 id",
	}
	for doc, want := range cases {
		_, err := loadASR(t, doc)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q：錯誤 = %v，預期含 %q", doc, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "sk") && strings.Contains(doc, "api_key") && strings.Contains(err.Error(), "api_key: sk") {
			t.Errorf("錯誤訊息不該回顯 key 的值：%v", err)
		}
	}
}
