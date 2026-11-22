package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
)

func TestKeyEnvVar(t *testing.T) {
	for id, want := range map[string]string{
		"anthropic": "NOTESTEME_ANTHROPIC_API_KEY", "openai": "NOTESTEME_OPENAI_API_KEY", "local-vllm": "NOTESTEME_LOCAL_VLLM_API_KEY",
	} {
		if got := KeyEnvVar(id); got != want {
			t.Errorf("KeyEnvVar(%q) = %q，預期 %q", id, got, want)
		}
	}
}

func TestRegistryProviders(t *testing.T) {
	r := NewRegistry(map[string]ProviderConfig{
		"anthropic": {Type: TypeAnthropic, Limits: Limits{RPM: 1}},
		"vllm":      {Type: TypeOpenAI, BaseURL: "http://10.0.0.5:8000/v1"},
		"other":     {Type: TypeOpenAI},
	}, nil)
	if !r.HasLLMProvider("openai") || !r.HasLLMProvider("vllm") || r.HasLLMProvider("nope") {
		t.Errorf("providers = %v", r.IDs())
	}
	a, _ := r.Provider("anthropic")
	if a.BaseURL != DefaultAnthropicBaseURL || a.Limits.RPM != 1 || a.ID != "anthropic" {
		t.Errorf("設定檔覆寫內建的 provider 時沿用預設的 base_url：%+v", a)
	}
	if o, _ := r.Provider("other"); o.BaseURL != DefaultOpenAIBaseURL {
		t.Errorf("沒有 base_url 的 openai 類型用預設值：%+v", o)
	}
}

// API key：環境變數優先，其次是資料庫；都沒有時 needs_action，動作「設定 API key」（LLM-R12）。
func TestRegistryKeys(t *testing.T) {
	r := NewRegistry(nil, nil)
	stored := map[string]string{"openai": "sk-db"}
	r.StoredKey = func(_ context.Context, id string) (string, bool, error) {
		k, ok := stored[id]
		return k, ok, nil
	}
	var gotKey string
	r.NewClient = func(_ ProviderConfig, key string) Client { gotKey = key; return &scripted{} }
	r.Env = map[string]string{"NOTESTEME_OPENAI_API_KEY": " sk-env \n"}
	if _, err := r.Client(context.Background(), "openai"); err != nil || gotKey != "sk-env" {
		t.Errorf("環境變數優先（去掉空白）：%q %v", gotKey, err)
	}
	r.Env = nil
	if _, err := r.Client(context.Background(), "openai"); err != nil || gotKey != "sk-db" {
		t.Errorf("沒有環境變數時用資料庫的 key：%q %v", gotKey, err)
	}
	_, err := r.Client(context.Background(), "anthropic")
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction || len(ce.Actions) != 1 || ce.Actions[0] != ActionSetAPIKey || ce.Message == "" {
		t.Errorf("沒有 key：%v", err)
	}
	if _, err := r.Client(context.Background(), "nope"); !errors.As(err, &ce) || ce.Class != errclass.ClassPermanent {
		t.Errorf("不存在的 provider：%v", err)
	}
}
