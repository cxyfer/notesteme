package pipeline

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// mustEffective 以單次覆寫建立生效設定。
func mustEffective(t *testing.T, over map[string]any) Effective {
	t.Helper()
	eff, err := BuildEffective(nil, over)
	if err != nil {
		t.Fatal(err)
	}
	return eff
}

// stagesOver 組出 {"stages": {stage: cfg}} 覆寫。
func stagesOver(stage string, cfg map[string]any) map[string]any {
	return map[string]any{"stages": map[string]any{stage: cfg}}
}

func TestResolveExecutorTable(t *testing.T) {
	// 舊快照（M1）沒有 stages 與 outputs 區段。
	legacy, err := ParseEffective(`{"language":{"targets":["en"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	withOutputs := mustEffective(t, map[string]any{"outputs": []any{map[string]any{"type": "file"}}})

	tests := []struct {
		name  string
		eff   Effective
		stage string
		want  StageResolution
	}{
		{"metadata 是 local", mustEffective(t, nil), StageMetadata, StageResolution{Stage: StageMetadata, Executor: ExecutorLocal}},
		{"transcript 是 local", mustEffective(t, nil), StageTranscript, StageResolution{Stage: StageTranscript, Executor: ExecutorLocal}},
		{"normalize 是 local", mustEffective(t, nil), StageNormalize, StageResolution{Stage: StageNormalize, Executor: ExecutorLocal}},

		// 預設：api 沒有 provider 與 model。
		{"segment 預設", mustEffective(t, nil), StageSegment,
			StageResolution{Stage: StageSegment, Executor: ExecutorAPI, Prompt: "segment-default", Skip: true, Reason: SkipNotConfigured}},
		{"correct 預設", mustEffective(t, nil), StageCorrect,
			StageResolution{Stage: StageCorrect, Executor: ExecutorAPI, Prompt: "correct-default", Skip: true, Reason: SkipNotConfigured}},
		{"translate 預設", mustEffective(t, nil), StageTranslate,
			StageResolution{Stage: StageTranslate, Executor: ExecutorAPI, Prompt: "translate-default", Skip: true, Reason: SkipNotConfigured}},

		{"correct off", mustEffective(t, stagesOver("correct", map[string]any{"executor": "off"})), StageCorrect,
			StageResolution{Stage: StageCorrect, Executor: ExecutorOff, Prompt: "correct-default", Skip: true, Reason: SkipOff}},
		{"correct api 缺 model", mustEffective(t, stagesOver("correct", map[string]any{"provider": "anthropic"})), StageCorrect,
			StageResolution{Stage: StageCorrect, Executor: ExecutorAPI, Provider: "anthropic", Prompt: "correct-default", Skip: true, Reason: SkipNotConfigured}},
		{"correct api 缺 provider", mustEffective(t, stagesOver("correct", map[string]any{"model": "m1"})), StageCorrect,
			StageResolution{Stage: StageCorrect, Executor: ExecutorAPI, Model: "m1", Prompt: "correct-default", Skip: true, Reason: SkipNotConfigured}},
		{"correct api 完整", mustEffective(t, stagesOver("correct", map[string]any{"provider": "anthropic", "model": "m1"})), StageCorrect,
			StageResolution{Stage: StageCorrect, Executor: ExecutorAPI, Provider: "anthropic", Model: "m1", Prompt: "correct-default"}},
		{"segment agent 不略過", mustEffective(t, stagesOver("segment", map[string]any{"executor": "agent"})), StageSegment,
			StageResolution{Stage: StageSegment, Executor: ExecutorAgent, Prompt: "segment-default"}},
		{"translate opencc 不略過", mustEffective(t, stagesOver("translate", map[string]any{"executor": "opencc"})), StageTranslate,
			StageResolution{Stage: StageTranslate, Executor: ExecutorOpenCC, Prompt: "translate-default"}},
		{"自訂 prompt", mustEffective(t, stagesOver("translate", map[string]any{"provider": "openai", "model": "m2", "prompt": "my-prompt"})), StageTranslate,
			StageResolution{Stage: StageTranslate, Executor: ExecutorAPI, Provider: "openai", Model: "m2", Prompt: "my-prompt"}},

		{"publish 沒有 outputs", mustEffective(t, nil), StagePublish,
			StageResolution{Stage: StagePublish, Executor: ExecutorLocal, Skip: true, Reason: SkipNoOutputs}},
		{"publish 有 outputs", withOutputs, StagePublish, StageResolution{Stage: StagePublish, Executor: ExecutorLocal}},

		// 舊快照沒有 stages 區段：用內建預設值。
		{"舊快照 correct", legacy, StageCorrect,
			StageResolution{Stage: StageCorrect, Executor: ExecutorAPI, Prompt: "correct-default", Skip: true, Reason: SkipNotConfigured}},
		{"舊快照 publish", legacy, StagePublish, StageResolution{Stage: StagePublish, Executor: ExecutorLocal, Skip: true, Reason: SkipNoOutputs}},
		{"舊快照 normalize", legacy, StageNormalize, StageResolution{Stage: StageNormalize, Executor: ExecutorLocal}},
	}
	for _, tt := range tests {
		got, err := ResolveExecutor(tt.eff, tt.stage, StageContext{})
		if err != nil {
			t.Errorf("%s：%v", tt.name, err)
			continue
		}
		got.Wanted = nil // Wanted 另有測試（TestResolveExecutorWanted）
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s：\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
	}
}

// Wanted 是這份設定要求產生譯文的目標（正規化後、不含與原文相同的）：translate 被略過時，
// 不在其中的現行譯文要撤下（Codex P2）。
func TestResolveExecutorWanted(t *testing.T) {
	for _, tt := range []struct {
		name    string
		src     string
		targets []string
		want    []string
		skip    string
	}{
		{"同語言", "zh-Hans", []string{"zh-Hans", "zh_cn"}, nil, SkipSameLanguage},
		{"混合沒有 provider", "zh-Hans", []string{"zh_hant_tw", "en"}, []string{"zh-Hant-TW", "en"}, SkipNotConfigured},
		{"只剩非中文", "zh-Hans", []string{"en"}, []string{"en"}, SkipNotConfigured},
		{"只轉換", "zh-Hans", []string{"zh-Hant-TW", "zh-Hans"}, []string{"zh-Hant-TW"}, ""},
		{"extlang 不同", "zh-cmn-Hans", []string{"zh-yue-Hant-HK"}, []string{"zh-yue-Hant-HK"}, SkipNotConfigured},
	} {
		got, err := ResolveExecutor(mustEffective(t, langOver(tt.targets, "")), StageTranslate, StageContext{SourceLang: tt.src})
		if err != nil {
			t.Fatalf("%s：%v", tt.name, err)
		}
		if strings.Join(got.Wanted, ",") != strings.Join(tt.want, ",") || got.Reason != tt.skip {
			t.Errorf("%s：Wanted = %v、Reason = %q，預期 %v、%q", tt.name, got.Wanted, got.Reason, tt.want, tt.skip)
		}
	}
	// off 也要帶出 Wanted：略過時照樣撤下已移除的目標。
	off := withMap(langOver([]string{"zh-Hant-TW"}, ""), stagesOver("translate", map[string]any{"executor": "off"}))
	if got, _ := ResolveExecutor(mustEffective(t, off), StageTranslate, StageContext{SourceLang: "zh-Hans"}); !got.Skip || got.Reason != SkipOff || len(got.Wanted) != 1 {
		t.Errorf("off：%+v", got)
	}
}

func TestResolveExecutorRejects(t *testing.T) {
	var inv *InvalidError
	// opencc 只用於 translate；快照被改過時（沒有經過 BuildEffective）也要擋下。
	bad, err := ParseEffective(`{"stages":{"correct":{"executor":"opencc"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecutor(bad, StageCorrect, StageContext{}); !errors.As(err, &inv) {
		t.Errorf("correct 的 opencc：%v", err)
	}
	if _, err := ResolveExecutor(mustEffective(t, nil), "summary", StageContext{}); !errors.As(err, &inv) {
		t.Errorf("summary：%v", err)
	}
}

func TestPlanChain(t *testing.T) {
	stages := func(c []StageResolution) []string {
		var out []string
		for _, r := range c {
			out = append(out, r.Stage)
		}
		return out
	}
	eff := mustEffective(t, nil)
	// 預設：normalize 之後全部略過，鏈一路走到 publish。
	c, err := planChain(eff, StageSegment, "", StageContext{})
	if err != nil || !slices.Equal(stages(c), []string{StageSegment, StageCorrect, StageTranslate, StagePublish}) {
		t.Fatalf("segment → 最後：%v，%v", stages(c), err)
	}
	for _, r := range c {
		if !r.Skip {
			t.Errorf("%s 應被略過", r.Stage)
		}
	}
	// until 在中間：到 until（含）為止。
	if c, _ = planChain(eff, StageSegment, StageCorrect, StageContext{}); !slices.Equal(stages(c), []string{StageSegment, StageCorrect}) {
		t.Errorf("until=correct：%v", stages(c))
	}
	// 遇到第一個不略過的步驟就停下（含）。
	run := mustEffective(t, stagesOver("correct", map[string]any{"provider": "anthropic", "model": "m1"}))
	if c, _ = planChain(run, StageSegment, "", StageContext{}); !slices.Equal(stages(c), []string{StageSegment, StageCorrect}) || c[1].Skip {
		t.Errorf("correct 可執行時：%+v", c)
	}
	// from 就是第一個要執行的步驟。
	if c, _ = planChain(eff, StageNormalize, "", StageContext{}); !slices.Equal(stages(c), []string{StageNormalize}) || c[0].Skip {
		t.Errorf("normalize：%+v", c)
	}
}

func TestStageExecutorsAndLLMStages(t *testing.T) {
	for _, s := range []string{StageSegment, StageCorrect, StageTranslate} {
		if !IsLLMStage(s) {
			t.Errorf("%s 是 LLM 步驟", s)
		}
	}
	for _, s := range []string{StageMetadata, StageTranscript, StageNormalize, StagePublish, "summary"} {
		if IsLLMStage(s) || StageExecutors(s) != nil {
			t.Errorf("%s 不是 LLM 步驟，也不能設定 executor", s)
		}
	}
	if slices.Contains(StageExecutors(StageCorrect), ExecutorOpenCC) || !slices.Contains(StageExecutors(StageTranslate), ExecutorOpenCC) {
		t.Error("opencc 只用於 translate")
	}
}

func TestStageImplementedOnlyForM1LocalStages(t *testing.T) {
	// translate 的 opencc 是本機的確定性處理（TRN-R8）。
	if !stageImplemented(StageTranslate, ExecutorOpenCC) {
		t.Error("translate／opencc 已實作")
	}
	for _, s := range []string{StageMetadata, StageTranscript, StageNormalize} {
		if !stageImplemented(s, ExecutorLocal) {
			t.Errorf("%s／local 已實作", s)
		}
	}
	// agent 只建立任務並等 Agent 領取（AGT-R1，D15）：LLM 步驟的 agent 視為可以前進。
	for _, s := range []string{StageSegment, StageCorrect, StageTranslate} {
		if !stageImplemented(s, ExecutorAgent) {
			t.Errorf("%s／agent 已可前進", s)
		}
	}
	for _, c := range [][2]string{{StageSegment, ExecutorAPI}, {StageCorrect, ExecutorOpenCC}, {StageTranslate, ExecutorAPI}, {StagePublish, ExecutorLocal}, {StageNormalize, ExecutorAPI}, {StagePublish, ExecutorAgent}, {StageNormalize, ExecutorAgent}} {
		if stageImplemented(c[0], c[1]) {
			t.Errorf("%s／%s 還沒實作", c[0], c[1])
		}
	}
}

// 預設值要寫進快照，快照才完整記錄當時用了什麼設定（PIPE-R13）。
func TestBuildEffectiveDefaultsAreSnapshotted(t *testing.T) {
	eff := mustEffective(t, nil)
	_, cfgJSON, err := eff.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Stages  map[string]StageConfig `json:"stages"`
		Outputs []any                  `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &snap); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{StageSegment, StageCorrect, StageTranslate} {
		if c := snap.Stages[s]; c.Executor != ExecutorAPI || c.Prompt != s+"-default" {
			t.Errorf("stages.%s = %+v", s, c)
		}
	}
	if snap.Outputs == nil || len(snap.Outputs) != 0 {
		t.Errorf("outputs 預設是空陣列：%v", snap.Outputs)
	}
	if outs, err := eff.Outputs(); err != nil || len(outs) != 0 {
		t.Errorf("Outputs = %v, %v", outs, err)
	}
}

func TestBuildEffectiveRejectsInvalidStages(t *testing.T) {
	var inv *InvalidError
	tests := []struct {
		name string
		over map[string]any
	}{
		{"確定性步驟不能設定", stagesOver("metadata", map[string]any{"executor": "api"})},
		{"publish 不能設定", stagesOver("publish", map[string]any{"executor": "off"})},
		{"規劃中的步驟不能設定", stagesOver("summary", map[string]any{"executor": "api"})},
		{"correct 不能用 opencc", stagesOver("correct", map[string]any{"executor": "opencc"})},
		{"segment 不能用 opencc", stagesOver("segment", map[string]any{"executor": "opencc"})},
		{"batch 屬於 M4", stagesOver("correct", map[string]any{"executor": "batch"})},
		{"不認識的 executor", stagesOver("translate", map[string]any{"executor": "magic"})},
		{"provider 不是字串", stagesOver("correct", map[string]any{"provider": 1})},
		{"model 不是字串", stagesOver("correct", map[string]any{"model": []any{"x"}})},
		{"prompt 不是字串", stagesOver("correct", map[string]any{"prompt": true})},
		{"stage 設定不是物件", map[string]any{"stages": map[string]any{"correct": "off"}}},
		{"stages 不是物件", map[string]any{"stages": "x"}},
		{"outputs 不是陣列", map[string]any{"outputs": "x"}},
		{"outputs 的元素不是物件", map[string]any{"outputs": []any{"x"}}},
	}
	for _, tt := range tests {
		if _, err := BuildEffective(nil, tt.over); !errors.As(err, &inv) {
			t.Errorf("%s：err = %v，預期 *InvalidError", tt.name, err)
		}
	}
	// 步驟專屬的欄位 S0 不拒絕（PRF-R4 的完整驗證屬於 S1）。
	if _, err := BuildEffective(nil, stagesOver("correct", map[string]any{"min_similarity": 0.8})); err != nil {
		t.Errorf("步驟專屬欄位不應被拒絕：%v", err)
	}
	// translate 可以用 opencc。
	if _, err := BuildEffective(nil, stagesOver("translate", map[string]any{"executor": "opencc"})); err != nil {
		t.Errorf("translate／opencc：%v", err)
	}
	// Go 呼叫端以具體型別傳入 stages（不是 map[string]any）也接受，和 outputs 一致。
	typed := map[string]any{"stages": map[string]map[string]string{"correct": {"executor": "off"}}}
	eff, err := BuildEffective(nil, typed)
	if err != nil {
		t.Fatalf("具體型別的 stages：%v", err)
	}
	if cfg, err := eff.Stage(StageCorrect); err != nil || cfg.Executor != ExecutorOff {
		t.Errorf("Stage(correct) = %+v, %v，預期 executor off", cfg, err)
	}
}

// langOver 組出 {"language": {...}} 覆寫。
func langOver(targets []string, strategy string) map[string]any {
	l := map[string]any{"targets": targets}
	if strategy != "" {
		l["variant_strategy"] = strategy
	}
	return map[string]any{"language": l}
}

// withMap 合併兩份覆寫（頂層鍵不重複）。
func withMap(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

var apiConfigured = stagesOver("translate", map[string]any{"executor": "api", "provider": "anthropic", "model": "m1"})

// TRN-R8：原文與目標都是中文且要轉換字形時，translate 用 opencc，不論設定的是 api（有或沒有 provider）或 agent。
func TestResolveTranslateOpenCC(t *testing.T) {
	zhHans := StageContext{SourceLang: "zh-Hans"}
	for name, over := range map[string]map[string]any{
		"預設（api 沒有 provider）": langOver([]string{"zh-Hant-TW"}, ""),
		"api 有 provider":      withMap(langOver([]string{"zh-Hant-TW"}, "convert"), apiConfigured),
		"agent":               withMap(langOver([]string{"zh-Hant-TW"}, ""), stagesOver("translate", map[string]any{"executor": "agent"})),
		"多個中文目標":              langOver([]string{"zh-Hant-TW", "zh-Hant-HK"}, ""),
		"中文目標加上與原文相同的":        langOver([]string{"zh-Hans", "zh-Hant-TW"}, ""),
	} {
		got, err := ResolveExecutor(mustEffective(t, over), StageTranslate, zhHans)
		if err != nil || got.Skip || got.Executor != ExecutorOpenCC || got.Provider != "" || got.Model != "" || got.Prompt != "" {
			t.Errorf("%s：%+v, %v，預期 opencc 且不帶 provider", name, got, err)
		}
	}
}

// off 優先於自動轉換（S6-Q5 與 X1 的關係）。
func TestResolveTranslateOffWins(t *testing.T) {
	over := withMap(langOver([]string{"zh-Hant-TW"}, ""), stagesOver("translate", map[string]any{"executor": "off"}))
	got, err := ResolveExecutor(mustEffective(t, over), StageTranslate, StageContext{SourceLang: "zh-Hans"})
	if err != nil || !got.Skip || got.Reason != SkipOff || got.Executor != ExecutorOff {
		t.Errorf("off = %+v, %v", got, err)
	}
}

// TRN-R10：每個目標都與原文相同（或只差字形而沒有轉換表）時略過，原因 same_language；same_language 優先於 not_configured。
func TestResolveTranslateSameLanguage(t *testing.T) {
	for name, c := range map[string]struct {
		source  string
		targets []string
	}{
		"完全相同":    {"zh-Hant-TW", []string{"zh-Hant-TW"}},
		"標記大小寫不同": {"zh-hant-tw", []string{"zh-Hant-TW"}},
		"英文":      {"en", []string{"en"}},
		"多個目標都相同": {"en", []string{"en", "EN"}},
		"繁體台灣 → 繁體（沒有地區）": {"zh-Hant-TW", []string{"zh-Hant"}},
		"簡體 → 簡體（有地區）":    {"zh-Hans", []string{"zh-Hans-CN"}},
		"簡體 → zh":         {"zh-Hans", []string{"zh"}},
	} {
		got, err := ResolveExecutor(mustEffective(t, langOver(c.targets, "")), StageTranslate, StageContext{SourceLang: c.source})
		if err != nil || !got.Skip || got.Reason != SkipSameLanguage {
			t.Errorf("%s：%+v, %v，預期 skipped（same_language）", name, got, err)
		}
	}
}

// 目標混合、或原文語言不明時照原本的規則：api 沒有 provider 就是 not_configured（S6-Q5）；有 provider 時交給 api（S6b）。
func TestResolveTranslateMixedNotConfigured(t *testing.T) {
	hans := StageContext{SourceLang: "zh-Hans"}
	cases := []struct {
		name    string
		over    map[string]any
		sc      StageContext
		skip    bool
		wantExe string
	}{
		{"混合目標沒有 provider", langOver([]string{"zh-Hant-TW", "en"}, ""), hans, true, ExecutorAPI},
		{"混合目標有 provider", withMap(langOver([]string{"zh-Hant-TW", "en"}, ""), apiConfigured), hans, false, ExecutorAPI},
		{"原文是英文", langOver([]string{"zh-Hant-TW"}, ""), StageContext{SourceLang: "en"}, true, ExecutorAPI},
		{"原文語言不明", langOver([]string{"zh-Hant-TW"}, ""), StageContext{}, true, ExecutorAPI},
		{"原文是 zh（判斷不出字形）", langOver([]string{"zh-Hant-TW"}, ""), StageContext{SourceLang: "zh"}, true, ExecutorAPI},
		{"localize 不自動轉換（api 沒有 provider）", langOver([]string{"zh-Hant-TW"}, "localize"), hans, true, ExecutorAPI},
		{"localize 有 provider 交給 api", withMap(langOver([]string{"zh-Hant-TW"}, "localize"), apiConfigured), hans, false, ExecutorAPI},
		{"agent 的混合目標", withMap(langOver([]string{"zh-Hant-TW", "ja"}, ""), stagesOver("translate", map[string]any{"executor": "agent"})), hans, false, ExecutorAgent},
	}
	for _, c := range cases {
		got, err := ResolveExecutor(mustEffective(t, c.over), StageTranslate, c.sc)
		if err != nil || got.Skip != c.skip || got.Executor != c.wantExe || (c.skip && got.Reason != SkipNotConfigured) {
			t.Errorf("%s：%+v, %v", c.name, got, err)
		}
	}
	// 明確設定 opencc：目標含非中文時仍是 opencc（執行時以 permanent 失敗，見 TestRunTranslateOpenCCNonChineseTarget）。
	exp := withMap(langOver([]string{"en"}, ""), stagesOver("translate", map[string]any{"executor": "opencc"}))
	if got, err := ResolveExecutor(mustEffective(t, exp), StageTranslate, hans); err != nil || got.Skip || got.Executor != ExecutorOpenCC {
		t.Errorf("明確 opencc：%+v, %v", got, err)
	}
}

func TestPlanChainTranslateUsesStageContext(t *testing.T) {
	eff := mustEffective(t, nil) // 預設目標 zh-Hant-TW、convert
	c, err := planChain(eff, StageSegment, "", StageContext{SourceLang: "zh-Hans"})
	if err != nil || len(c) != 3 || c[2].Stage != StageTranslate || c[2].Skip || c[2].Executor != ExecutorOpenCC {
		t.Fatalf("簡體原文：%+v, %v", c, err)
	}
	c, err = planChain(eff, StageSegment, "", StageContext{SourceLang: "zh-Hant-TW"})
	if err != nil || len(c) != 4 || c[2].Reason != SkipSameLanguage || c[3].Stage != StagePublish {
		t.Fatalf("繁體原文：%+v, %v", c, err)
	}
}
