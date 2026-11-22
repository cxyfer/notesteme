package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// M3 的四個步驟在 S0 的解析（ADR-0020 第 2 點）：預設值與略過的原因；ResolveExecutor 不看有沒有實作。
func TestResolveExecutorM3Stages(t *testing.T) {
	yt := StageContext{Platform: "youtube"}
	tests := []struct {
		name  string
		over  map[string]any
		stage string
		sc    StageContext
		want  StageResolution
	}{
		{"sponsorblock 預設對 YouTube 執行", nil, StageSponsorBlock, yt,
			StageResolution{Stage: StageSponsorBlock, Executor: ExecutorLocal}},
		{"sponsorblock 不是 YouTube", nil, StageSponsorBlock, StageContext{Platform: "bilibili"},
			StageResolution{Stage: StageSponsorBlock, Executor: ExecutorLocal, Skip: true, Reason: SkipNotApplicable}},
		{"sponsorblock enabled: false 優先", stagesOver(StageSponsorBlock, map[string]any{"enabled": false}), StageSponsorBlock, StageContext{Platform: "local"},
			StageResolution{Stage: StageSponsorBlock, Executor: ExecutorLocal, Skip: true, Reason: SkipOff}},
		{"chapters 預設 not_configured", nil, StageChapters, yt,
			StageResolution{Stage: StageChapters, Executor: ExecutorAPI, Prompt: "chapters-default", Skip: true, Reason: SkipNotConfigured}},
		{"note 預設 not_configured", nil, StageNote, yt,
			StageResolution{Stage: StageNote, Executor: ExecutorAPI, Prompt: "note-default", Skip: true, Reason: SkipNotConfigured}},
		{"frames 預設 off", nil, StageFrames, yt,
			StageResolution{Stage: StageFrames, Executor: ExecutorOff, Prompt: "frames-default", Skip: true, Reason: SkipOff}},
		{"frames rules 不需要 provider", stagesOver(StageFrames, map[string]any{"executor": "rules"}), StageFrames, yt,
			StageResolution{Stage: StageFrames, Executor: ExecutorRules}},
		{"chapters 設定了 provider", stagesOver(StageChapters, map[string]any{"provider": "anthropic", "model": "m"}), StageChapters, yt,
			StageResolution{Stage: StageChapters, Executor: ExecutorAPI, Provider: "anthropic", Model: "m", Prompt: "chapters-default"}},
	}
	for _, tt := range tests {
		got, err := ResolveExecutor(mustEffective(t, tt.over), tt.stage, tt.sc)
		if err != nil {
			t.Errorf("%s：%v", tt.name, err)
			continue
		}
		if got.Stage != tt.want.Stage || got.Executor != tt.want.Executor || got.Provider != tt.want.Provider ||
			got.Model != tt.want.Model || got.Prompt != tt.want.Prompt || got.Skip != tt.want.Skip || got.Reason != tt.want.Reason {
			t.Errorf("%s：%+v，預期 %+v", tt.name, got, tt.want)
		}
	}
}

