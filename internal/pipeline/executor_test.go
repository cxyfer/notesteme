package pipeline

import (
	"encoding/json"
	"errors"
	"slices"
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
		got, err := ResolveExecutor(tt.eff, tt.stage)
		if err != nil {
			t.Errorf("%s：%v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s：\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
	}
}

func TestResolveExecutorRejects(t *testing.T) {
	var inv *InvalidError
	// opencc 只用於 translate；快照被改過時（沒有經過 BuildEffective）也要擋下。
	bad, err := ParseEffective(`{"stages":{"correct":{"executor":"opencc"}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecutor(bad, StageCorrect); !errors.As(err, &inv) {
		t.Errorf("correct 的 opencc：%v", err)
	}
	if _, err := ResolveExecutor(mustEffective(t, nil), "summary"); !errors.As(err, &inv) {
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
	c, err := planChain(eff, StageSegment, "")
	if err != nil || !slices.Equal(stages(c), []string{StageSegment, StageCorrect, StageTranslate, StagePublish}) {
		t.Fatalf("segment → 最後：%v，%v", stages(c), err)
	}
	for _, r := range c {
		if !r.Skip {
			t.Errorf("%s 應被略過", r.Stage)
		}
	}
	// until 在中間：到 until（含）為止。
	if c, _ = planChain(eff, StageSegment, StageCorrect); !slices.Equal(stages(c), []string{StageSegment, StageCorrect}) {
		t.Errorf("until=correct：%v", stages(c))
	}
	// 遇到第一個不略過的步驟就停下（含）。
	run := mustEffective(t, stagesOver("correct", map[string]any{"provider": "anthropic", "model": "m1"}))
	if c, _ = planChain(run, StageSegment, ""); !slices.Equal(stages(c), []string{StageSegment, StageCorrect}) || c[1].Skip {
		t.Errorf("correct 可執行時：%+v", c)
	}
	// from 就是第一個要執行的步驟。
	if c, _ = planChain(eff, StageNormalize, ""); !slices.Equal(stages(c), []string{StageNormalize}) || c[0].Skip {
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
	for _, s := range []string{StageMetadata, StageTranscript, StageNormalize} {
		if !stageImplemented(s, ExecutorLocal) {
			t.Errorf("%s／local 已實作", s)
		}
	}
	for _, c := range [][2]string{{StageSegment, ExecutorAPI}, {StageCorrect, ExecutorAgent}, {StageTranslate, ExecutorOpenCC}, {StagePublish, ExecutorLocal}, {StageNormalize, ExecutorAPI}} {
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
}
