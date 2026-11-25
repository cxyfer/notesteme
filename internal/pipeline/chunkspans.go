package pipeline

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 這個檔案是 LLM chunk 的時間範圍（LIB-R29，GET /api/items/:id/chunks）：時間帶畫出各 chunk 處理到哪裡
// （design-language.md §6.1 的「處理中」「失敗」），以及把拖曳選取的時間範圍換成 POST /rerun 的 chunks。

// chunk 的狀態（LIB-R29）。done、fallback 來自 llm_chunks；還沒有結果的 chunk 依它最近一個任務的狀態推得。
const (
	ChunkSpanPending  = "pending"
	ChunkSpanRunning  = "running"
	ChunkSpanFailed   = "failed"
	ChunkSpanDone     = "done"
	ChunkSpanFallback = "fallback"
)

// ChunkSpan 是一個 LLM chunk 的時間範圍與狀態（LIB-R29）。
type ChunkSpan struct {
	Stage string
	// Chunk 是 llm_chunks 的編號，就是 POST /rerun 的 chunks 使用的編號（translate 是各目標的全域編號）。
	Chunk  int
	Status string
	// Lang 是 translate 的目標語言；其他步驟是空字串。
	Lang string
	// StartMS、EndMS 是這個 chunk 負責輸出的句子（不含開頭和前一個 chunk 重疊的句子，重疊區以前一個 chunk 為準，LLM-R1）
	// 從第一句的開始到最後一句的結束。HasTime 為 false 代表輸入的字幕讀不到（已清除、損毀）或範圍和輸入不符。
	StartMS, EndMS int64
	HasTime        bool
	// Current 為 true 代表這次切分屬於這個步驟目前（最新）的 stage run；false 是更早的 stage run 的切分
	// （例如之後的執行整個重用了快取，或新的執行還沒有切分）。
	Current bool
}

// Overlaps 回報 chunk 的時間範圍和 [fromMS, toMS) 有沒有重疊；toMS <= 0 代表沒有上限。沒有時間的 chunk 不重疊。
func (c ChunkSpan) Overlaps(fromMS, toMS int64) bool {
	if !c.HasTime {
		return false
	}
	return c.EndMS > fromMS && (toMS <= 0 || c.StartMS < toMS)
}

// ItemChunks 回傳影音的這些 LLM 步驟（segment、correct、translate）最近一次切分的每個 chunk，依步驟順序、chunk 編號。
// 和 POST /rerun 的 chunks 對照同一次切分（store.LatestLLMChunkNumbers）。唯讀：輸入的字幕讀不到時只省略時間，
// 不觸發 NFR-REL-2 的重跑。不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) ItemChunks(ctx context.Context, itemID string, stages []string) ([]ChunkSpan, error) {
	r := p.st.Read()
	it, err := r.ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, &InTrashError{ItemID: it.ID}
	}
	runs, err := r.StageRunsByItem(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	latest := latestRuns(runs)
	inputs := map[string]*subtitle.Transcript{}
	out := []ChunkSpan{}
	for _, stage := range Stages {
		if !IsLLMStage(stage) || (len(stages) > 0 && !slices.Contains(stages, stage)) {
			continue
		}
		runID, err := r.LatestLLMChunkRun(ctx, it.ID, stage)
		if err != nil {
			return nil, err
		}
		if runID == "" {
			continue
		}
		rows, err := r.LLMChunks(ctx, runID)
		if err != nil {
			return nil, err
		}
		states, err := r.ChunkTaskStates(ctx, runID)
		if err != nil {
			return nil, err
		}
		cur, ok := latest[stage]
		current := ok && cur.ID == runID
		for _, row := range rows {
			span := ChunkSpan{Stage: stage, Chunk: row.Chunk, Status: chunkSpanStatus(row.Status, states[row.Chunk]), Current: current}
			p.chunkTime(ctx, r, &span, row, inputs)
			out = append(out, span)
		}
	}
	return out, nil
}

func chunkSpanStatus(chunk, task string) string {
	switch chunk {
	case store.ChunkDone:
		return ChunkSpanDone
	case store.ChunkFallback:
		return ChunkSpanFallback
	}
	switch task {
	case store.TaskLeased, store.TaskWaiting:
		return ChunkSpanRunning
	case store.TaskFailed:
		return ChunkSpanFailed
	}
	return ChunkSpanPending
}

// chunkTime 依 plan_json 與輸入的字幕填入時間範圍（與目標語言）。讀不到時不填，不回傳錯誤。
func (p *Pipeline) chunkTime(ctx context.Context, r *store.Repo, span *ChunkSpan, row store.LLMChunk, inputs map[string]*subtitle.Transcript) {
	var plan trnChunkPlan // segment、correct 的 chunkPlan 是它的子集（chunk 的範圍與 input）
	if json.Unmarshal(row.Plan, &plan) != nil || plan.Input == "" {
		return
	}
	var target *trnTarget
	if row.Stage == StageTranslate {
		if plan.Target < 0 || plan.Target >= len(plan.Targets) {
			return
		}
		target = &plan.Targets[plan.Target]
		span.Lang = target.Lang
	}
	tr, ok := inputs[plan.Input]
	if !ok {
		tr = nil
		if a, err := r.Artifact(ctx, plan.Input); err == nil {
			if t, err := readTranscript(p.dataDir, a.Path, a.SHA256); err == nil {
				tr = &t
			}
		}
		inputs[plan.Input] = tr
	}
	if tr == nil {
		return
	}
	// 輸出的範圍（從 1 起算、含兩端）：不含開頭的重疊句
	first, last := plan.From+plan.Overlap, plan.To
	if target != nil {
		// translate 的範圍是翻譯單位（localize 一個 cue 一個單位；translate 依句末標點組句群，TRN-R2），換回 cue
		units := translateUnits(tr, *target)
		if first < 1 || last > len(units) || first > last {
			return
		}
		first, last = units[first-1].First+1, units[last-1].First+units[last-1].Count
	}
	if first < 1 || last > len(tr.Cues) || first > last {
		return
	}
	span.StartMS, span.EndMS, span.HasTime = tr.Cues[first-1].StartMS, tr.Cues[last-1].EndMS, true
}

// translateUnits 重建 trnJobFor 的翻譯單位（只需要範圍，不需要轉換後的文字：OpenCC 不改變句數）。
func translateUnits(tr *subtitle.Transcript, target trnTarget) []llm.Unit {
	if target.Kind == TargetLocalize {
		units := make([]llm.Unit, len(tr.Cues))
		for k := range units {
			units[k] = llm.Unit{First: k, Count: 1}
		}
		return units
	}
	texts := make([]string, len(tr.Cues))
	for k, c := range tr.Cues {
		texts[k] = c.Text
	}
	return llm.BuildUnits(texts)
}
