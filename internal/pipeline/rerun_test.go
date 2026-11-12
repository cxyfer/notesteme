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
	if err != nil || len(d.Stages) != 3 {
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
	r.yt.info.Title = "changed" // 讓 transcript 的快取鍵改變，重跑才會產生新版本
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
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: "correct"}); !errors.As(err, &ni) {
		t.Errorf("correct：%v", err)
	}
	var inv *InvalidError
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: "nope"}); !errors.As(err, &inv) {
		t.Errorf("nope：%v", err)
	}
	if _, err := r.p.Rerun(ctx, "missing", RerunRequest{From: StageNormalize}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing：%v", err)
	}
}
