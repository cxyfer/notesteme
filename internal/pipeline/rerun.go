package pipeline

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// RerunRequest 是重跑的請求（POST /api/items/:id/rerun）。
type RerunRequest struct {
	// From 是從哪個步驟開始重新處理。
	From string
	// Overrides 是這次重跑的單次覆寫，疊在影音新增時的覆寫之上，只記錄在新步驟的設定快照中。
	Overrides map[string]any
	// Until 指定做到哪個步驟為止；空字串代表做到最後。每次重跑都重新決定（會更新影音的 until）。
	Until string
	// Profile 是這次重跑改用的處理方案（PRF-R3）；空字串代表沿用影音記錄的處理方案（最新版本）。
	// 處理方案不存在時回 *InvalidError（path 為 profile）。
	Profile string
	// Chunks 只重新送出這些 chunk（LLM-R8，從 1 起算）：其他 chunk 重用相同輸入的結果，這些 chunk 一律重新送出。
	// 只能用在 LLM 步驟（From 是 segment、correct 或 translate），否則回 *InvalidError；編號必須是現行切分中實際存在的（translate 被沿用的目標沒有 chunk，編號會有空洞），否則 422。
	Chunks []int
	// ReuseCandidates 只用於 From 為 frames（FRM-R13）：沿用現行的候選畫面只重新選圖；沒有現行的候選或其他步驟時忽略。
	ReuseCandidates bool
	// Priority 是任務的優先級；0 代表手動重跑（queue.PriorityManual）。改用更好的字幕自動重跑用 backfill（SRC-R22）。
	Priority int
}

// Rerun 從指定步驟重新建立 stage run 與任務（PIPE-R7、plan 第 5.1 節）：
// From 與之後的每個步驟各建立一筆新的 stage run（pending），只有 From 立刻建立任務，
// 之後的步驟等上游完成再建立（PIPE-R1）。上游步驟不動。
// 新產出會讓同一組 (item, kind, variant, lang) 的舊版本標記 superseded_by（store.InsertArtifact）；
// 快取鍵相同時重用既有產出，不建立新版本（PIPE-R5）。
// 舊步驟中還沒被領取的任務會被取消。回傳最新的影音詳情。
//
// 沒有可執行的 executor 的步驟（off、沒有 provider 的 api、沒有輸出的 publish）直接 skipped，鏈在第一個要執行的步驟或 until 停下。
// 這個步驟還沒有實作時回 501，而且什麼都不寫（ADR-0015）。
//
// 錯誤：store.ErrNotFound、*InTrashError（影音在垃圾桶中，DEL-R1）、*InvalidError（步驟名稱或設定不正確）、
// *NotImplementedError（plannedStages，或要執行的步驟還沒有實作）。
// Chunks 只能用在 LLM 步驟；步驟不是用 api executor 時不使用。
func (p *Pipeline) Rerun(ctx context.Context, itemID string, req RerunRequest) (Detail, error) {
	prep, err := p.prepareRerun(ctx, itemID, req)
	if err != nil {
		return Detail{}, err
	}
	req, layers, sha, cfgJSON, layersJSON, resolve := prep.req, prep.layers, prep.sha, prep.cfgJSON, prep.layersJSON, prep.resolve

	if p.beforeRerunTx != nil {
		p.beforeRerunTx()
	}
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		// 上面讀取之後影音可能已被移到垃圾桶或永久刪除：在交易內重新檢查，否則會替垃圾桶中的影音建立任務。
		cur, err := r.ItemByID(ctx, itemID)
		if err != nil {
			return err
		}
		if cur.DeletedAt != nil {
			return &InTrashError{ItemID: cur.ID}
		}
		chain, err := resolve(r, cur)
		if err != nil {
			return err
		}
		snap, err := r.EnsureSnapshotLayers(ctx, sha, cfgJSON, layersJSON)
		if err != nil {
			return err
		}
		// 記下這次實際用到的處理方案與版本，之後不帶 profile 的重跑才會一致（D7）。
		if err := r.SetItemProfile(ctx, itemID, layers.Profile.Name, layers.Profile.Version); err != nil {
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
			if old, ok := latest[s]; ok && (old.Status == store.StatusPending || old.Status == store.StatusWaiting || old.Status == store.StatusAwaitingAgent) {
				if err := r.SetStageState(ctx, old.ID, supersededState()); err != nil {
					return err
				}
			}
		}
		if err := r.SetItemUntil(ctx, itemID, req.Until); err != nil {
			return err
		}
		fresh := map[string]store.StageRun{}
		for _, s := range Stages[slices.Index(Stages, req.From):] {
			run, err := r.InsertStageRun(ctx, store.NewStageRun{
				ItemID: itemID, Stage: s, Status: store.StatusPending, ConfigSnapshotID: snap,
			})
			if err != nil {
				return err
			}
			fresh[s] = run
		}
		// SRC-R18：從 transcript（sponsorblock）以前重跑時結束重新 Probe 的對應部分，完成後依新的結果重新判斷。
		if err := p.endReprobeOnRerun(ctx, r, itemID, req.From); err != nil {
			return err
		}
		prio := queue.PriorityManual
		if req.Priority != 0 {
			prio = req.Priority
		}
		// 只有鏈上的步驟處理：略過的 skipped，第一個要執行的建立任務；之後的等上游完成再前進（PIPE-R1）。
		if _, err = p.applyChain(ctx, r, itemID, chain, fresh, prio, false); err != nil {
			return err
		}
		// 鏈上的步驟全部直接略過時（例如從沒有輸出的 publish 重跑）沒有任務會寫回結果：在這裡判斷通知（NOTIFY-R3）。
		cur.UntilStage = req.Until
		return p.emitRunEvent(ctx, r, cur, chain[len(chain)-1].Stage)
	})
	if err != nil {
		return Detail{}, err
	}
	p.wake()
	return p.Item(ctx, itemID)
}

