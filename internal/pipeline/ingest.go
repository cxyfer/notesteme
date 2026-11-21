package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

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
	// InTrash 為 true 代表影音在垃圾桶中：沒有重新建立，也不算在媒體庫中（DEL-R10）。
	// 這時 AlreadyInLibrary 是 false，Item 是垃圾桶中的那一筆。
	InTrash bool
}

// Ingest 新增影音並立刻建立 metadata 任務，優先順序為手動新增（ING-R12）。
// 影音以 (platform, external_id, part) 識別；已存在時不建立新項目（ING-R2）。
//
// YouTube 播放清單展開成多部影音，每部各自套用 ING-R2（ING-R4）；回傳的順序與清單相同。
//
// 錯誤：ErrUnrecognizedURL、ErrChannel、*InvalidError（422）、*UpstreamError（502，yt-dlp 失敗）、
// *NotImplementedError（501，例如沒有設定 Bilibili 依賴、處理方案）、*PolicyError（422，抓取 feed 或預檢音檔被防護擋下）。
//
// 網址只看網址認不出來時，當成 Podcast feed 處理（expandPodcast）：episodes 指定要新增的集，每集一部影音。
//
// 影音在垃圾桶中時不重新建立：結果的 InTrash 為 true（DEL-R10）。單一目標在垃圾桶中時，
// 是否回 409 由呼叫端決定（API 見 createItems）。
//
// Bilibili 每個分P 是一個影音（ING-R3）：網址沒有 p 時展開全部分P（或 parts 指定的），有 p=N 時只建立那一個。
func (p *Pipeline) Ingest(ctx context.Context, req IngestRequest) ([]IngestResult, error) {
	x, err := p.expandRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if x.podcast != nil {
		return p.insertTargets(ctx, req, x.plan, x.podcast.build(ctx))
	}
	return p.insertTargets(ctx, req, x.plan, func(*store.Repo) ([]source.Target, string, error) {
		return x.targets, "", nil
	})
}

// ingestExpansion 是辨識與展開的結果：要建立（或估價）的 Target，以及設定快照。
// 它不含任何寫入；新增（Ingest）與估價（Estimate）共用同一份辨識與展開。
type ingestExpansion struct {
	plan ingestPlan
	// targets 是 YouTube 與 Bilibili 展開後的影音；Podcast 為 nil，改看 podcast。
	targets []source.Target
	podcast *podcastSelection
}

