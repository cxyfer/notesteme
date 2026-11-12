package pipeline

import (
	"context"
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
}

// Item 回傳影音詳情；不存在時回傳 store.ErrNotFound。
func (p *Pipeline) Item(ctx context.Context, id string) (Detail, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	all, err := r.StageRunsByItem(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	// 重跑之後同一個步驟有多筆 stage run，詳情只列最新的一筆，依步驟順序。
	latest := latestRuns(all)
	var stages []store.StageRun
	for _, s := range Stages {
		if run, ok := latest[s]; ok {
			stages = append(stages, run)
		}
	}
	probes, err := r.LatestProbes(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Item: it, Stages: stages, Sources: probes}, nil
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

// CuesWithItem 同 Cues，另外回傳影音（下載檔名要用標題）。
func (p *Pipeline) CuesWithItem(ctx context.Context, itemID string, q CuesQuery) (store.Item, subtitle.Transcript, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return store.Item{}, subtitle.Transcript{}, err
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