// rerunPrep 是 prepareRerun 在寫入之前算好的內容：Rerun 與批次重跑的 dry_run（LIB-R14）共用，
// 兩者對同一部影音的驗證與生效設定才會相同。
type rerunPrep struct {
	req                      RerunRequest // From 已換成實際的起點（rerunStart）
	it                       store.Item
	layers                   Layers
	eff                      Effective
	sha, cfgJSON, layersJSON string
	resolve                  func(repo *store.Repo, item store.Item) ([]StageResolution, error)
}

// prepareRerun 做 Rerun 寫入之前的全部檢查與計算，不寫入任何資料；錯誤同 Rerun。
func (p *Pipeline) prepareRerun(ctx context.Context, itemID string, req RerunRequest) (*rerunPrep, error) {
	switch {
	case gatedStage(req.From) && req.From != StageNote && !StageAvailable(req.From):
		// M3 的步驟與 M4 的 summary 加進 Stages 之後、實作之前（M3-S0、M4-S0）：從它重跑回 501，
		// 不留下半套的重跑（ADR-0020 第 1 點、ADR-0021 第 3 點）。note 在 Compose 之後判斷（GEM 可以重新產生）。
		return nil, &NotImplementedError{What: "重跑步驟 " + req.From}
	case ValidStage(req.From):
	case slices.Contains(plannedStages, req.From):
		return nil, &NotImplementedError{What: "重跑步驟 " + req.From}
	default:
		return nil, invalidf("from 必須是 %v 其中之一。", Stages)
	}
	if req.Until != "" && !ValidStage(req.Until) {
		return nil, invalidf("until 必須是 %v 其中之一。", Stages)
	}
	// from 在 until 之後時，這次重跑一個步驟都不會執行；回傳 422 而不是悄悄放寬 until，
	// 讓呼叫端明確知道自己的請求互相矛盾（PIPE-R1）。
	if req.Until != "" && slices.Index(Stages, req.From) > slices.Index(Stages, req.Until) {
		return nil, invalidf("from（%s）不能在 until（%s）之後。", req.From, req.Until)
	}

	if len(req.Chunks) > 0 {
		if !IsLLMStage(req.From) {
			return nil, invalidFields([]validate.FieldError{{Path: "chunks", Message: "chunks 只能用在 LLM 步驟（segment、correct、translate）。"}})
		}
		for i, c := range req.Chunks {
			if c < 1 {
				return nil, invalidFields([]validate.FieldError{{Path: fmt.Sprintf("chunks[%d]", i), Message: "chunk 必須是從 1 起算的整數。"}})
			}
		}
	}
	if err := p.validateOverrides(ctx, req.Overrides); err != nil {
		return nil, err
	}

	r0 := p.st.Read()
	it, err := r0.ItemByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if it.DeletedAt != nil {
		return nil, &InTrashError{ItemID: it.ID}
	}
	if req.From, err = p.rerunStart(ctx, r0, it.ID, req.From); err != nil {
		return nil, err
	}
	if len(req.Chunks) > 0 {
		// chunk 編號對照現行切分實際存在的編號（L12）：沒有切分過、或編號不存在時 422。
		// 不能用列數當上限：translate 的編號是各目標的全域編號，被沿用的目標不產生 chunk，編號會有空洞（S6b-Q3）。
		existing, err := r0.LatestLLMChunkNumbers(ctx, it.ID, req.From)
		if err != nil {
			return nil, err
		}
		if fs := checkRerunChunks(req.From, req.Chunks, existing); len(fs) > 0 {
			return nil, invalidFields(fs)
		}
	}
	itemOverrides, err := decodeOverrides(it.Overrides)
	if err != nil {
		return nil, err
	}
	// 處理方案（D7）：body 指定的；沒有就用影音記錄的（最新版本，PRF-R3）；M1 的影音沒有記錄，用預設處理方案。
	// 記錄的處理方案已被刪除時回 422（path 為 profile）。
	profile := req.Profile
	if profile == "" {
		profile = it.ProfileID
	}
	layers, err := p.currentLayers(ctx, r0, profile, it.ChannelID, itemOverrides, req.Overrides)
	if err != nil {
		return nil, err
	}
	eff, err := p.Compose(ctx, layers)
	if err != nil {
		return nil, err
	}
	if req.From == StageNote && !StageAvailable(StageNote) && !geminiEngine(eff) {
		// 同上：note 的一般流程還沒有實作（M3-S4）；engine: gemini 已經可以重新產生（GEM）。
		return nil, &NotImplementedError{What: "重跑步驟 " + req.From}
	}
	sha, cfgJSON, layersJSON, err := SnapshotOf(eff, layers)
	if err != nil {
		return nil, err
	}
	// 在寫入任何資料之前解析執行方式：要執行的步驟還沒有實作就回 501，不留下半套的重跑。
	// 交易內會用交易的讀取再解析一次：兩次之間 normalize 可能剛完成，原文語言就不同了（Codex P2）。
	resolve := func(repo *store.Repo, item store.Item) ([]StageResolution, error) {
		sc, err := p.planContext(ctx, repo, item, req.From)
		if err != nil {
			return nil, err
		}
		chain, err := planChain(eff, req.From, req.Until, sc)
		if err != nil {
			return nil, err
		}
		if last := chain[len(chain)-1]; !last.Skip && !stageImplemented(last.Stage, last.Executor) && !geminiNoteRes(eff, last.Stage, last.Executor) {
			return nil, &NotImplementedError{What: "步驟 " + last.Stage + "／" + last.Executor}
		}
		if len(req.Chunks) > 0 && chain[0].Stage == req.From {
			chain[0].Chunks = slices.Clone(req.Chunks)
		}
		if req.ReuseCandidates && chain[0].Stage == StageFrames {
			chain[0].ReuseCandidates = true
		}
		return chain, nil
	}
	if _, err := resolve(r0, it); err != nil {
		return nil, err
	}
	return &rerunPrep{req: req, it: it, layers: layers, eff: eff, sha: sha, cfgJSON: cfgJSON, layersJSON: layersJSON, resolve: resolve}, nil
}