// expandRequest 辨識網址並展開成要處理的影音（播放清單、Bilibili 分P、Podcast 的單集），不寫入資料庫。
// 驗證與錯誤見 Ingest。
func (p *Pipeline) expandRequest(ctx context.Context, req IngestRequest) (ingestExpansion, error) {
	t, ok := source.Recognize(req.URL)
	switch {
	case !ok:
		// 只看網址認不出來的，可能是 Podcast feed（要抓內容才知道，ING-R1）。
		return p.expandPodcast(ctx, req)
	case t.Kind == source.KindChannel:
		return ingestExpansion{}, ErrChannel
	}
	if req.Until != "" && !ValidStage(req.Until) {
		return ingestExpansion{}, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	switch {
	case t.Platform == source.PlatformYouTube:
	case t.Platform == source.PlatformBilibili && p.bili != nil:
	default:
		// 沒有註冊的平台（還沒實作，或 Bilibili 的依賴沒有設定）。
		return ingestExpansion{}, &NotImplementedError{What: "新增 " + t.Platform + " 的影音"}
	}
	if err := validateSelection(t, req); err != nil {
		return ingestExpansion{}, err
	}

	plan, err := p.planIngest(ctx, req)
	if err != nil {
		return ingestExpansion{}, err
	}

	// 播放清單在這裡才展開：前面的驗證都通過了，才花時間呼叫 yt-dlp。
	targets := []source.Target{t}
	switch {
	case t.Kind == source.KindPlaylist:
		if targets, err = p.expandPlaylist(ctx, t); err != nil {
			return ingestExpansion{}, err
		}
	case t.Platform == source.PlatformBilibili && t.Part == 0:
		// 網址沒有 p：展開分P（ING-R3）；有 p 時只建立那一個，不呼叫 yt-dlp。
		if targets, err = p.expandParts(ctx, t, req.Parts); err != nil {
			return ingestExpansion{}, err
		}
	}
	return ingestExpansion{plan: plan, targets: targets}, nil
}

// ingestPlan 是新增影音時算好的設定快照與單次覆寫，整批影音共用。
type ingestPlan struct {
	sha, cfgJSON string
	// layersJSON 是組成這份快照的各層（config_snapshots.layers_json）。
	layersJSON string
	// profile 是實際用到的處理方案與版本（寫進 items.profile_id、profile_version，D7）。
	profile   ProfileLayer
	overrides json.RawMessage
	// eff 是生效設定；估價用它取得 language，不必再從快照解析一次。
	eff Effective
}

// planIngest 合併各層（全域、處理方案、單次覆寫；頻道要到 metadata 完成才知道，D6），驗證覆寫的內容，算出設定快照。
// 要在任何耗時的外部呼叫之前做，覆寫或處理方案不正確時才不必先呼叫 yt-dlp 或抓取 feed。
// 處理方案沒有指定時用預設處理方案；不存在時回傳 path 為 profile 的 *InvalidError。
func (p *Pipeline) planIngest(ctx context.Context, req IngestRequest) (ingestPlan, error) {
	if err := p.validateOverrides(ctx, req.Overrides); err != nil {
		return ingestPlan{}, err
	}
	l, err := p.currentLayers(ctx, p.st.Read(), req.Profile, "", req.Overrides, nil)
	if err != nil {
		return ingestPlan{}, err
	}
	eff, err := p.Compose(ctx, l)
	if err != nil {
		return ingestPlan{}, err
	}
	plan := ingestPlan{overrides: json.RawMessage(`{}`), eff: eff, profile: l.Profile}
	if plan.sha, plan.cfgJSON, plan.layersJSON, err = SnapshotOf(eff, l); err != nil {
		return ingestPlan{}, err
	}
	if len(req.Overrides) > 0 {
		if plan.overrides, err = json.Marshal(req.Overrides); err != nil {
			return ingestPlan{}, err
		}
	}
	return plan, nil
}

// insertTargets 在同一個交易中為每個 Target 建立影音、步驟與 metadata 任務；
// 整份清單要嘛全部進來，要嘛都不建立。已經存在的不重新建立（ING-R2）。
//
// build 在交易裡呼叫，回傳要建立的 Target，以及（可為空）這些影音所屬的頻道 ID；
// Podcast 在這裡取得 feedKey 並建立頻道，與影音同生共死。build 可能因交易重試而被呼叫多次，必須可重複執行。
func (p *Pipeline) insertTargets(ctx context.Context, req IngestRequest, plan ingestPlan,
	build func(*store.Repo) (targets []source.Target, channelID string, err error)) ([]IngestResult, error) {
	priority := req.Priority
	if priority == 0 {
		priority = queue.PriorityManual
	}
	var results []IngestResult
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		results = results[:0]
		targets, channelID, err := build(r)
		if err != nil {
			return err
		}
		snap := ""
		for _, target := range targets {
			it, created, err := r.InsertItemIfAbsent(ctx, store.NewItem{
				Platform: target.Platform, ExternalID: target.ExternalID, Part: target.Part, URL: target.URL,
				Overrides: plan.overrides, UntilStage: req.Until,
				ProfileID: plan.profile.Name, ProfileVersion: plan.profile.Version,
			})
			if err != nil {
				return err
			}
			if !created {
				// 影音在垃圾桶中時不重新建立，標示 InTrash，由呼叫端提示還原（DEL-R10）。
				results = append(results, IngestResult{Item: it, AlreadyInLibrary: it.DeletedAt == nil, InTrash: it.DeletedAt != nil})
				continue
			}
			if channelID != "" {
				if err := r.SetItemChannel(ctx, it.ID, channelID); err != nil {
					return err
				}
				it.ChannelID = channelID
			}
			results = append(results, IngestResult{Item: it})
			// 手動新增視為「想要這部影音」：同一個交易中移除永久刪除留下的 tombstone（DEL-R11）。
			if _, err := r.DeleteTombstones(ctx, target.Platform, target.ExternalID, target.Part); err != nil {
				return err
			}
			if snap == "" {
				if snap, err = r.EnsureSnapshotLayers(ctx, plan.sha, plan.cfgJSON, plan.layersJSON); err != nil {
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
		if !r.AlreadyInLibrary && !r.InTrash {
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

// validateSelection 檢查 parts 與 episodes：YouTube 沒有可選的分P 或單集；Bilibili 沒有單集，
// 網址已經有 p 就不能再給 parts，parts 的編號從 1 起算。範圍要等展開之後才知道（見 expandParts）。
func validateSelection(t source.Target, req IngestRequest) error {
	switch {
	case t.Platform != source.PlatformBilibili:
		if len(req.Parts) > 0 || len(req.Episodes) > 0 {
			return invalidf("YouTube 影片沒有分P 或單集可以選擇。")
		}
	case len(req.Episodes) > 0:
		return invalidf("Bilibili 影片沒有單集可以選擇。")
	case t.Part > 0 && len(req.Parts) > 0:
		return invalidf("網址已經指定了分P（p=%d），不能再指定 parts。", t.Part)
	}
	for _, n := range req.Parts {
		if n < 1 {
			return invalidf("parts 的分P 編號必須從 1 起算，收到 %d。", n)
		}
	}
	return nil
}

// expandParts 以 yt-dlp 展開 Bilibili 影片的分P，回傳要建立的分P（ING-R3）：沒有指定 parts 時是全部，
// 指定時只有那些分P（重複的去掉，依分P 順序）；有編號不在影片的分P 範圍內時是 422。
//
// yt-dlp 失敗時的對應同播放清單（見 expandPlaylist）：影片不存在或有地區限制是 422，其他是 *UpstreamError（502）。
func (p *Pipeline) expandParts(ctx context.Context, t source.Target, want []int) ([]source.Target, error) {
	entries, err := p.bili.Parts(ctx, t.ExternalID)
	if err != nil {
		switch class, _ := errclass.ClassOf(err); {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case class == errclass.ClassPermanent:
			return nil, invalidf("找不到這部 Bilibili 影片，可能已刪除或有地區限制。")
		case class == errclass.ClassNeedsAction:
			var ce *errclass.Error
			if errors.As(err, &ce) && ce.Message != "" {
				return nil, &UpstreamError{Msg: ce.Message, Err: err}
			}
			return nil, &UpstreamError{Msg: "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。", Err: err}
		}
		return nil, &UpstreamError{Msg: "yt-dlp 讀取 Bilibili 分P 失敗，請稍後再試。", Err: err}
	}
	if len(entries) == 0 {
		return nil, invalidf("這部影片沒有可新增的分P。")
	}
	var targets []source.Target
	seen := map[int]bool{}
	for _, e := range entries {
		if seen[e.Part] || (len(want) > 0 && !slices.Contains(want, e.Part)) {
			continue
		}
		seen[e.Part] = true
		targets = append(targets, bilibiliPart(t.ExternalID, e.Part))
	}
	for _, n := range want {
		if !seen[n] {
			return nil, invalidf("這部影片沒有第 %d 個分P（共 %d 個）。", n, len(entries))
		}
	}
	return targets, nil
}

// insertStageTask 為步驟建立任務。transcriptASR 的意義見 resourceClass；不知道字幕軌時傳 false。
// run.Executor 是 agent 時，任務的 executor 也是 agent：內建 worker 只領 builtin 的任務，不會執行它（AGT-R1、ADR-0004）。
func insertStageTask(ctx context.Context, r *store.Repo, itemID string, run store.StageRun, priority int, transcriptASR bool) (store.Task, error) {
	payload, err := json.Marshal(map[string]string{"item_id": itemID})
	if err != nil {
		return store.Task{}, err
	}
	executor := queue.ExecutorBuiltin
	if run.Executor == ExecutorAgent {
		executor = ExecutorAgent
	}
	return r.InsertTask(ctx, store.NewTask{
		StageRunID: run.ID, Kind: run.Stage, Executor: executor,
		ResourceClass: resourceClass(run.Stage, run.Executor, transcriptASR), Priority: priority, Payload: payload,
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
