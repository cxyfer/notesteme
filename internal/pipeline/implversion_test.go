package pipeline

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestImplVersionDefaultsAndOverride(t *testing.T) {
	r := newRig(t, func(d *Deps) { d.ImplVersions = map[string]int{StageTranscript: 3} })
	if r.p.implVersion(StageMetadata) != 1 || r.p.implVersion(StageNormalize) != 1 || r.p.implVersion(StageTranscript) != 3 {
		t.Errorf("Deps 的覆寫只影響指定的步驟：%d %d %d",
			r.p.implVersion(StageMetadata), r.p.implVersion(StageTranscript), r.p.implVersion(StageNormalize))
	}
	if err := r.p.SetImplVersion(StageNormalize, 2); err != nil || r.p.implVersion(StageNormalize) != 2 {
		t.Errorf("SetImplVersion = %v，版本 %d", err, r.p.implVersion(StageNormalize))
	}
	if err := r.p.SetImplVersion("nope", 2); err == nil {
		t.Error("不認識的步驟應回報錯誤")
	}
	if err := r.p.SetImplVersion(StageNormalize, 0); err == nil {
		t.Error("版本小於 1 應回報錯誤")
	}
}

// PIPE-R6：步驟的 impl_version 改變後，舊的快取不再命中，重跑產生新版本。
func TestNewImplVersionInvalidatesCache(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	first := r.stage(it, StageNormalize)
	if first.ImplVersion != 1 {
		t.Fatalf("第一次的 impl_version = %d", first.ImplVersion)
	}

	rerun := func() store.StageRun {
		t.Helper()
		d, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize})
		if err != nil {
			t.Fatal(err)
		}
		r.run()
		for _, s := range d.Stages {
			if s.Stage == StageNormalize {
				got, err := r.st.Read().StageRun(ctx, s.ID)
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
		}
		t.Fatal("重跑後沒有 normalize")
		return store.StageRun{}
	}

	// 版本沒變：快取命中，不產生新版本。
	if same := rerun(); same.ArtifactID != first.ArtifactID || same.CacheKey != first.CacheKey {
		t.Errorf("版本沒變應命中快取：%+v，原本 %+v", same, first)
	}
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "normalized", "en"); n != 1 {
		t.Errorf("快取命中不應產生新版本：%d", n)
	}

	// 版本改變：快取鍵不同，重新產生新的版本。
	if err := r.p.SetImplVersion(StageNormalize, 2); err != nil {
		t.Fatal(err)
	}
	bumped := rerun()
	if bumped.ImplVersion != 2 || bumped.CacheKey == first.CacheKey || bumped.ArtifactID == first.ArtifactID {
		t.Errorf("版本改變應重新產生：%+v，原本 %+v", bumped, first)
	}
	if n, _ := r.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "normalized", "en"); n != 2 {
		t.Errorf("normalized 產出應有 2 個版本：%d", n)
	}
}
