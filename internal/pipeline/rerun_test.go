package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func seedSource(t *testing.T, r *rig, it store.Item, texts ...string) {
	t.Helper()
	var cues []subtitle.Cue
	for i, s := range texts {
		cues = append(cues, subtitle.Cue{StartMS: int64(i) * 2000, EndMS: int64(i+1) * 2000, Text: s})
	}
	if err := r.p.SeedTranscript(context.Background(), SeedRequest{ItemID: it.ID, Variant: subtitle.VariantSource, Cues: cues}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedMarksStageDoneAndCancelsItsTask(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "metadata")
	r.run()
	seedSource(t, r, it, "one", "two")
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || st.ArtifactID == "" {
		t.Fatalf("transcript = %+v", st)
	}
	tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 2 || tr.Lang != "en" || tr.Cues[0].Timing != subtitle.TimingSource {
		t.Fatalf("Cues = %+v, %v", tr, err)
	}
}

func TestRerunFromNormalizeCreatesNewVersion(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "metadata")
	r.run()
	seedSource(t, r, it, "so today we", "so today we talk")
	for i := 0; i < 2; i++ {
		if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Overrides: map[string]any{"language": map[string]any{"targets": []string{"en"}}}}); err != nil {
			t.Fatal(err)
		}
		r.run()
	}
	arts, err := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	var norm []store.Artifact
	for _, a := range arts {
		if a.Variant == "normalized" {
			norm = append(norm, a)
		}
	}
	// 第二次重跑的快取鍵與第一次相同：重用既有產出，不建立新版本（PIPE-R5）。
	if len(norm) != 1 {
		t.Fatalf("現行的 normalized = %d", len(norm))
	}
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "normalized", ""); n != 1 {
		t.Errorf("版本數 = %d，預期 1", n)
	}
	d, err := r.p.Item(ctx, it.ID)
	if err != nil || len(d.Stages) != len(Stages) {
		t.Fatalf("Item = %+v, %v", d, err)
	}
	if d.Stages[2].Stage != StageNormalize || d.Stages[2].Status != store.StatusDone {
		t.Errorf("stages = %+v", d.Stages)
	}
}

func TestRerunSupersedesWhenUpstreamChanges(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "metadata")
	r.run()
	seedSource(t, r, it, "alpha")
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	r.run()
	seedSource(t, r, it, "beta")
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "normalized", "en"); n != 2 {
		t.Fatalf("版本數 = %d，預期 2", n)
	}
	arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	for _, a := range arts {
		if a.SupersededBy != "" {
			t.Errorf("現行產出不應被取代：%+v", a)
		}
	}
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "normalized", ToMS: -1})
	if err != nil || len(tr.Cues) != 1 || tr.Cues[0].Text != "beta" {
		t.Fatalf("Cues = %+v, %v", tr, err)
	}
}

func TestRerunFromTranscriptRunsDownstreamOnce(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "")
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	r.run()
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	r.run()
	n, err := r.st.Read().CountTasks(ctx, store.TaskDone)
	if err != nil {
		t.Fatal(err)
	}
	// 第一輪 3 個任務，重跑 transcript 與 normalize 各一個。
	if n != 5 {
		t.Errorf("完成的任務 = %d，預期 5", n)
	}
}

func TestRerunErrors(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	it := r.ingest(watch, nil, "metadata")
	var ni *NotImplementedError
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: "summary"}); !errors.As(err, &ni) {
		t.Errorf("summary：%v", err)
	}
	var inv *InvalidError
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: "nope"}); !errors.As(err, &inv) {
		t.Errorf("nope：%v", err)
	}
	if _, err := r.p.Rerun(ctx, "missing", RerunRequest{From: StageNormalize}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing：%v", err)
	}
}

// 重跑 M2 的步驟：沒有可執行的 executor 時整條鏈 skipped，不建任務（PIPE-R7）。
func TestRerunFromM2StageSkipsChain(t *testing.T) {
	cases := []struct {
		from    string
		skipped []string
	}{
		{StageCorrect, []string{StageCorrect, StageTranslate, StagePublish}},
		{StageTranslate, []string{StageTranslate, StagePublish}},
		{StagePublish, []string{StagePublish}},
	}
	for _, c := range cases {
		r := newRig(t)
		ctx := context.Background()
		r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
		it := r.ingest(watch, nil, "")
		r.run()
		oldSegment := latestStage(t, r, it.ID, StageSegment)
		doneBefore, _ := r.st.Read().CountTasks(ctx, store.TaskDone)
		got := recordEvents(r.st)

		if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: c.from}); err != nil {
			t.Fatalf("from %s：%v", c.from, err)
		}
		r.run()
		for _, s := range c.skipped {
			st := latestStage(t, r, it.ID, s)
			if st.Status != store.StatusSkipped || st.Message != "" || st.ID == "" || hasTask(t, r, it.ID, s) {
				t.Errorf("from %s：%s = %+v", c.from, s, st)
			}
			if ev := stageEvents(got()); ev[s+"/pending"] != 1 || ev[s+"/skipped"] != 1 {
				t.Errorf("from %s：%s 的事件 %v", c.from, s, ev)
			}
		}
		if n, _ := r.st.Read().CountTasks(ctx, store.TaskDone); n != doneBefore {
			t.Errorf("from %s：沒有任務該執行，完成的任務 %d → %d", c.from, doneBefore, n)
		}
		// 上游不動。
		if now := latestStage(t, r, it.ID, StageSegment); c.from != StageSegment && now.ID != oldSegment.ID {
			t.Errorf("from %s：segment 不該被重跑", c.from)
		}
	}
}

