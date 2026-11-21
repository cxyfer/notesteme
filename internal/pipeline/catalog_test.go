package pipeline

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/validate"
)

func TestBuiltinProviders(t *testing.T) {
	var c ProviderCatalog = BuiltinProviders{}
	for _, id := range []string{"anthropic", "openai"} {
		if !c.HasLLMProvider(id) {
			t.Errorf("%s 應該是內建 provider", id)
		}
	}
	for _, id := range []string{"", "nope", "Anthropic"} {
		if c.HasLLMProvider(id) {
			t.Errorf("%q 不應該是內建 provider", id)
		}
	}
}

func TestInvalidFields(t *testing.T) {
	e := invalidFields([]validate.FieldError{{Path: "stages.correct.provider", Message: "必須指定。"}, {Path: "b", Message: "c"}})
	if e.Msg != "stages.correct.provider：必須指定。" || len(e.Fields) != 2 {
		t.Errorf("invalidFields = %+v", e)
	}
	// 既有的 invalidf 沒有 Fields（M1 的 422 不變）。
	if e := invalidf("x %d", 1); e.Msg != "x 1" || e.Fields != nil {
		t.Errorf("invalidf = %+v", e)
	}
}

func TestInUseErrorMessages(t *testing.T) {
	for _, code := range []string{CodeProfileInUse, CodePromptInUse, "other"} {
		if (&InUseError{Code: code}).Error() == "" {
			t.Errorf("%s 沒有訊息", code)
		}
	}
}

// ValidateLayer 在契約階段不檢查任何東西（WP-1 實作）。
func TestValidateLayerStubReturnsNil(t *testing.T) {
	if got := ValidateLayer(t.Context(), map[string]any{"colour": "red"}, KindProfile, ValidateDeps{}); got != nil {
		t.Errorf("stub 應回傳 nil：%v", got)
	}
}

// prompt_version、prompt_sha256 是快照才有的欄位：Stage() 讀得出來；沒有時是零值。
func TestStageConfigReadsPromptVersion(t *testing.T) {
	eff := Effective{"stages": map[string]any{"correct": map[string]any{"prompt": "my@2", "prompt_version": float64(2), "prompt_sha256": "abc"}}}
	cfg, err := eff.Stage(StageCorrect)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PromptVersion != 2 || cfg.PromptSHA256 != "abc" || cfg.Prompt != "my@2" {
		t.Errorf("Stage = %+v", cfg)
	}
	if cfg, _ := (Effective{}).Stage(StageCorrect); cfg.PromptVersion != 0 || cfg.PromptSHA256 != "" {
		t.Errorf("預設值不應有 prompt 版本：%+v", cfg)
	}
}