// 要執行而還沒有實作的 M3 步驟在鏈上改成 skipped（not_implemented），鏈繼續到 publish。
func TestPlanChainSkipsUnimplementedM3Stages(t *testing.T) {
	over := map[string]any{"stages": map[string]any{
		StageChapters: map[string]any{"provider": "anthropic", "model": "m"},
		StageFrames:   map[string]any{"executor": "rules"},
	}}
	c, err := planChain(mustEffective(t, over), StageSponsorBlock, "", StageContext{Platform: "youtube"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		StageSponsorBlock: SkipNotImplemented, StageChapters: SkipNotImplemented, StageFrames: SkipNotImplemented,
		StageNote: SkipNotConfigured, StagePublish: SkipNoOutputs,
	}
	if len(c) != len(want) {
		t.Fatalf("鏈 = %+v", c)
	}
	for _, r := range c {
		if !r.Skip || r.Reason != want[r.Stage] {
			t.Errorf("%s = %+v，預期 skipped（%s）", r.Stage, r, want[r.Stage])
		}
	}
	// has 過濾：沒有記錄的步驟不出現在鏈上。
	c, err = planChainFor(mustEffective(t, nil), StageSponsorBlock, "", StageContext{}, func(s string) bool { return s == StagePublish })
	if err != nil || len(c) != 1 || c[0].Stage != StagePublish {
		t.Errorf("只有 publish：%+v，%v", c, err)
	}
}

func TestStageAvailable(t *testing.T) {
	for _, s := range []string{StageMetadata, StageTranscript, StageNormalize, StageSegment, StageCorrect, StageTranslate, StagePublish} {
		if !StageAvailable(s) {
			t.Errorf("%s 應該已經實作", s)
		}
	}
	for _, s := range []string{StageSponsorBlock, StageChapters, StageFrames, StageNote, "summary", "frames.select", ""} {
		if StageAvailable(s) {
			t.Errorf("%s 不應該回報已實作", s)
		}
	}
}

// 預設設定的 YouTube 影音：M3 的四個步驟依序 skipped、不建任務，publish 照常前進（308 個場景不受影響的前提）。
func TestFullRunSkipsM3StagesWithoutTasks(t *testing.T) {
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	want := map[string]string{
		StageSponsorBlock: SkipNotImplemented, StageChapters: SkipNotConfigured, StageFrames: SkipOff,
		StageNote: SkipNotConfigured, StagePublish: SkipNoOutputs,
	}
	for s, reason := range want {
		st := latestStage(t, r, it.ID, s)
		if st.Status != store.StatusSkipped || st.BlockReason != reason || st.Message != "" {
			t.Errorf("%s = status %q, reason %q, message %q；預期 skipped（%s）", s, st.Status, st.BlockReason, st.Message, reason)
		}
		if hasTask(t, r, it.ID, s) {
			t.Errorf("%s 不該有任務", s)
		}
	}
}

// M2 建立的影音只有 7 個步驟的 stage run：translate 之後跳過不存在的 M3 步驟，publish 仍然前進。
func TestAdvanceSkipsMissingM3Runs(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	eff, err := BuildEffective(nil)
	if err != nil {
		t.Fatal(err)
	}
	sha, cfg, err := eff.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	m2 := []string{StageMetadata, StageTranscript, StageNormalize, StageSegment, StageCorrect, StageTranslate, StagePublish}
	var itemID string
	err = r.st.Tx(ctx, func(tx *store.Repo) error {
		it, _, err := tx.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: "abc123xxxxx", URL: watch})
		if err != nil {
			return err
		}
		itemID = it.ID
		snap, err := tx.EnsureSnapshot(ctx, sha, cfg)
		if err != nil {
			return err
		}
		var first store.StageRun
		for _, s := range m2 {
			run, err := tx.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: s, Status: store.StatusPending, ConfigSnapshotID: snap})
			if err != nil {
				return err
			}
			if s == StageMetadata {
				first = run
			}
		}
		_, err = insertStageTask(ctx, tx, it.ID, first, queue.PriorityManual, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r.run()
	if st := latestStage(t, r, itemID, StagePublish); st.Status != store.StatusSkipped || st.BlockReason != SkipNoOutputs {
		t.Errorf("publish = %+v，預期 skipped（no_outputs）", st)
	}
	runs, err := r.st.Read().StageRunsByItem(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		if IsM3Stage(run.Stage) {
			t.Errorf("不該補建 M3 的步驟：%+v", run)
		}
	}
}

// frames.candidates、frames.select 已合併成 frames（ADR-0020 第 1 點）：現在是不認識的名稱（422）。
func TestRerunRejectsOldFramesStageNames(t *testing.T) {
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	for _, name := range []string{"frames.candidates", "frames.select"} {
		var inv *InvalidError
		if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: name}); !errors.As(err, &inv) {
			t.Errorf("%s：err = %v，預期 *InvalidError", name, err)
		}
	}
}
