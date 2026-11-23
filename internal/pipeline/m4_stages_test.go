package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// 內建預設的快照在 M4-S0 之前（M3-S0 之後的 master，4895730）算出的 sha：summary 的預設值只在讀取時補上，
// 不寫進快照，所以既有處理方案的快照 sha 與快取鍵不變（ADR-0021 第 3 點）。
const (
	defaultSnapshotSHA   = "86085679e9e51a10021f5b9d0fbc96285baf60c67672da6889789183c16f59c8"
	enTargetSnapshotSHA  = "a7c8ca21a10c090ef27e856b2fafd2df1bd1d69fd60b71797040addd5cd5aaa1"
	summaryStagePosition = 3
)

func TestSummaryStageOrder(t *testing.T) {
	if i := slices.Index(Stages, StageSummary); i != summaryStagePosition || Stages[i-1] != StageNormalize || Stages[i+1] != StageSegment {
		t.Fatalf("summary 要在 normalize 之後、segment 之前（ADR-0021 第 3 點）：%v", Stages)
	}
	if slices.Contains(plannedStages, StageSummary) || !ValidStage(StageSummary) {
		t.Error("summary 已經是 Stages 的成員，不再是 plannedStages")
	}
	if IsLLMStage(StageSummary) || !IsM4Stage(StageSummary) || IsM4Stage(StageSegment) {
		t.Error("summary 是 M4 的步驟，不是 M2 的 LLM 步驟")
	}
	if !StageAvailable(StageSummary) || !stageImplemented(StageSummary, ExecutorAPI) || !stageImplemented(StageSummary, ExecutorAgent) {
		t.Error("M4-S6 起 summary 的 api 與 agent 已實作")
	}
	if c := resourceClass(StageSummary, ExecutorAPI, false); c != queue.ResourceLLM {
		t.Errorf("summary（api）的資源類別 = %s，預期 llm", c)
	}
	if c := resourceClass(StageSummary, ExecutorOff, false); c != queue.ResourceLocal {
		t.Errorf("summary（off）的資源類別 = %s", c)
	}
	if defaultImplVersion(StageSummary) != 1 {
		t.Error("summary 要登記 impl_version")
	}
}

func TestSnapshotSHAUnchangedBySummary(t *testing.T) {
	base, err := BuildEffective(nil)
	if err != nil {
		t.Fatal(err)
	}
	sha, cfg, err := base.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if sha != defaultSnapshotSHA || strings.Contains(cfg, StageSummary) {
		t.Errorf("內建預設的快照改變了：%s\n%s", sha, cfg)
	}
	en, err := BuildEffective(map[string]any{"language": map[string]any{"targets": []any{"en"}}})
	if err != nil {
		t.Fatal(err)
	}
	if sha, _, _ := en.Snapshot(); sha != enTargetSnapshotSHA {
		t.Errorf("覆寫 targets 的快照改變了：%s", sha)
	}
}

