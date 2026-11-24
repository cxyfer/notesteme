package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cxyfer/notesteme/internal/langtag"
	"strings"

	"github.com/cxyfer/notesteme/internal/sponsorblock"
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
	// SponsorSegments 是現行的 SponsorBlock 片段（SPB-R4）；步驟沒有執行過（沒有產出）時是 nil，沒有片段時是空切片。
	SponsorSegments []sponsorblock.Segment
	// Chapters 是現行的章節（CHAP-R7）；chapters 步驟沒有產生過產出時是 nil。
	Chapters *Chapters
	// Batches 是等待 Batch API 結果的步驟（以 stage run ID 為 key）正在等的 batch（BATCH 的介面）。
	Batches map[string]*BatchView
	// Summary 是現行的摘要（DIG-R5）；沒有摘要產出時是 nil。
	Summary *Summary
	// Reprobe 是排定的重新 Probe（SRC-R18），沒有排程時是 nil；BetterSource 是發現的更好來源（SRC-R23），沒有時是 nil。
	Reprobe      *ReprobeView
	BetterSource *store.BetterSource
	// ImportedFrom 是最近一次從 bundle 匯入的來源（BND-R8、N7）；沒有從 bundle 匯入過時是 nil。
	ImportedFrom *store.BundleImport
	// HubOrigin 是現行的原始字幕從 Hub 取得時的來源（HSHR-R9 的 hub_origin）；現行的原始字幕不是來自 Hub 時是 nil。
	HubOrigin *HubOrigin
	// Running 是執行中（running）的步驟（以 stage run ID 為 key）的任務完成數與 provider（LIB-R24）；
	// 其他狀態的步驟不在其中。
	Running map[string]RunningStage
}

// RunningStage 是執行中步驟的補充說明（LIB-R24）：任務完成數的算法同總覽的 progress（LIB-R21，不含取消的任務），
// Provider 是 stage run 設定快照中 stages.<stage>.provider（沒有時是空字串）。
type RunningStage struct {
	Done, Total int
	Provider    string
}

// HubOrigin 是來源紀錄的 hub_origin（HSHR-R9）。Uploader 是 Hub 上的顯示名稱，是不可信的文字。
type HubOrigin struct {
	URL       string
	VersionID string
	SHA256    string
	Uploader  string
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
	var batches map[string]*BatchView
	var running map[string]RunningStage
	for _, s := range Stages {
		run, ok := latest[s]
		if !ok {
			continue
		}
		stages = append(stages, run)
		bv, err := waitingBatch(ctx, r, run)
		if err != nil {
			return Detail{}, err
		}
		if bv != nil {
			if batches == nil {
				batches = map[string]*BatchView{}
			}
			batches[run.ID] = bv
		}
		switch t, err := r.TaskByStageRun(ctx, run.ID); {
		case err == nil:
			attempts[run.ID] = t.Attempts
		case !errors.Is(err, store.ErrNotFound):
			return Detail{}, err
		}
		if run.Status == store.StatusRunning {
			rs, err := runningStage(ctx, r, run)
			if err != nil {
				return Detail{}, err
			}
			if running == nil {
				running = map[string]RunningStage{}
			}
			running[run.ID] = rs
		}
	}
	probes, err := r.LatestProbes(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Item: it, Stages: stages, Sources: probes, Attempts: attempts, Batches: batches, Running: running}
	if d.Reprobe, d.BetterSource, err = reprobeDetail(ctx, r, id); err != nil {
		return Detail{}, err
	}
	switch b, ok, err := r.LatestBundleImport(ctx, id); {
	case err != nil:
		return Detail{}, err
	case ok:
		d.ImportedFrom = &b
	}
	if d.HubOrigin, err = currentHubOrigin(ctx, r, id); err != nil {
		return Detail{}, err
	}
	// 產出檔讀不到時只略過這個欄位，不讓整個詳情失敗。
	switch segs, ok, err := p.SponsorSegments(ctx, id); {
	case err != nil:
		p.log.WarnContext(ctx, "讀取 SponsorBlock 片段失敗", "item", id, "error", err)
	case ok:
		d.SponsorSegments = segs
	}
	switch ch, ok, err := p.ItemChapters(ctx, id); {
	case err != nil:
		p.log.WarnContext(ctx, "讀取章節失敗", "item", id, "error", err)
	case ok:
		d.Chapters = &ch
	}
	switch sm, ok, err := p.ItemSummary(ctx, id); {
	case err != nil:
		p.log.WarnContext(ctx, "讀取摘要失敗", "item", id, "error", err)
	case ok:
		d.Summary = &sm
	}
	return d, nil
}

// runningStage 算出執行中步驟的任務完成數與 provider（LIB-R24）。設定快照讀不到或格式不對時只略過 provider：
// 這只是列表上的補充說明，不讓整個詳情失敗。
func runningStage(ctx context.Context, r *store.Repo, run store.StageRun) (RunningStage, error) {
	done, total, err := r.StageRunTaskProgress(ctx, run.ID)
	if err != nil {
		return RunningStage{}, err
	}
	rs := RunningStage{Done: done, Total: total}
	if snap, err := r.Snapshot(ctx, run.ConfigSnapshotID); err == nil {
		if eff, err := ParseEffective(snap); err == nil {
			if cfg, err := eff.Stage(run.Stage); err == nil {
				rs.Provider = cfg.Provider
			}
		}
	}
	return rs, nil
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
// 影音或步驟不存在時回傳 store.ErrNotFound。
func (p *Pipeline) StageConfig(ctx context.Context, itemID, stage string) (config, layers json.RawMessage, err error) {
	r := p.st.Read()
	if _, err := r.ItemByID(ctx, itemID); err != nil {
		return nil, nil, err
	}
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return nil, nil, err
	}
	run, ok := latestRuns(runs)[stage]
	if !ok {
		return nil, nil, store.ErrNotFound
	}
	cfg, err := r.Snapshot(ctx, run.ConfigSnapshotID)
	if err != nil {
		return nil, nil, err
	}
	ls, err := r.SnapshotLayers(ctx, run.ConfigSnapshotID)
	if err != nil {
		return nil, nil, err
	}
	return json.RawMessage(cfg), json.RawMessage(ls), nil
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
		arts = filterArtifacts(arts, func(a store.Artifact) bool { return sameLang(a.Lang, q.Lang) })
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
	tr, err := p.readTranscriptArtifact(ctx, chosen)
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

// sameLang 比較兩個語言標記：忽略大小寫，也接受底線與完整標記的等價拼法（zh_hant_tw 與 zh-Hant-TW）。
func sameLang(a, b string) bool {
	return strings.EqualFold(a, b) || langtag.Equal(a, b)
}
