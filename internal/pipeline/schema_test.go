package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/internal/validate"
)

// fakeCatalog 只認得 anthropic、openai（和 BuiltinProviders 相同，測試不依賴它）。
type fakeCatalog struct{}

func (fakeCatalog) HasLLMProvider(id string) bool { return id == "anthropic" || id == "openai" }

// fakeResolver 模擬 prompt 庫：內建三個 default（版本 1）、使用者的 my-correct（版本 1、2，類型 correct）。
type fakeResolver struct{}

func (fakeResolver) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, ver, hasVer := strings.Cut(ref, "@")
	kinds := map[string]string{
		"segment-default": "segment", "correct-default": "correct", "translate-default": "translate",
		"localize-zh-hant-tw": "translate", "my-correct": "correct",
	}
	kind, ok := kinds[id]
	if !ok {
		return ResolvedPrompt{}, ErrPromptNotFound
	}
	version := 1
	switch {
	case !hasVer || ver == "default":
	case ver == "1":
	case ver == "2" && id == "my-correct":
		version = 2
	case ver == "x":
		return ResolvedPrompt{}, &InvalidError{Msg: "prompt 引用的版本必須是 default 或正整數。"}
	default:
		return ResolvedPrompt{}, ErrPromptVersionNotFound
	}
	return ResolvedPrompt{ID: id, Kind: kind, Version: version, SHA256: "sha-" + id}, nil
}

var testDeps = ValidateDeps{Providers: fakeCatalog{}, Prompts: fakeResolver{}}

func jsonDoc(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("測試資料不是 JSON：%v\n%s", err, s)
	}
	return m
}

func yamlDoc(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("測試資料不是 YAML：%v\n%s", err, s)
	}
	return m
}

func errPaths(errs []validate.FieldError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Path)
	}
	return out
}

// PRF-R4 的 6 個範例（features/PRF-profiles.feature 的 Examples）：errors[0].path 必須對得上。
func TestValidateLayer_PRF_R4_Examples(t *testing.T) {
	cases := []struct{ config, path string }{
		{`{ "stages": { "correct": { "executor": "api" } } }`, "stages.correct.provider"},
		{`{ "stages": { "correct": { "executor": "opencc" } } }`, "stages.correct.executor"},
		{`{ "language": { "targets": "en" } }`, "language.targets"},
		{`{ "stages": { "correct": { "executor": "api", "provider": "nope", "model": "m" } } }`, "stages.correct.provider"},
		{`{ "stages": { "correct": { "executor": "api", "provider": "anthropic", "model": "m", "prompt": "missing" } } }`, "stages.correct.prompt"},
		{`{ "colour": "blue" }`, "colour"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			errs := ValidateLayer(t.Context(), jsonDoc(t, c.config), KindProfile, testDeps)
			if len(errs) == 0 || errs[0].Path != c.path {
				t.Fatalf("errors = %+v，errors[0].path 應為 %s", errs, c.path)
			}
			if errs[0].Message == "" {
				t.Error("錯誤沒有說明")
			}
		})
	}
	// 範例 1 的完整結果：provider 排在 model 之前，兩個都回報。
	errs := ValidateLayer(t.Context(), jsonDoc(t, cases[0].config), KindProfile, testDeps)
	if want := []string{"stages.correct.provider", "stages.correct.model"}; !reflect.DeepEqual(errPaths(errs), want) {
		t.Errorf("範例 1 的路徑 = %v，預期 %v", errPaths(errs), want)
	}
	// 範例 2：executor 錯誤時略過這個 stage 的完整性檢查，只有一個錯誤。
	errs = ValidateLayer(t.Context(), jsonDoc(t, cases[1].config), KindProfile, testDeps)
	if len(errs) != 1 || errs[0].Message != "correct 不支援 opencc；可用 api、agent、off。" {
		t.Errorf("範例 2 = %+v", errs)
	}
}

// PRF-R1 的完整處理方案（features/PRF-profiles.feature「A complete profile is accepted」）。
func TestValidateLayer_CompleteProfileAccepted(t *testing.T) {
	doc := jsonDoc(t, `{
          "stages": {
            "segment":   { "executor": "api", "provider": "openai", "model": "fixture-model", "prompt": "segment-default" },
            "correct":   { "executor": "agent", "prompt": "correct-default" },
            "translate": { "executor": "opencc" } },
          "language": { "targets": ["zh-Hant-TW"], "variant_strategy": "convert" },
          "timing": { "split_timing": "when_needed", "split_timing_audio": "auto" },
          "llm": { "chunk": { "max_tokens": 4000, "overlap_cues": 3, "context_cues": 5 } },
          "agent": { "timeout": "24h", "on_timeout": "fallback_api" },
          "subtitle_rules": { "cjk": { "max_chars_per_line": 22 } },
          "outputs": [ { "target": "sidecar", "formats": ["srt"], "lang": ["zh-Hant-TW"] } ] }`)
	if errs := ValidateLayer(t.Context(), doc, KindProfile, testDeps); errs != nil {
		t.Errorf("完整的處理方案應該通過：%+v", errs)
	}
	// 沒有依賴（不查 provider、prompt）也通過。
	if errs := ValidateLayer(t.Context(), doc, KindProfile, ValidateDeps{}); errs != nil {
		t.Errorf("沒有依賴時應該通過：%+v", errs)
	}
}