// 重跑會落在還沒實作的 executor 或功能時回 501，而且不寫入任何資料（ADR-0015）。
func TestRerunNotImplementedWritesNothing(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()

	count := func() (runs, tasks int, evs int) {
		all, _ := r.st.Read().StageRunsByItem(ctx, it.ID)
		pending, _ := r.st.Read().CountTasks(ctx, store.TaskPending)
		n, _ := r.st.Read().CountEvents(ctx)
		return len(all), pending, int(n)
	}
	r0, t0, e0 := count()
	until, _ := r.st.Read().ItemByID(ctx, it.ID)

	var ni *NotImplementedError
	tests := []struct {
		name string
		req  RerunRequest
	}{
		{"summary", RerunRequest{From: "summary"}},
		{"frames.select", RerunRequest{From: "frames.select"}},
	}
	for _, tt := range tests {
		if _, err := r.p.Rerun(ctx, it.ID, tt.req); !errors.As(err, &ni) {
			t.Errorf("%s：err = %v，預期 *NotImplementedError", tt.name, err)
		}
		if r1, t1, e1 := count(); r1 != r0 || t1 != t0 || e1 != e0 {
			t.Errorf("%s：501 之後 stage run %d → %d、任務 %d → %d、事件 %d → %d", tt.name, r0, r1, t0, t1, e0, e1)
		}
	}
	// chunks 只能用在 LLM 步驟、從 1 起算：422，同樣什麼都不寫。
	var inv *InvalidError
	// correct 還沒有切分過：「尚無 chunk 切分」。
	for _, req := range []RerunRequest{{From: StageNormalize, Chunks: []int{1}}, {From: StageCorrect, Chunks: []int{0}}, {From: StageCorrect, Chunks: []int{1}}} {
		if _, err := r.p.Rerun(ctx, it.ID, req); !errors.As(err, &inv) {
			t.Errorf("%+v：err = %v，預期 *InvalidError", req, err)
		}
		if r1, t1, e1 := count(); r1 != r0 || t1 != t0 || e1 != e0 {
			t.Errorf("%+v：422 之後 stage run %d → %d、任務 %d → %d、事件 %d → %d", req, r0, r1, t0, t1, e0, e1)
		}
	}
	if now, _ := r.st.Read().ItemByID(ctx, it.ID); now.UntilStage != until.UntilStage {
		t.Errorf("until 被改動：%q → %q", until.UntilStage, now.UntilStage)
	}
	// 沒有實作的步驟只要鏈在它之前停下（until）就不是問題：until 之後的步驟不執行。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Until: StageNormalize,
		Overrides: stagesOver("correct", map[string]any{"provider": "anthropic", "model": "m1"})}); err != nil {
		t.Errorf("until=normalize 時 correct 不會被執行：%v", err)
	}
}

// seed 一個 M2 變體就等於那個步驟完成（COR、TRN 的 Background 先 seed 上游變體，再從下一步重跑）。
func TestSeedMarksM2StagesDone(t *testing.T) {
	cases := []struct {
		variant subtitle.Variant
		stage   string
	}{
		{subtitle.VariantSegmented, StageSegment},
		{subtitle.VariantCorrected, StageCorrect},
		{subtitle.VariantTranslated, StageTranslate},
	}
	for _, c := range cases {
		r := newRig(t)
		ctx := context.Background()
		it := r.ingest(watch, nil, StageMetadata)
		r.run()
		err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: c.variant, Cues: []subtitle.Cue{{StartMS: 0, EndMS: 2000, Text: "one"}}})
		if err != nil {
			t.Fatalf("%s：%v", c.variant, err)
		}
		st := latestStage(t, r, it.ID, c.stage)
		if st.Status != store.StatusDone || st.ArtifactID == "" {
			t.Errorf("%s → %s = %+v，預期 done 並指向產出", c.variant, c.stage, st)
		}
		if hasTask(t, r, it.ID, c.stage) {
			if tk, _ := r.st.Read().TaskByStageRun(ctx, st.ID); tk.State == store.TaskPending {
				t.Errorf("%s：未領取的任務應取消", c.stage)
			}
		}
		// 其他 M2 步驟不受影響。
		for _, s := range []string{StageSegment, StageCorrect, StageTranslate} {
			if s != c.stage && latestStage(t, r, it.ID, s).Status != store.StatusPending {
				t.Errorf("seed %s 不該動到 %s", c.variant, s)
			}
		}
	}
	// edited 沒有對應的步驟，只寫入產出。
	r := newRig(t)
	it := r.ingest(watch, nil, StageMetadata)
	r.run()
	if err := r.p.SeedTranscript(context.Background(), SeedRequest{ItemID: it.ID, Variant: subtitle.VariantEdited, Cues: []subtitle.Cue{{StartMS: 0, EndMS: 2000, Text: "one"}}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range m2Stages {
		if latestStage(t, r, it.ID, s).Status != store.StatusPending {
			t.Errorf("edited 不該動到 %s", s)
		}
	}
}
