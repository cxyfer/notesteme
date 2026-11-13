package pipeline

import (
	"context"
	"slices"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// plannedStages 是 plan 第 5 節中、M1 還沒實作的步驟；重跑它們回傳 501，名稱不對則是 422。
var plannedStages = []string{"summary", "chapters", "segment", "correct", "translate", "frames.candidates", "frames.select", "note", "publish"}

// RerunRequest 是重跑的請求（POST /api/items/:id/rerun）。
type RerunRequest struct {
	// From 是從哪個步驟開始重新處理。
	From string
	// Overrides 是這次重跑的單次覆寫，疊在影音新增時的覆寫之上，只記錄在新步驟的設定快照中。
	Overrides map[string]any
	// Until 指定做到哪個步驟為止；空字串代表做到最後。每次重跑都重新決定（會更新影音的 until）。
	Until string
}

// Rerun 從指定步驟重新建立 stage run 與任務（PIPE-R7、plan 第 5.1 節）：
// From 與之後的每個步驟各建立一筆新的 stage run（pending），只有 From 立刻建立任務，
// 之後的步驟等上游完成再建立（PIPE-R1）。上游步驟不動。
// 新產出會讓同一組 (item, kind, variant, lang) 的舊版本標記 superseded_by（store.InsertArtifact）；
// 快取鍵相同時重用既有產出，不建立新版本（PIPE-R5）。
// 舊步驟中還沒被領取的任務會被取消。回傳最新的影音詳情。
//
// 錯誤：store.ErrNotFound、*InvalidError（步驟名稱或設定不正確）、*NotImplementedError（M1 之後的步驟）。
func (p *Pipeline) Rerun(ctx context.Context, itemID string, req RerunRequest) (Detail, error) {
	switch {
	case ValidStage(req.From):
	case slices.Contains(plannedStages, req.From):
		return Detail{}, &NotImplementedError{What: "重跑步驟 " + req.From}
	default:
		return Detail{}, invalidf("from 必須是 %v 其中之一。", Stages)
	}
	if req.Until != "" && !ValidStage(req.Until) {
		return Detail{}, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	// from 在 until 之後時，這次重跑一個步驟都不會執行；回傳 422 而不是悄悄放寬 until，
	// 讓呼叫端明確知道自己的請求互相矛盾（PIPE-R1）。
	if req.Until != "" && slices.Index(Stages, req.From) > slices.Index(Stages, req.Until) {
		return Detail{}, invalidf("from（%s）不能在 until（%s）之後。", req.From, req.Until)
	}

	it, err := p.st.Read().ItemByID(ctx, itemID)
	if err != nil {
		return Detail{}, err
	}
	if it.DeletedAt != nil {
		return Detail{}, store.ErrNotFound
	}
	global, err := p.globalConfig(ctx)
	if err != nil {
		return Detail{}, err
	}
	itemOverrides, err := decodeOverrides(it.Overrides)
	if err != nil {
		return Detail{}, err
	}
	eff, err := BuildEffective(global, itemOverrides, req.Overrides)
	if err != nil {
		return Detail{}, err
	}
	sha, cfgJSON, err := eff.Snapshot()
	if err != nil {
		return Detail{}, err
	}

	err = p.st.Tx(ctx, func(r *store.Repo) error {
		snap, err := r.EnsureSnapshot(ctx, sha, cfgJSON)
		if err != nil {
			return err
		}
		runs, err := r.StageRunsByItem(ctx, itemID)
		if err != nil {
			return err
		}
		latest := latestRuns(runs)
		var superseded []string
		for _, s := range Stages[slices.Index(Stages, req.From):] {
			if old, ok := latest[s]; ok {
				superseded = append(superseded, old.ID)
			}
		}
		if _, err := r.CancelPendingTasks(ctx, superseded); err != nil {
			return err
		}
		// 任務被取消的舊步驟不能殘留成 pending：標為 skipped（取消後的終止狀態，PIPE-R15）。
		// 執行中（running）的不動，它完成時 apply 會發現自己已不是最新而丟棄結果；
		// 已經是 done／failed／blocked 的保留原狀，那是它自己的結果。
		for _, s := range Stages[slices.Index(Stages, req.From):] {
			if old, ok := latest[s]; ok && (old.Status == store.StatusPending || old.Status == store.StatusWaiting) {
				if err := r.SetStageState(ctx, old.ID, supersededState()); err != nil {
					return err
				}
			}
		}
		if err := r.SetItemUntil(ctx, itemID, req.Until); err != nil {
			return err
		}
		for _, s := range Stages[slices.Index(Stages, req.From):] {
			run, err := r.InsertStageRun(ctx, store.NewStageRun{
				ItemID: itemID, Stage: s, Status: store.StatusPending, ConfigSnapshotID: snap,
			})
			if err != nil {
				return err
			}
			if s == req.From {
				if _, err := insertStageTask(ctx, r, itemID, run, queue.PriorityManual, false); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Detail{}, err
	}
	p.wake()
	return p.Item(ctx, itemID)
}

// latestRuns 回傳每個步驟最近建立的 stage run（重跑會讓同一個步驟有多筆）。
// runs 必須依建立順序排列（StageRunsByItem）。
func latestRuns(runs []store.StageRun) map[string]store.StageRun {
	out := make(map[string]store.StageRun, len(Stages))
	for _, r := range runs {
		out[r.Stage] = r
	}
	return out
}
