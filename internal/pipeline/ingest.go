package pipeline

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/source/youtube"
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
// YouTube 播放清單展開成多部影音，每部各自套用 ING-R2（ING-R4）；回傳的順序與清單相同。
//
// 錯誤：ErrUnrecognizedURL、ErrChannel、*InvalidError（422）、*UpstreamError（502，yt-dlp 失敗）、
// *NotImplementedError（501，例如 Bilibili、Podcast、處理方案）。
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
	if t.Platform != source.PlatformYouTube {
		return nil, &NotImplementedError{What: "新增 " + t.Platform + " 的影音"}
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

	// 播放清單在這裡才展開：前面的驗證都通過了，才花時間呼叫 yt-dlp。
	targets := []source.Target{t}
	if t.Kind == source.KindPlaylist {
		if targets, err = p.expandPlaylist(ctx, t); err != nil {
			return nil, err
		}
	}

	priority := req.Priority
	if priority == 0 {
		priority = queue.PriorityManual
	}
	// 整份清單在同一個交易中建立：展開的結果要嘛全部進來，要嘛都不建立。
	var results []IngestResult
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		results = results[:0]
		snap := ""
		for _, target := range targets {
			it, created, err := r.InsertItemIfAbsent(ctx, store.NewItem{
				Platform: target.Platform, ExternalID: target.ExternalID, Part: target.Part, URL: target.URL,
				Overrides: overrides, UntilStage: req.Until,
			})
			if err != nil {
				return err
			}
			results = append(results, IngestResult{Item: it, AlreadyInLibrary: !created})
			if !created {
				// TODO(DEL-R10)：影音在垃圾桶中時的行為。
				continue
			}
			if snap == "" {
				if snap, err = r.EnsureSnapshot(ctx, sha, cfgJSON); err != nil {
					return err
				}
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
			if _, err := insertStageTask(ctx, r, it.ID, first, priority, false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		if !r.AlreadyInLibrary {
			p.wake()
			break
		}
	}
	return results, nil
}

// expandPlaylist 以 yt-dlp 展開 YouTube 播放清單，回傳每部影片的 Target（ING-R4）。
// 展開是一次性的：不記錄播放清單本身，之後清單的變動不會影響資料庫（持續追蹤是 M4 的訂閱）。
//
// yt-dlp 失敗時：播放清單不存在或私人是 422；找不到 yt-dlp 與其他失敗是 *UpstreamError（502），
// 原始錯誤留在 Err 供 log 使用。context 取消時原樣回傳。
func (p *Pipeline) expandPlaylist(ctx context.Context, t source.Target) ([]source.Target, error) {
	entries, err := p.yt.Playlist(ctx, t.ExternalID)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, youtube.ErrPlaylistGone):
			return nil, invalidf("找不到這個播放清單，可能已刪除或不是公開的。")
		}
		if class, ok := errclass.ClassOf(err); ok && class == errclass.ClassNeedsAction {
			// 錯誤自己帶了給使用者看的訊息時採用（例如 403：先更新 yt-dlp，SRC-R9）；否則是找不到 yt-dlp 之類。
			var ce *errclass.Error
			if errors.As(err, &ce) && ce.Message != "" {
				return nil, &UpstreamError{Msg: ce.Message, Err: err}
			}
			return nil, &UpstreamError{Msg: "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。", Err: err}
		}
		return nil, &UpstreamError{Msg: "yt-dlp 讀取播放清單失敗，請稍後再試。", Err: err}
	}
	if len(entries) == 0 {
		return nil, invalidf("這個播放清單沒有可新增的影片。")
	}
	targets := make([]source.Target, 0, len(entries))
	for _, e := range entries {
		targets = append(targets, source.Target{
			Platform: source.PlatformYouTube, Kind: source.KindVideo, ExternalID: e.ID, URL: youtube.WatchURL(e.ID),
		})
	}
	return targets, nil
}

// insertStageTask 為步驟建立任務。transcriptASR 的意義見 resourceClass；不知道字幕軌時傳 false。
func insertStageTask(ctx context.Context, r *store.Repo, itemID string, run store.StageRun, priority int, transcriptASR bool) (store.Task, error) {
	payload, err := json.Marshal(map[string]string{"item_id": itemID})
	if err != nil {
		return store.Task{}, err
	}
	return r.InsertTask(ctx, store.NewTask{
		StageRunID: run.ID, Kind: run.Stage, Executor: queue.ExecutorBuiltin,
		ResourceClass: resourceClass(run.Stage, transcriptASR), Priority: priority, Payload: payload,
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
