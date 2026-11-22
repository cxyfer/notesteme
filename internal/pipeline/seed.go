package pipeline

import (
	"context"
	"fmt"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// SeedRequest 是直接寫入一份字幕產出的請求。
type SeedRequest struct {
	ItemID  string
	Variant subtitle.Variant
	// Lang 為空時使用影音的原文語言，影音還不知道原文語言時用 en。
	Lang string
	Cues []subtitle.Cue
}

// SeedTranscript 把一份字幕直接寫成產出，跳過產生它的步驟。只給驗收測試的 harness 使用
// （ADR-0015：harness 可以直接存取 Core）；沒有任何 HTTP 端點或 CLI 指令呼叫它。
//
// 產出帶有來源紀錄（標記 seeded）。變體對應到步驟時（source → transcript、normalized → normalize、
// segmented → segment、corrected → correct、translated → translate），該步驟最新的 stage run 標為 done 並指向這份產出，
// 還沒被領取的任務取消：seed 一個變體就等於那個步驟完成，COR、TRN 的場景才能先 seed 上游變體再從下一步重跑。
// edited 沒有對應的步驟，只寫入產出。
func (p *Pipeline) SeedTranscript(ctx context.Context, req SeedRequest) error {
	if !req.Variant.Valid() {
		return invalidf("不認識的字幕變體 %q。", req.Variant)
	}
	it, err := p.st.Read().ItemByID(ctx, req.ItemID)
	if err != nil {
		return err
	}
	lang := req.Lang
	if lang == "" {
		if lang = it.OriginalLang; lang == "" {
			lang = "en"
		}
	}
	cues := make([]subtitle.Cue, len(req.Cues))
	for i, c := range req.Cues {
		if c.Timing == "" {
			c.Timing = subtitle.TimingSource
		}
		if c.Index == 0 {
			c.Index = i + 1
		}
		cues[i] = c
	}

	stage := ""
	switch req.Variant {
	case subtitle.VariantSource:
		stage = StageTranscript
	case subtitle.VariantNormalized:
		stage = StageNormalize
	case subtitle.VariantSegmented:
		stage = StageSegment
	case subtitle.VariantCorrected:
		stage = StageCorrect
	case subtitle.VariantTranslated:
		stage = StageTranslate
	}
	prov, err := p.provenance(map[string]any{"stage": stage, "impl_version": p.implVersion(stage), "seeded": true}, nil)
	if err != nil {
		return err
	}
	art, err := p.newCuesArtifact(it.ID, subtitle.Transcript{Lang: lang, Variant: req.Variant, Cues: cues}, nil, prov)
	if err != nil {
		return err
	}
	return p.st.Tx(ctx, func(r *store.Repo) error {
		a, err := r.InsertArtifact(ctx, *art)
		if err != nil {
			return err
		}
		if stage == "" {
			return nil
		}
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		run, ok := latestRuns(runs)[stage]
		if !ok {
			return fmt.Errorf("影音 %s 沒有步驟 %s", it.ID, stage)
		}
		if _, err := r.CancelPendingTasks(ctx, []string{run.ID}); err != nil {
			return err
		}
		if err := r.SetStageResult(ctx, run.ID, store.StageResult{Executor: "local", ImplVersion: p.implVersion(stage), ArtifactID: a.ID}); err != nil {
			return err
		}
		return r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusDone})
	})
}

// SeedLangMixed 把影音的原文語言標記為 mixed（夾雜多種語言）。只給驗收測試的 harness 使用（同 SeedTranscript）：
// 產品中判斷 mixed 的流程（SRC-R11）還沒有實作。
func (p *Pipeline) SeedLangMixed(ctx context.Context, itemID string) error {
	return p.st.Tx(ctx, func(r *store.Repo) error { return r.SetItemLangMixed(ctx, itemID, true) })
}
