package pipeline

import (
	"context"
	"errors"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// runNormalize 讀取原始字幕，交給 subtitle.Normalize 整理，產出 normalized 變體。
// 產出不依賴目標語言；來源紀錄指向 source 產出並記錄 impl_version（NRM-R8）。
func (p *Pipeline) runNormalize(ctx context.Context, it store.Item) (result, error) {
	arts, err := p.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		return result{}, err
	}
	var src *store.Artifact
	for i := range arts {
		if arts[i].Variant == string(subtitle.VariantSource) {
			src = &arts[i] // 取最近產生的
		}
	}
	if src == nil {
		return result{}, errPermanent(errNoSource.Error())
	}

	key := cacheKey{
		Item: it.ID, Stage: StageNormalize, Impl: p.implVersion(StageNormalize), Provider: "local", Upstream: src.SHA256,
	}.String()
	stage := &store.StageResult{Executor: "local", CacheKey: key, ImplVersion: p.implVersion(StageNormalize)}
	done := store.StageState{Status: store.StatusDone}

	// 同一份上游、同一版演算法的結果已經存在：重用（PIPE-R5）。
	if hit, err := p.st.Read().DoneStageByCacheKey(ctx, it.ID, StageNormalize, key); err == nil && hit.ArtifactID != "" {
		stage.ArtifactID = hit.ArtifactID
		return result{task: taskDone, advance: true, stage: stage, state: done}, nil
	}

	tr, err := readTranscript(p.dataDir, src.Path, src.SHA256)
	if err != nil {
		if errors.Is(err, ErrCorruptArtifact) {
			return result{}, errPermanent(err.Error())
		}
		return result{}, err
	}
	out := subtitle.Transcript{Lang: tr.Lang, Variant: subtitle.VariantNormalized, Speakers: tr.Speakers, Cues: subtitle.Normalize(tr.Cues)}

	prov, err := p.provenance(map[string]any{
		"stage": StageNormalize, "impl_version": p.implVersion(StageNormalize),
		"derived_from": []map[string]string{{"artifact": src.ID, "variant": src.Variant, "sha256": src.SHA256}},
	}, nil)
	if err != nil {
		return result{}, err
	}
	art, err := p.writeCuesArtifact(ctx, it.ID, out, []string{src.ID}, prov)
	if err != nil {
		return result{}, err
	}
	return result{task: taskDone, advance: true, stage: stage, artifact: art, state: done}, nil
}
