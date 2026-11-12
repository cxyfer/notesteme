package pipeline

import (
	"context"
	"encoding/json"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// IngestRequest 是新增影音的請求（ING 規格的 POST /api/items）。
type IngestRequest struct {
	URL      string
	Parts    []int
	Episodes []string
	Profile  string
	// Overrides 是單次覆寫（ING-R7），只影響這次新增的影音。
	Overrides map[string]any
	// Until 指定做到哪個步驟為止；空字串代表全部。
	Until string
	// Priority 是任務的優先順序（queue.PriorityXxx，PIPE-R9）；0 代表手動新增。
	// API 不開放指定：訂閱與回補來自內部的排程（之後的切片），測試也透過測試專用介面使用。
	Priority int
}

// IngestResult 是新增的結果：新建立的影音，或已經存在的影音（ING-R2）。
type IngestResult struct {
	Item             store.Item
	AlreadyInLibrary bool
}

// Ingest 新增影音並立刻建立 metadata 任務，優先順序為手動新增（ING-R12）。
// 影音以 (platform, external_id, part) 識別；已存在時不建立新項目（ING-R2）。
//
// 錯誤：ErrUnrecognizedURL、ErrChannel、*InvalidError（422）、*NotImplementedError（501，
// 例如 Bilibili、Podcast、播放清單展開、處理方案）。
func (p *Pipeline) Ingest(ctx context.Context, req IngestRequest) ([]IngestResult, error) {
	t, ok := source.Recognize(req.URL)
	switch {
	case !ok:
		return nil, ErrUnrecognizedURL
	case t.Kind == source.KindChannel:
		return nil, ErrChannel
	}
	if req.Until != "" && !ValidStage(req.Until) {
		return nil, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	switch {
	case t.Platform != source.PlatformYouTube:
		return nil, &NotImplementedError{What: "新增 " + t.Platform + " 的影音"}
	case t.Kind == source.KindPlaylist:
		return nil, &NotImplementedError{What: "展開播放清單"}
	}
	if req.Profile != "" {
		// TODO(PRF)：處理方案屬於 M2；之後在全域設定與單次覆寫之間插入處理方案這一層。
		return nil, &NotImplementedError{What: "處理方案"}
	}
	if len(req.Parts) > 0 || len(req.Episodes) > 0 {
		return nil, invalidf("YouTube 影片沒有分P 或單集可以選擇。")
	}

	global, err := p.globalConfig(ctx)
	if err != nil {
		return nil, err
	}
	eff, err := BuildEffective(global, req.Overrides)
	if err != nil {
		return nil, err
	}
	sha, cfgJSON, err := eff.Snapshot()
	if err != nil {
		return nil, err
	}
	overrides := json.RawMessage(`{}`)
	if len(req.Overrides) > 0 {
		if overrides, err = json.Marshal(req.Overrides); err != nil {
			return nil, err
		}
	}

	var res IngestResult
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		it, created, err := r.InsertItemIfAbsent(ctx, store.NewItem{
			Platform: t.Platform, ExternalID: t.ExternalID, Part: t.Part, URL: t.URL,
			Overrides: overrides, UntilStage: req.Until,
		})
		if err != nil {
			return err
		}
		res = IngestResult{Item: it, AlreadyInLibrary: !created}
		if !created {
			// TODO(DEL-R10)：影音在垃圾桶中時的行為。
			return nil
		}
		snap, err := r.EnsureSnapshot(ctx, sha, cfgJSON)
		if err != nil {
			return err
		}
		// 所有步驟一開始都是 pending；until 之後的步驟不建立任務（PIPE-R1）。
		var first store.StageRun
		for _, stage := range Stages {
			run, err := r.InsertStageRun(ctx, store.NewStageRun{
				ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: snap,
			})
			if err != nil {
				return err
			}
			if stage == StageMetadata {
				first = run
			}
		}
		priority := req.Priority
		if priority == 0 {
			priority = queue.PriorityManual
		}
		_, err = insertStageTask(ctx, r, it.ID, first, priority)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !res.AlreadyInLibrary {
		p.wake()
	}
	return []IngestResult{res}, nil
}

// insertStageTask 為步驟建立任務。
func insertStageTask(ctx context.Context, r *store.Repo, itemID string, run store.StageRun, priority int) (store.Task, error) {
	payload, err := json.Marshal(map[string]string{"item_id": itemID})
	if err != nil {
		return store.Task{}, err
	}
	return r.InsertTask(ctx, store.NewTask{
		StageRunID: run.ID, Kind: run.Stage, Executor: queue.ExecutorBuiltin,
		ResourceClass: resourceClass(run.Stage), Priority: priority, Payload: payload,
		MaxAttempts: queue.DefaultMaxAttempts,
	})
}

// decodeOverrides 讀出影音新增時記錄的單次覆寫。
func decodeOverrides(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}