// rerunStart 回傳重跑實際的起點。從 chapters、frames、note 重跑、而 sponsorblock 從來沒有執行過
// （最新的 stage run 還是 pending，也沒有現行的 sponsor_segments 產出）時，改從 sponsorblock 開始：
// 這三個步驟的輸入要排除片段內的 cue（SPB-R7），沒有查過就不知道片段。已經查過時不重新查詢（SPB-R8）。
// 例如影音只做到 metadata、再寫入校正後的字幕（seeding）就從 chapters 重跑（見 CHAP 待決問題 H8）。
func (p *Pipeline) rerunStart(ctx context.Context, r *store.Repo, itemID, from string) (string, error) {
	if from != StageChapters && from != StageFrames && from != StageNote {
		return from, nil
	}
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return "", err
	}
	sb, ok := latestRuns(runs)[StageSponsorBlock]
	if !ok || sb.Status != store.StatusPending {
		return from, nil
	}
	arts, err := r.CurrentArtifacts(ctx, itemID, KindSponsorSegments)
	if err != nil || len(arts) > 0 {
		return from, err
	}
	return StageSponsorBlock, nil
}

// checkRerunChunks 比對重跑要求的 chunk 編號與現行切分實際存在的編號（existing，由小到大）：
// 還沒有切分過是一筆 path 為 chunks 的錯誤；編號不存在於切分中，每一個回一筆 path 為 chunks[i] 的錯誤，訊息帶現有的編號。
func checkRerunChunks(stage string, requested, existing []int) []validate.FieldError {
	if len(existing) == 0 {
		return []validate.FieldError{{Path: "chunks", Message: fmt.Sprintf("尚無 chunk 切分：%s 還沒有用 LLM 處理過，請先不指定 chunks 重跑。", stage)}}
	}
	var fs []validate.FieldError
	for i, c := range requested {
		if _, found := slices.BinarySearch(existing, c); !found {
			fs = append(fs, validate.FieldError{
				Path:    fmt.Sprintf("chunks[%d]", i),
				Message: fmt.Sprintf("chunk %d 不存在於現行的切分；現有的 chunk 編號：%s。", c, formatChunkRanges(existing)),
			})
		}
	}
	return fs
}

// maxChunkRanges 是 formatChunkRanges 最多列出的區間數；超過時以「…」收尾，不讓錯誤訊息隨 chunk 數無限變長。
const maxChunkRanges = 20

// formatChunkRanges 把由小到大的編號寫成區間：[1 2 3 7 9 10] → "1～3、7、9～10"。
func formatChunkRanges(nums []int) string {
	var parts []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		if len(parts) == maxChunkRanges {
			parts = append(parts, "…")
			break
		}
		if j == i {
			parts = append(parts, strconv.Itoa(nums[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d～%d", nums[i], nums[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, "、")
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