// summary 的解析（DIG-R1）：預設 off；api 沒有 provider 時 not_configured；不繼承其他步驟的 provider。
func TestResolveExecutorSummary(t *testing.T) {
	tests := []struct {
		name string
		over map[string]any
		want StageResolution
	}{
		{"預設 off", nil,
			StageResolution{Stage: StageSummary, Executor: ExecutorOff, Prompt: "summary-default", Skip: true, Reason: SkipOff}},
		{"只寫 language 仍是 off", stagesOver(StageSummary, map[string]any{"language": "zh-Hant"}),
			StageResolution{Stage: StageSummary, Executor: ExecutorOff, Prompt: "summary-default", Skip: true, Reason: SkipOff}},
		{"api 沒有 provider", stagesOver(StageSummary, map[string]any{"executor": "api"}),
			StageResolution{Stage: StageSummary, Executor: ExecutorAPI, Prompt: "summary-default", Skip: true, Reason: SkipNotConfigured}},
		{"不繼承 correct 的 provider", map[string]any{"stages": map[string]any{
			StageSummary: map[string]any{"executor": "api"},
			StageCorrect: map[string]any{"provider": "anthropic", "model": "m"},
		}}, StageResolution{Stage: StageSummary, Executor: ExecutorAPI, Prompt: "summary-default", Skip: true, Reason: SkipNotConfigured}},
		{"api 設定完整", stagesOver(StageSummary, map[string]any{"executor": "api", "provider": "anthropic", "model": "m"}),
			StageResolution{Stage: StageSummary, Executor: ExecutorAPI, Provider: "anthropic", Model: "m", Prompt: "summary-default"}},
		{"agent", stagesOver(StageSummary, map[string]any{"executor": "agent"}),
			StageResolution{Stage: StageSummary, Executor: ExecutorAgent, Prompt: "summary-default"}},
	}
	for _, tt := range tests {
		got, err := ResolveExecutor(mustEffective(t, tt.over), StageSummary, StageContext{Platform: "youtube"})
		if err != nil {
			t.Errorf("%s：%v", tt.name, err)
			continue
		}
		if got.Stage != tt.want.Stage || got.Executor != tt.want.Executor || got.Provider != tt.want.Provider ||
			got.Model != tt.want.Model || got.Prompt != tt.want.Prompt || got.Skip != tt.want.Skip || got.Reason != tt.want.Reason {
			t.Errorf("%s：%+v，預期 %+v", tt.name, got, tt.want)
		}
	}
	if _, err := BuildEffective(stagesOver(StageSummary, map[string]any{"executor": "rules"})); err == nil {
		t.Error("summary 不支援 rules")
	}
}

// 設定要執行的 summary（M4-S6）：鏈停在 summary，替它建立任務。
func TestPlanChainStopsAtSummary(t *testing.T) {
	for _, exec := range []string{ExecutorAPI, ExecutorAgent} {
		over := stagesOver(StageSummary, map[string]any{"executor": exec, "provider": "anthropic", "model": "m"})
		c, err := planChain(mustEffective(t, over), StageSummary, StageSegment, StageContext{})
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != 1 || c[0].Stage != StageSummary || c[0].Skip {
			t.Errorf("%s：鏈 = %+v", exec, c)
		}
	}
}

// 預設設定的新影音：summary 是 skipped（off）、沒有任務，後續步驟照常前進（308 個場景不受影響的前提）。
func TestFullRunSkipsSummaryWithoutTasks(t *testing.T) {
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	st := latestStage(t, r, it.ID, StageSummary)
	if st.Status != store.StatusSkipped || st.BlockReason != SkipOff || st.Message != "" {
		t.Errorf("summary = %+v，預期 skipped（off）", st)
	}
	if hasTask(t, r, it.ID, StageSummary) {
		t.Error("summary 不該有任務")
	}
	if st := latestStage(t, r, it.ID, StagePublish); st.Status != store.StatusSkipped || st.BlockReason != SkipNoOutputs {
		t.Errorf("publish = %+v", st)
	}
	// 設定了 provider 的 summary（M4-S6）：建立任務（rig 沒有 LLM provider，任務以 permanent 失敗）。
	r2 := newRig(t)
	r2.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it2 := r2.ingest(watch, stagesOver(StageSummary, map[string]any{"executor": "api", "provider": "anthropic", "model": "m"}), "")
	r2.run()
	if !hasTask(t, r2, it2.ID, StageSummary) {
		t.Error("要執行的 summary 應該有任務")
	}
}

// 從 summary 重跑（M4-S6）：預設 off 的 summary 直接 skipped（off）、鏈往下；until 可以是 summary。
func TestRerunFromSummary(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSummary}); err != nil {
		t.Fatalf("從 summary 重跑：%v", err)
	}
	if st := latestStage(t, r, it.ID, StageSummary); st.Status != store.StatusSkipped || st.BlockReason != SkipOff {
		t.Errorf("summary = %+v，預期 skipped（off）", st)
	}
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript, Until: StageSummary}); err != nil {
		t.Errorf("until=summary 應該可以：%v", err)
	}
}

// M3 時期建立的影音（沒有 summary 的 stage run）：normalize 之後跳過不存在的 summary，不補建。
func TestAdvanceSkipsMissingSummaryRun(t *testing.T) {
	m3 := slices.DeleteFunc(slices.Clone(Stages), func(s string) bool { return s == StageSummary })
	advanceWithRuns(t, m3)
}
