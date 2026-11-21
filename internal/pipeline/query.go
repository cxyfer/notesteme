package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// Items 列出影音（不含垃圾桶中的）。
func (p *Pipeline) Items(ctx context.Context) ([]store.Item, error) {
	return p.st.Read().ListItems(ctx)
}

// Detail 是影音的詳情：各步驟的狀態，以及最近一次 Probe 各來源的結果（SRC-R17）。
type Detail struct {
	Item    store.Item
	Stages  []store.StageRun
	Sources []store.Probe
	// Attempts 是各步驟（以 stage run ID 為 key）最近一個任務已經嘗試的次數；還沒有任務的步驟不在其中。
	Attempts map[string]int
}

// Item 回傳影音詳情；不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) Item(ctx context.Context, id string) (Detail, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	if it.DeletedAt != nil {
		return Detail{}, &InTrashError{ItemID: it.ID}
	}
	all, err := r.StageRunsByItem(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	// 重跑之後同一個步驟有多筆 stage run，詳情只列最新的一筆，依步驟順序。
	latest := latestRuns(all)
	var stages []store.StageRun
	attempts := map[string]int{}
	for _, s := range Stages {
		run, ok := latest[s]
		if !ok {
			continue
		}
		stages = append(stages, run)
		switch t, err := r.TaskByStageRun(ctx, run.ID); {
		case err == nil:
			attempts[run.ID] = t.Attempts
		case !errors.Is(err, store.ErrNotFound):
			return Detail{}, err
		}
	}
	probes, err := r.LatestProbes(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Item: it, Stages: stages, Sources: probes, Attempts: attempts}, nil
}

// Runs 列出影音實際執行過的步驟與當時的生效設定，由新到舊（PIPE-R13）；
// 影音不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) Runs(ctx context.Context, id string) ([]store.ExecutedRun, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, &InTrashError{ItemID: it.ID}
	}
	return r.ExecutedRunsByItem(ctx, id)
}

// StageConfig 回傳影音某個步驟最新一筆 stage run 綁定的快照：config 是生效設定，layers 是組成它的各層
// （PRF-R2；M1 的舊快照是 {}）。只給驗收測試的 harness 使用（透過 app.Server.StageConfig），沒有 API 端點。
//
// TODO(WP-2)：實作（M2 S1/S2 設計第 4.5 節）；契約階段回傳 *NotImplementedError。
func (p *Pipeline) StageConfig(ctx context.Context, itemID, stage string) (config, layers json.RawMessage, err error) {
	return nil, nil, &NotImplementedError{What: "影音的生效設定查詢"}
}

// CuesQuery 是取得字幕內容的條件。
type CuesQuery struct {
	// Variant 為空時依 OUT-R2 選處理得最完整的變體。
	Variant string
	// Lang 為空時使用影音的原文語言，沒有符合的就用最近產生的。
	Lang string
	// FromMS、ToMS 限定時間範圍；ToMS 小於 0 代表沒有上限（OUT-R3）。
	FromMS int64
	ToMS   int64
}

// ErrNoTranscript 表示影音還沒有符合條件的字幕。
var ErrNoTranscript = errors.New("這部影音還沒有符合條件的字幕。")

// Cues 回傳字幕。找不到符合的產出時回傳 ErrNoTranscript。
func (p *Pipeline) Cues(ctx context.Context, itemID string, q CuesQuery) (subtitle.Transcript, error) {
	_, tr, err := p.CuesWithItem(ctx, itemID, q)
	return tr, err
}

// CuesWithItem 同 Cues，另外回傳影音（下載檔名要用標題）。影音在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) CuesWithItem(ctx context.Context, itemID string, q CuesQuery) (store.Item, subtitle.Transcript, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return store.Item{}, subtitle.Transcript{}, err
	}
	if it.DeletedAt != nil {
		return store.Item{}, subtitle.Transcript{}, &InTrashError{ItemID: it.ID}
	}
	if q.Variant != "" && !subtitle.Variant(q.Variant).Valid() {
		return store.Item{}, subtitle.Transcript{}, invalidf("variant 必須是 source、normalized、segmented、corrected、translated 或 edited。")
	}
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return store.Item{}, subtitle.Transcript{}, err
	}
	// 先依語言篩選，再選變體：沒指定變體時，用篩選後還有的變體中最完整的。
	if q.Lang != "" {
		arts = filterArtifacts(arts, func(a store.Artifact) bool { return strings.EqualFold(a.Lang, q.Lang) })
	}
	variant := subtitle.Variant(q.Variant)
	if variant == "" {
		var have []subtitle.Variant
		for _, a := range arts {
			have = append(have, subtitle.Variant(a.Variant))
		}
		v, ok := subtitle.PickVariant(have)
		if !ok {
			return store.Item{}, subtitle.Transcript{}, ErrNoTranscript
		}
		variant = v
	}
	arts = filterArtifacts(arts, func(a store.Artifact) bool { return a.Variant == string(variant) })
	if len(arts) == 0 {
		return store.Item{}, subtitle.Transcript{}, ErrNoTranscript
	}
	chosen := arts[len(arts)-1] // 最近產生的
	if q.Lang == "" && it.OriginalLang != "" {
		for _, a := range arts {
			if strings.EqualFold(a.Lang, it.OriginalLang) {
				chosen = a
			}
		}
	}
	tr, err := readTranscript(p.dataDir, chosen.Path, chosen.SHA256)
	if err != nil {
		return store.Item{}, subtitle.Transcript{}, err
	}
	tr.Cues = subtitle.FilterRange(tr.Cues, q.FromMS, q.ToMS)
	return it, tr, nil
}

func filterArtifacts(in []store.Artifact, keep func(store.Artifact) bool) []store.Artifact {
	var out []store.Artifact
	for _, a := range in {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}
