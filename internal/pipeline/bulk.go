package pipeline

import (
	"context"
	"errors"
	"regexp"
	"slices"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是媒體庫批次操作（LIB-R13～R20）在 pipeline 這一層的部分：解析 ids、已讀狀態與批次重跑的估算。
// 實際的動作（重跑、刪除、匯出）重用單一影音的方法（Rerun、Trash、ExportBundle），不另寫一套。

// MaxBatchItems 是一次批次操作最多的影音數（展開、去掉重複之後，LIB-R13）。
const MaxBatchItems = 500

// bareBiliRef 是不帶分P的 bili:<BV>：批次操作中展開成這個 BV 的所有分P（LIB-R13）。
var bareBiliRef = regexp.MustCompile(`^bili:([0-9A-Za-z]+)$`)

// BatchTarget 是批次中的一部影音。Err 為 nil 時 Item 是找到的影音；Err 是 store.ErrNotFound 或 *InTrashError，
// 後者的 Item 也有值（垃圾桶中的影音）。Raw 是請求中 ids 的寫法。
type BatchTarget struct {
	Raw  string
	Item store.Item
	Err  error
}

// ResolveBatch 依請求的順序解析 ids：影音 ID 或代號，不帶分P的 bili:<BV> 展開成所有分P；
// 同一部影音只留第一次出現的（找不到的以原始寫法去重複）。不改變任何資料。
func (p *Pipeline) ResolveBatch(ctx context.Context, ids []string) ([]BatchTarget, error) {
	r := p.st.Read()
	var out []BatchTarget
	seen := map[string]bool{}
	add := func(t BatchTarget) {
		key := "raw:" + t.Raw
		if t.Item.ID != "" {
			key = "id:" + t.Item.ID
		}
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, t)
	}
	for _, raw := range ids {
		if m := bareBiliRef.FindStringSubmatch(raw); m != nil {
			parts, err := r.ItemsByExternalID(ctx, source.PlatformBilibili, m[1])
			if err != nil {
				return nil, err
			}
			if len(parts) == 0 {
				add(BatchTarget{Raw: raw, Err: store.ErrNotFound})
			}
			for _, it := range parts {
				add(batchTarget(raw, it, nil))
			}
			continue
		}
		var it store.Item
		var err error
		if platform, ext, part, ok := source.SplitRef(raw); ok {
			it, err = r.ItemByKey(ctx, platform, ext, part)
		} else {
			it, err = r.ItemByID(ctx, raw)
		}
		switch {
		case errors.Is(err, store.ErrNotFound):
			add(BatchTarget{Raw: raw, Err: store.ErrNotFound})
		case err != nil:
			return nil, err
		default:
			add(batchTarget(raw, it, nil))
		}
	}
	return out, nil
}

func batchTarget(raw string, it store.Item, err error) BatchTarget {
	if err == nil && it.DeletedAt != nil {
		err = &InTrashError{ItemID: it.ID}
	}
	return BatchTarget{Raw: raw, Item: it, Err: err}
}

// SetReadState 設定影音的已讀狀態（LIB-R19）：state 是 store.ReadRead 或 store.ReadUnread。
// 影音不存在時回傳 store.ErrNotFound，在垃圾桶中時回傳 *InTrashError。
func (p *Pipeline) SetReadState(ctx context.Context, itemID, state string) error {
	if state != store.ReadRead && state != store.ReadUnread {
		return invalidf("read_state 必須是 read 或 unread。")
	}
	return p.st.Tx(ctx, func(r *store.Repo) error {
		it, err := r.ItemByID(ctx, itemID)
		if err != nil {
			return err
		}
		if it.DeletedAt != nil {
			return &InTrashError{ItemID: it.ID}
		}
		_, err = r.SetItemReadState(ctx, it.ID, state)
		return err
	})
}

// RerunEstimate 是一部影音從某個步驟重跑的預估（LIB-R14）。
type RerunEstimate struct {
	// USD 是能估算的付費任務的總和。
	USD cost.Micro
	// Unknown 是無法估算的付費任務數（找不到單價、輸入還不存在，或 vision、Gemini 等沒有估算方法的呼叫）。
	Unknown int
}