// features/*.feature 中所有處理方案、Background、頻道覆寫、新增與重跑覆寫的設定，逐字放進來，都不能有錯誤。
// 新增的 until 是請求層級的欄位，不屬於覆寫，所以不在這些文件中。
func TestValidateLayer_M2FeatureProfilesAccepted(t *testing.T) {
	type doc struct {
		name string
		kind LayerKind
		yaml string
	}
	cases := []doc{
		// AGT
		{"AGT agent-correct", KindProfile, `
stages:
  correct: { executor: agent, prompt: correct-default, provider: anthropic, model: fixture-model }
agent:
  max_claims: 4
  lease: 10m
  timeout: 24h
  on_timeout: fallback_api
llm:
  chunk: { max_tokens: 4000 }
`},
		{"AGT agent-notify", KindProfile, `
stages:
  correct: { executor: agent, prompt: correct-default }
agent: { timeout: 24h, on_timeout: notify }
`},
		// COR
		{"COR cor-test", KindProfile, `
stages:
  correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default, min_similarity: 0.6 }
`},
		{"COR channel glossary 1", KindOverride, `glossary: [KRaft, Confluent]`},
		{"COR channel glossary 2", KindOverride, `glossary: [微服務, 資料庫]`},
		{"COR channel glossary 3", KindOverride, `glossary: [KRaft]`},
		{"COR ingest overrides", KindOverride, `language: { fallback: [groq] }`},
		{"COR no-correct", KindProfile, `
stages:
  correct: { executor: off }
  translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
language: { targets: [zh-Hant-TW] }
`},
		// DEL、OUT
		{"DEL/OUT media-server", KindProfile, `
outputs:
  - { target: sidecar, formats: [srt], lang: [en] }
`},
		{"OUT careful", KindProfile, `
outputs:
  - { target: sidecar, formats: [srt], lang: [en], on_user_change: skip }
`},
		// ING
		{"ING standard", KindProfile, `
language:
  targets: [zh-Hant-TW]
`},
		{"ING ingest overrides", KindOverride, `
language:
  targets: [en]
`},
		// LLM
		{"LLM llm-test", KindProfile, `
stages:
  correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default }
llm:
  chunk: { max_tokens: 4000, overlap_cues: 3, context_cues: 5 }
  max_validation_retries: 2
`},
		// PIPE
		{"PIPE fallback supadata", KindOverride, `
language:
  fallback: [supadata]
`},
		{"PIPE fallback groq", KindOverride, `language: { fallback: [groq] }`},
		{"PIPE targets and fallback", KindOverride, `language: { targets: [en], fallback: [supadata] }`},
		// PRF
		{"PRF global", KindGlobal, `
language: { targets: [zh-Hant-TW], fallback: [supadata, groq] }
timing: { split_timing: when_needed }
`},
		{"PRF standard", KindProfile, `timing: { split_timing: forbid }`},
		{"PRF channel", KindOverride, `language: { targets: [ja] }`},
		{"PRF ingest overrides", KindOverride, `language: { fallback: [groq] }`},
		{"PRF global lists", KindGlobal, `language: { targets: [zh-Hant-TW, en] }`},
		{"PRF standard correct", KindProfile, `
stages:
  correct: { executor: api, provider: anthropic, model: fixture-model, prompt: correct-default }
`},
		{"PRF mixed", KindProfile, `
stages:
  segment:   { executor: off }
  correct:   { executor: agent, prompt: correct-default }
  translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
`},
		// PRM
		{"PRM prompt @1", KindProfile, `
stages:
  correct: { executor: api, provider: anthropic, model: fixture-model, prompt: my-correct@1 }
`},
		{"PRM prompt @default", KindProfile, `
stages:
  correct: { executor: api, provider: anthropic, model: fixture-model, prompt: my-correct@default }
`},
		// SEC：設定檔中的處理方案
		{"SEC config profile", KindProfile, `{ language: { targets: [en] } }`},
		// SEG
		{"SEG seg-test", KindProfile, `
stages:
  segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
timing:
  split_timing: when_needed
  split_timing_audio: auto
`},
		{"SEG strict", KindProfile, `
stages:
  segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
timing:
  split_timing: forbid
`},
		{"SEG vps", KindProfile, `
stages:
  segment: { executor: api, provider: openai, model: fixture-model, prompt: segment-default }
timing:
  split_timing: when_needed
  split_timing_audio: never
`},
		// SRC
		{"SRC global", KindGlobal, `
language:
  targets: [zh-Hant-TW]
  caption_priority:
    - { lang: original, kind: manual }
    - { lang: original, kind: auto }
    - { lang: zh-Hans, kind: manual }
  allow_platform_translated: false
  fallback: [supadata, groq]
`},
		// TRN
		{"TRN trn-test", KindProfile, `
stages:
  translate: { executor: api, provider: anthropic, model: fixture-model, prompt: translate-default }
language:
  targets: [zh-Hant-TW]
  variant_strategy: convert
`},
		// 重跑覆寫（JSON，逐字取自 LIB、TRN、NRM）
		{"LIB rerun override", KindOverride, `{ "stages": { "correct": { "provider": "openai", "model": "fixture-model" } } }`},
		{"TRN rerun targets", KindOverride, `{ "language": { "targets": ["zh-Hant-TW", "ja"] } }`},
		{"TRN rerun convert zh-Hant-TW", KindOverride, `{ "language": { "targets": ["zh-Hant-TW"], "variant_strategy": "convert" } }`},
		{"TRN rerun convert zh-Hant-HK", KindOverride, `{ "language": { "targets": ["zh-Hant-HK"], "variant_strategy": "convert" } }`},
		{"TRN rerun convert zh-Hans", KindOverride, `{ "language": { "targets": ["zh-Hans"], "variant_strategy": "convert" } }`},
		{"TRN rerun localize", KindOverride, `{ "language": { "targets": ["zh-Hant-TW"], "variant_strategy": "localize" } }`},
		{"NRM rerun override", KindOverride, `{ "language": { "targets": ["en"] } }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// YAML 是 JSON 的超集，所以 JSON 與 YAML 都用 yaml 解析，和 harness 一樣。
			d := yamlDoc(t, c.yaml)
			if errs := ValidateLayer(t.Context(), d, c.kind, testDeps); errs != nil {
				t.Errorf("應該通過，卻有錯誤：%+v", errs)
			}
		})
	}
}

// 覆寫與全域是部分的層：不檢查完整性；處理方案只檢查文件中有寫出來的 stage（D8）。
func TestValidateLayer_OverrideSkipsCompleteness(t *testing.T) {
	partial := []string{
		`{ "stages": { "correct": { "executor": "api" } } }`,
		`{ "stages": { "correct": { "provider": "openai", "model": "fixture-model" } } }`,
		`{ "stages": { "translate": {} } }`,
	}
	for _, kind := range []LayerKind{KindOverride, KindGlobal} {
		for _, s := range partial {
			if errs := ValidateLayer(t.Context(), jsonDoc(t, s), kind, testDeps); errs != nil {
				t.Errorf("kind=%d %s 不該檢查完整性：%+v", kind, s, errs)
			}
		}
	}
	// 同樣的文件在處理方案中不完整。
	if errs := ValidateLayer(t.Context(), jsonDoc(t, partial[0]), KindProfile, testDeps); len(errs) != 2 {
		t.Errorf("處理方案應該缺 provider、model：%+v", errs)
	}
	if errs := ValidateLayer(t.Context(), jsonDoc(t, partial[1]), KindProfile, testDeps); errs != nil {
		t.Errorf("executor 沒寫就是 api，provider 與 model 都有就完整：%+v", errs)
	}
	if errs := ValidateLayer(t.Context(), jsonDoc(t, partial[2]), KindProfile, testDeps); len(errs) != 2 {
		t.Errorf("寫出來的空 stage 要檢查完整性：%+v", errs)
	}
	// 沒寫出來的 stage 不檢查；空文件、只有其他區段都合法。
	for _, s := range []string{`{}`, `{ "language": { "targets": ["en"] } }`, `{ "stages": {} }`} {
		if errs := ValidateLayer(t.Context(), jsonDoc(t, s), KindProfile, testDeps); errs != nil {
			t.Errorf("%s 應該通過：%+v", s, errs)
		}
	}
	// executor 是 agent、off、opencc 時不要求 provider 與 model。
	for _, s := range []string{
		`{ "stages": { "correct": { "executor": "agent" } } }`,
		`{ "stages": { "segment": { "executor": "off" } } }`,
		`{ "stages": { "translate": { "executor": "opencc" } } }`,
	} {
		if errs := ValidateLayer(t.Context(), jsonDoc(t, s), KindProfile, testDeps); errs != nil {
			t.Errorf("%s 應該通過：%+v", s, errs)
		}
	}
	// 只缺 model 時只回報 model。
	errs := ValidateLayer(t.Context(), jsonDoc(t, `{ "stages": { "correct": { "provider": "anthropic" } } }`), KindProfile, testDeps)
	if want := []string{"stages.correct.model"}; !reflect.DeepEqual(errPaths(errs), want) {
		t.Errorf("路徑 = %v，預期 %v", errPaths(errs), want)
	}
}

// 錯誤的順序固定：依 schema 的欄位順序，未知欄位接在同一層的已知欄位之後並依字母排序。
func TestValidateLayer_ErrorOrderDeterministic(t *testing.T) {
	raw := `{
		"zzz": 1, "aaa": 2,
		"outputs": [ { "target": "nowhere", "zeta": 1, "alpha": 2 } ],
		"language": { "targets": "en", "variant_strategy": "x" },
		"stages": {
			"translate": { "model": 5 },
			"correct": { "executor": "api", "bogus": 1, "another": 2 },
			"nope": {}, "alpha": {} },
		"supadata": { "mode": "x" },
		"agent": { "lease": "soon", "max_claims": 0 }
	}`
	want := []string{
		"stages.correct.provider", "stages.correct.model", "stages.correct.another", "stages.correct.bogus",
		"stages.translate.provider", "stages.translate.model",
		"stages.alpha", "stages.nope",
		"language.targets", "language.variant_strategy",
		"agent.max_claims", "agent.lease",
		"outputs.0.target", "outputs.0.alpha", "outputs.0.zeta",
		"supadata.mode",
		"aaa", "zzz",
	}
	first := ValidateLayer(t.Context(), jsonDoc(t, raw), KindProfile, testDeps)
	if !reflect.DeepEqual(errPaths(first), want) {
		t.Fatalf("路徑順序 =\n%v\n預期\n%v", errPaths(first), want)
	}
	for i := 0; i < 100; i++ {
		// 每次重新解析：Go 的 map 每次的走訪順序都不同。
		if got := ValidateLayer(t.Context(), jsonDoc(t, raw), KindProfile, testDeps); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次的結果不同：\n%+v\n%+v", i, got, first)
		}
	}
}

// 任何層級的 null 都回 422（只有 PATCH 的 merge patch 用 null 表示刪除，那是驗證之前的事）。
func TestValidateLayer_NullRejected(t *testing.T) {
	cases := []struct{ doc, path string }{
		{`{ "stages": null }`, "stages"},
		{`{ "stages": { "correct": null } }`, "stages.correct"},
		{`{ "stages": { "correct": { "executor": null } } }`, "stages.correct.executor"},
		{`{ "stages": { "correct": { "provider": null } } }`, "stages.correct.provider"},
		{`{ "stages": { "correct": { "model": null } } }`, "stages.correct.model"},
		{`{ "stages": { "correct": { "prompt": null } } }`, "stages.correct.prompt"},
		{`{ "stages": { "correct": { "min_similarity": null } } }`, "stages.correct.min_similarity"},
		{`{ "stages": { "translate": { "length_ratio": { "min": null } } } }`, "stages.translate.length_ratio.min"},
		{`{ "language": null }`, "language"},
		{`{ "language": { "targets": null } }`, "language.targets"},
		{`{ "language": { "targets": ["en", null] } }`, "language.targets.1"},
		{`{ "language": { "caption_priority": [null] } }`, "language.caption_priority.0"},
		{`{ "language": { "allow_platform_translated": null } }`, "language.allow_platform_translated"},
		{`{ "timing": { "split_timing": null } }`, "timing.split_timing"},
		{`{ "llm": { "chunk": { "max_tokens": null } } }`, "llm.chunk.max_tokens"},
		{`{ "agent": { "lease": null } }`, "agent.lease"},
		{`{ "subtitle_rules": { "cjk": { "max_cps": null } } }`, "subtitle_rules.cjk.max_cps"},
		{`{ "outputs": null }`, "outputs"},
		{`{ "outputs": [ { "target": null } ] }`, "outputs.0.target"},
		{`{ "glossary": null }`, "glossary"},
		{`{ "supadata": { "mode": null } }`, "supadata.mode"},
	}
	for _, kind := range []LayerKind{KindGlobal, KindProfile, KindOverride} {
		for _, c := range cases {
			errs := ValidateLayer(t.Context(), jsonDoc(t, c.doc), kind, testDeps)
			found := false
			for _, e := range errs {
				if e.Path == c.path && e.Message == "不能是 null。" {
					found = true
				}
			}
			if !found {
				t.Errorf("kind=%d %s：預期 %s 回報 null，實際 %+v", kind, c.doc, c.path, errs)
			}
		}
	}
	// 未知欄位的值是 null：回報未知欄位。
	errs := ValidateLayer(t.Context(), jsonDoc(t, `{ "colour": null }`), KindOverride, testDeps)
	if len(errs) != 1 || errs[0].Path != "colour" {
		t.Errorf("errs = %+v", errs)
	}
}

// YAML 1.2：off、on 是字串，不是布林（harness 用 gopkg.in/yaml.v3，見 features/steps/config.go）。
func TestValidateLayer_YAMLOffIsString(t *testing.T) {
	doc := yamlDoc(t, "stages:\n  segment: { executor: off }\n  correct:\n    executor: off\n")
	for _, name := range []string{"segment", "correct"} {
		got := doc["stages"].(map[string]any)[name].(map[string]any)["executor"]
		if s, ok := got.(string); !ok || s != "off" {
			t.Fatalf("executor: off 解成 %T(%v)，應為字串 \"off\"", got, got)
		}
	}
	if errs := ValidateLayer(t.Context(), doc, KindProfile, testDeps); errs != nil {
		t.Errorf("executor: off 應該通過（完整性不要求 provider）：%+v", errs)
	}
	// on 也是字串：它不是合法的 executor，回報的是列舉錯誤而不是型別錯誤。
	on := yamlDoc(t, "stages:\n  correct: { executor: on }\n")
	if got := on["stages"].(map[string]any)["correct"].(map[string]any)["executor"]; got != "on" {
		t.Fatalf("executor: on 解成 %T(%v)", got, got)
	}
	errs := ValidateLayer(t.Context(), on, KindProfile, testDeps)
	if len(errs) != 1 || errs[0].Message != "correct 不支援 on；可用 api、agent、off。" {
		t.Errorf("errs = %+v", errs)
	}
}

// prompt 引用的檢查：不存在、版本不存在、類型與步驟不符、引用格式錯誤。
func TestValidateLayer_PromptKindMismatch(t *testing.T) {
	cases := []struct {
		name, stage, prompt, msg string
	}{
		{"類型不符", "correct", "translate-default", "prompt「translate-default」的類型是 translate，不能用於 correct。"},
		{"localize 是 translate", "segment", "localize-zh-hant-tw", "prompt「localize-zh-hant-tw」的類型是 translate，不能用於 segment。"},
		{"自訂版本的類型不符", "translate", "my-correct@2", "prompt「my-correct@2」的類型是 correct，不能用於 translate。"},
		{"不存在", "correct", "missing", "找不到 prompt「missing」。"},
		{"版本不存在", "correct", "my-correct@9", "prompt「my-correct」沒有第 9 版。"},
		{"引用格式錯", "correct", "my-correct@x", "prompt 引用的版本必須是 default 或正整數。"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := map[string]any{"stages": map[string]any{c.stage: map[string]any{"executor": "agent", "prompt": c.prompt}}}
			errs := ValidateLayer(t.Context(), doc, KindProfile, testDeps)
			if len(errs) != 1 || errs[0].Path != "stages."+c.stage+".prompt" || errs[0].Message != c.msg {
				t.Errorf("errs = %+v，預期 %s：%s", errs, "stages."+c.stage+".prompt", c.msg)
			}
		})
	}
	// 類型相符就通過；Prompts 為 nil 時完全不檢查。
	for _, ok := range []struct{ stage, prompt string }{{"correct", "correct-default"}, {"translate", "localize-zh-hant-tw"}, {"correct", "my-correct@2"}} {
		doc := map[string]any{"stages": map[string]any{ok.stage: map[string]any{"executor": "agent", "prompt": ok.prompt}}}
		if errs := ValidateLayer(t.Context(), doc, KindProfile, testDeps); errs != nil {
			t.Errorf("%s/%s 應該通過：%+v", ok.stage, ok.prompt, errs)
		}
	}
	doc := map[string]any{"stages": map[string]any{"correct": map[string]any{"executor": "agent", "prompt": "missing"}}}
	if errs := ValidateLayer(t.Context(), doc, KindProfile, ValidateDeps{}); errs != nil {
		t.Errorf("Prompts 為 nil 時不該檢查：%+v", errs)
	}
	// 解析器回傳其他錯誤時，回報成這個欄位的錯誤，不吞掉。
	broken := ValidateDeps{Prompts: errResolver{errors.New("db down")}}
	errs := ValidateLayer(t.Context(), doc, KindProfile, broken)
	if len(errs) != 1 || errs[0].Path != "stages.correct.prompt" || !strings.Contains(errs[0].Message, "db down") {
		t.Errorf("errs = %+v", errs)
	}
}

type errResolver struct{ err error }

func (r errResolver) ResolvePrompt(context.Context, string) (ResolvedPrompt, error) {
	return ResolvedPrompt{}, r.err
}

// batch 屬於 M4：每個 LLM 步驟都回報同一句話，而且不再回報完整性。
func TestValidateLayer_BatchIsM4(t *testing.T) {
	for _, stage := range []string{"segment", "correct", "translate"} {
		doc := map[string]any{"stages": map[string]any{stage: map[string]any{"executor": "batch"}}}
		for _, kind := range []LayerKind{KindProfile, KindOverride, KindGlobal} {
			errs := ValidateLayer(t.Context(), doc, kind, testDeps)
			if len(errs) != 1 || errs[0].Path != "stages."+stage+".executor" || errs[0].Message != "batch 屬於 M4，尚未支援。" {
				t.Errorf("%s kind=%d：errs = %+v", stage, kind, errs)
			}
		}
	}
}

// 未知欄位、型別、列舉與範圍：每個層級都嚴格。
func TestValidateLayer_StrictSchema(t *testing.T) {
	cases := []struct{ name, doc, path, msg string }{
		{"未知頂層欄位", `{ "colour": "blue" }`, "colour", "不認識的欄位。"},
		{"快照專屬欄位 prompt_version 是未知", `{ "stages": { "correct": { "prompt_version": 2 } } }`, "stages.correct.prompt_version", "不認識的欄位。"},
		{"batch 以外的 stage 名稱", `{ "stages": { "transcript": {} } }`, "stages.transcript", "這個步驟沒有可設定的欄位。"},
		{"stages 不是物件", `{ "stages": [] }`, "stages", "必須是物件。"},
		{"stage 不是物件", `{ "stages": { "correct": "api" } }`, "stages.correct", "必須是物件。"},
		{"executor 不是字串", `{ "stages": { "correct": { "executor": 1 } } }`, "stages.correct.executor", "必須是字串。"},
		{"provider 空字串", `{ "stages": { "correct": { "provider": "" } } }`, "stages.correct.provider", "不可以是空字串。"},
		{"provider 不存在", `{ "stages": { "correct": { "provider": "nope" } } }`, "stages.correct.provider", "找不到 LLM provider「nope」。"},
		{"model 不是字串", `{ "stages": { "correct": { "model": 5 } } }`, "stages.correct.model", "必須是字串。"},
		{"translate 不支援的欄位", `{ "stages": { "translate": { "min_similarity": 0.5 } } }`, "stages.translate.min_similarity", "不認識的欄位。"},
		{"min_similarity 超出範圍", `{ "stages": { "correct": { "min_similarity": 1.5 } } }`, "stages.correct.min_similarity", "必須是 0 到 1 之間的數字。"},
		{"min_similarity 不是數字", `{ "stages": { "correct": { "min_similarity": "high" } } }`, "stages.correct.min_similarity", "必須是 0 到 1 之間的數字。"},
		{"length_ratio 負數", `{ "stages": { "translate": { "length_ratio": { "min": -1 } } } }`, "stages.translate.length_ratio.min", "必須是大於 0 的數字。"},
		{"length_ratio 範圍顛倒", `{ "stages": { "translate": { "length_ratio": { "min": 4, "max": 1 } } } }`, "stages.translate.length_ratio.min", "不可以大於 max。"},
		{"length_ratio 未知欄位", `{ "stages": { "translate": { "length_ratio": { "avg": 1 } } } }`, "stages.translate.length_ratio.avg", "不認識的欄位。"},
		{"targets 是字串", `{ "language": { "targets": "en" } }`, "language.targets", "必須是字串陣列。"},
		{"targets 空陣列", `{ "language": { "targets": [] } }`, "language.targets", "至少要有 1 個項目。"},
		{"targets 有空字串", `{ "language": { "targets": ["en", ""] } }`, "language.targets.1", "不可以是空字串。"},
		{"targets 有數字", `{ "language": { "targets": [1] } }`, "language.targets.0", "必須是字串。"},
		{"caption_priority 缺 kind", `{ "language": { "caption_priority": [ { "lang": "en" } ] } }`, "language.caption_priority.0.kind", "必填。"},
		{"caption_priority kind 不合法", `{ "language": { "caption_priority": [ { "lang": "en", "kind": "x" } ] } }`, "language.caption_priority.0.kind", "必須是 manual、auto 其中之一。"},
		{"caption_priority 不是陣列", `{ "language": { "caption_priority": {} } }`, "language.caption_priority", "必須是物件陣列。"},
		{"allow_platform_translated 不是 bool", `{ "language": { "allow_platform_translated": "no" } }`, "language.allow_platform_translated", "必須是 true 或 false。"},
		{"fallback 不是陣列", `{ "language": { "fallback": "groq" } }`, "language.fallback", "必須是字串陣列。"},
		{"variant_strategy 不合法", `{ "language": { "variant_strategy": "both" } }`, "language.variant_strategy", "必須是 convert、localize 其中之一。"},
		{"language 未知欄位", `{ "language": { "target": ["en"] } }`, "language.target", "不認識的欄位。"},
		{"split_timing 不合法", `{ "timing": { "split_timing": "always" } }`, "timing.split_timing", "必須是 forbid、when_needed、free 其中之一。"},
		{"split_timing_audio 不合法", `{ "timing": { "split_timing_audio": "x" } }`, "timing.split_timing_audio", "必須是 auto、never 其中之一。"},
		{"max_tokens 是 0", `{ "llm": { "chunk": { "max_tokens": 0 } } }`, "llm.chunk.max_tokens", "必須是大於 0 的整數。"},
		{"max_tokens 是小數", `{ "llm": { "chunk": { "max_tokens": 1.5 } } }`, "llm.chunk.max_tokens", "必須是大於 0 的整數。"},
		{"overlap_cues 是負數", `{ "llm": { "chunk": { "overlap_cues": -1 } } }`, "llm.chunk.overlap_cues", "必須是 0 以上的整數。"},
		{"max_validation_retries 是字串", `{ "llm": { "max_validation_retries": "2" } }`, "llm.max_validation_retries", "必須是 0 以上的整數。"},
		{"llm 未知欄位", `{ "llm": { "providers": {} } }`, "llm.providers", "不認識的欄位。"},
		{"max_claims 是 0", `{ "agent": { "max_claims": 0 } }`, "agent.max_claims", "必須是大於 0 的整數。"},
		{"lease 不是 duration", `{ "agent": { "lease": "soon" } }`, "agent.lease", "必須是有效期間字串，例如 24h、10m、2d。"},
		{"timeout 是數字", `{ "agent": { "timeout": 24 } }`, "agent.timeout", "必須是有效期間字串，例如 24h、10m、2d。"},
		{"on_timeout 不合法", `{ "agent": { "on_timeout": "ignore" } }`, "agent.on_timeout", "必須是 fallback_api、notify 其中之一。"},
		{"on_validation_failure 不合法", `{ "agent": { "on_validation_failure": "notify" } }`, "agent.on_validation_failure", "必須是 requeue、fallback_api 其中之一。"},
		{"subtitle_rules 只有 cjk、latin", `{ "subtitle_rules": { "kana": {} } }`, "subtitle_rules.kana", "不認識的欄位。"},
		{"max_lines 是 0", `{ "subtitle_rules": { "latin": { "max_lines": 0 } } }`, "subtitle_rules.latin.max_lines", "必須是大於 0 的整數。"},
		{"max_cps 是 0", `{ "subtitle_rules": { "cjk": { "max_cps": 0 } } }`, "subtitle_rules.cjk.max_cps", "必須是大於 0 的數字。"},
		{"min_duration 不是 duration", `{ "subtitle_rules": { "cjk": { "min_duration": "fast" } } }`, "subtitle_rules.cjk.min_duration", "必須是有效期間字串，例如 24h、10m、2d。"},
		{"subtitle_rules 欄位拼錯", `{ "subtitle_rules": { "cjk": { "max_char": 22 } } }`, "subtitle_rules.cjk.max_char", "不認識的欄位。"},
		{"outputs 不是陣列", `{ "outputs": {} }`, "outputs", "必須是物件陣列。"},
		{"outputs 項目不是物件", `{ "outputs": ["srt"] }`, "outputs.0", "必須是物件。"},
		{"outputs 缺 target", `{ "outputs": [ { "formats": ["srt"] } ] }`, "outputs.0.target", "必填。"},
		{"outputs target 不合法", `{ "outputs": [ { "target": "cloud" } ] }`, "outputs.0.target", "必須是 sidecar、folder 其中之一。"},
		{"outputs formats 不合法", `{ "outputs": [ { "target": "sidecar", "formats": ["srt", "doc"] } ] }`, "outputs.0.formats.1", "必須是 srt、vtt、txt、cues-json、ass、bilingual-srt 其中之一。"},
		{"outputs formats 不是陣列", `{ "outputs": [ { "target": "sidecar", "formats": "srt" } ] }`, "outputs.0.formats", "必須是字串陣列。"},
		{"outputs lang 不是陣列", `{ "outputs": [ { "target": "sidecar", "lang": "en" } ] }`, "outputs.0.lang", "必須是字串陣列。"},
		{"outputs 未知欄位", `{ "outputs": [ { "target": "sidecar", "dir": "x" } ] }`, "outputs.0.dir", "不認識的欄位。"},
		{"glossary 不是陣列", `{ "glossary": "KRaft" }`, "glossary", "必須是字串陣列。"},
		{"glossary 有空字串", `{ "glossary": ["a", ""] }`, "glossary.1", "不可以是空字串。"},
		{"supadata mode 不合法", `{ "supadata": { "mode": "x" } }`, "supadata.mode", "必須是 native、generate、auto 其中之一。"},
		{"supadata 未知欄位", `{ "supadata": { "key": "x" } }`, "supadata.key", "不認識的欄位。"},
	}
	for _, kind := range []LayerKind{KindGlobal, KindOverride, KindProfile} {
		for _, c := range cases {
			errs := ValidateLayer(t.Context(), jsonDoc(t, c.doc), kind, testDeps)
			found := false
			for _, e := range errs {
				if e.Path == c.path && e.Message == c.msg {
					found = true
				}
			}
			if !found {
				t.Errorf("kind=%d %s：預期 %s「%s」，實際 %+v", kind, c.name, c.path, c.msg, errs)
			}
		}
	}
}

// 輸入先經 JSON 轉換：Go 的 int、[]string 等型別（呼叫端直接組出來的 map）一樣能驗證；
// nil 或空文件沒有錯誤；不能轉成 JSON 的文件回報一個沒有路徑的錯誤。
func TestValidateLayer_NormalizesInput(t *testing.T) {
	doc := map[string]any{
		"language": map[string]any{"targets": []string{"en", "ja"}},
		"llm":      map[string]any{"chunk": map[string]any{"max_tokens": 4000, "overlap_cues": int64(3)}},
		"outputs":  []map[string]any{{"target": "sidecar", "formats": []string{"srt"}}},
	}
	if errs := ValidateLayer(t.Context(), doc, KindProfile, testDeps); errs != nil {
		t.Errorf("Go 型別的文件應該通過：%+v", errs)
	}
	if errs := ValidateLayer(t.Context(), nil, KindProfile, testDeps); errs != nil {
		t.Errorf("nil 文件：%+v", errs)
	}
	if errs := ValidateLayer(t.Context(), map[string]any{}, KindProfile, testDeps); errs != nil {
		t.Errorf("空文件：%+v", errs)
	}
	bad := ValidateLayer(t.Context(), map[string]any{"language": make(chan int)}, KindOverride, testDeps)
	if len(bad) != 1 || bad[0].Path != "" || !strings.HasPrefix(bad[0].Message, "設定不是合法的 JSON 物件") {
		t.Errorf("不能轉成 JSON：%+v", bad)
	}
	// 不修改輸入。
	in := jsonDoc(t, `{ "colour": 1, "stages": { "correct": { "executor": "api" } } }`)
	want := jsonDoc(t, `{ "colour": 1, "stages": { "correct": { "executor": "api" } } }`)
	ValidateLayer(t.Context(), in, KindProfile, testDeps)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("輸入被修改：%v", in)
	}
}

// 沒有依賴時只做 schema 檢查：不存在的 provider 與 prompt 不會報錯。
func TestValidateLayer_NilDepsSkipExistence(t *testing.T) {
	doc := jsonDoc(t, `{ "stages": { "correct": { "executor": "api", "provider": "nope", "model": "m", "prompt": "missing" } } }`)
	if errs := ValidateLayer(t.Context(), doc, KindProfile, ValidateDeps{}); errs != nil {
		t.Errorf("errs = %+v", errs)
	}
	errs := ValidateLayer(t.Context(), doc, KindProfile, ValidateDeps{Providers: BuiltinProviders{}})
	if want := []string{"stages.correct.provider"}; !reflect.DeepEqual(errPaths(errs), want) {
		t.Errorf("路徑 = %v，預期 %v", errPaths(errs), want)
	}
}

// 錯誤可以直接組成 *InvalidError（D9）：Msg 是第一個錯誤的「路徑：說明」。
func TestValidateLayer_FeedsInvalidFields(t *testing.T) {
	errs := ValidateLayer(t.Context(), jsonDoc(t, `{ "stages": { "correct": { "executor": "api" } } }`), KindProfile, testDeps)
	e := invalidFields(errs)
	if want := fmt.Sprintf("%s：%s", "stages.correct.provider", "executor 為 api 時必須指定 provider。"); e.Msg != want {
		t.Errorf("Msg = %q，預期 %q", e.Msg, want)
	}
}