// EstimateRerun 估算 Rerun(itemID, {From: from, Profile: profile}) 會花多少錢，不寫入任何資料、不呼叫外部服務（LIB-R14）。
// 驗證與生效設定和 Rerun 相同（prepareRerun），所以 Rerun 會失敗的影音這裡回傳同樣的錯誤。
//
// 估算是近似值（LIB 待決問題 L-M10）：從 from 起每個要執行的步驟依目前的輸入字幕切分 chunk，每個 chunk 依 COST-R5 估算；
// 不扣除快取命中（是上限）；下游步驟的輸入用目前現有的字幕代替；agent 不經 API 計費，不計入。
func (p *Pipeline) EstimateRerun(ctx context.Context, itemID, from, profile string) (RerunEstimate, error) {
	prep, err := p.prepareRerun(ctx, itemID, RerunRequest{From: from, Profile: profile})
	if err != nil {
		return RerunEstimate{}, err
	}
	r := p.st.Read()
	sc, err := p.planContext(ctx, r, prep.it, prep.req.From)
	if err != nil {
		return RerunEstimate{}, err
	}
	var out RerunEstimate
	for _, stage := range Stages[slices.Index(Stages, prep.req.From):] {
		res, err := ResolveExecutor(prep.eff, stage, sc)
		if err != nil {
			return RerunEstimate{}, err
		}
		if res.Skip || (gatedStage(stage) && !stageImplemented(stage, res.Executor) && !geminiNoteRes(prep.eff, stage, res.Executor)) {
			continue
		}
		switch {
		case res.Executor != ExecutorAPI:
			// local、opencc、rules 不花錢；agent 由 Agent 自己的帳號計費（COST-R3），不經預算。
		case usesLLMEngine(stage, res.Executor):
			e, err := p.estimateLLMStage(ctx, r, prep.it, stage, prep.eff)
			if err != nil {
				return RerunEstimate{}, err
			}
			out.USD += e.USD
			out.Unknown += e.Unknown
		default:
			// vision 選圖、Gemini 筆記：沒有事先估算的方法。
			out.Unknown++
		}
	}
	return out, nil
}

// estimateLLMStage 估算一個 LLM 步驟：每個 chunk 一個請求，依 COST-R5 的 token 估算與單價（或測試控制的金額）。
// 輸入字幕還不存在、或找不到單價的請求計入 Unknown。
func (p *Pipeline) estimateLLMStage(ctx context.Context, r *store.Repo, it store.Item, stage string, eff Effective) (RerunEstimate, error) {
	if p.cost == nil {
		return RerunEstimate{Unknown: 1}, nil
	}
	job, err := p.prepareLLM(ctx, it, stage, eff, "")
	if tl := (*summaryTooLongError)(nil); errors.As(err, &tl) {
		return RerunEstimate{}, nil // DIG-R2：太長時 skipped，不呼叫 LLM
	}
	if err != nil {
		if ctx.Err() != nil {
			return RerunEstimate{}, ctx.Err()
		}
		return RerunEstimate{Unknown: 1}, nil
	}
	var out RerunEstimate
	for _, c := range job.chunks {
		req, err := llm.ChunkRequest(llm.RunOptions{Model: job.cfg.Model, Input: job.chunkInput(c), Spec: job.spec, Fixed: job.fixed})
		if err != nil {
			out.Unknown++
			continue
		}
		call := cost.Call{ItemID: it.ID, Stage: stage, Provider: job.cfg.Provider, Model: job.cfg.Model, CustomBase: p.customBase(job.cfg.Provider)}
		call.InputTokens, call.Output = requestTokens(req)
		est, priced, err := p.cost.Estimate(ctx, r, call)
		if err != nil {
			return RerunEstimate{}, err
		}
		if !priced && !p.cost.Estimated(stage) {
			out.Unknown++
			continue
		}
		out.USD += est
	}
	return out, nil
}

// customBase 回報 provider 是否用自訂的 base_url（COST-R2：自訂的不套用內建計價表）。
func (p *Pipeline) customBase(providerID string) bool {
	if prov, ok := p.llm.Provider(providerID); ok {
		if b, isBuiltin := llm.Builtin()[providerID]; isBuiltin && prov.BaseURL == b.BaseURL {
			return false
		}
	}
	return true
}
