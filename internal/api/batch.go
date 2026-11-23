package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是媒體庫的批次操作 POST /api/items/batch（LIB-R13～R20）。
// 每部影音重用單一影音的方法（Rerun、Trash、SetReadState、DeletionPreview），批次不另外放寬任何規則。

// 批次的動作（LIB-R13）。
const (
	BatchRerun         = "rerun"
	BatchChangeProfile = "change_profile"
	BatchDelete        = "delete"
	BatchMarkRead      = "mark_read"
	BatchMarkUnread    = "mark_unread"
	BatchExportBundle  = "export_bundle"
)

// CodeBatchCancelled 是請求中斷時沒有處理的影音（批次的 items[].code）。
const CodeBatchCancelled = "cancelled"

// batchScopes 是每個動作需要的 scope（LIB-R16）：delete 同 DEL-R12，export_bundle 同 BND 的匯出（read）。
var batchScopes = map[string]string{
	BatchRerun: auth.ScopeIngest, BatchChangeProfile: auth.ScopeIngest, BatchMarkRead: auth.ScopeIngest,
	BatchMarkUnread: auth.ScopeIngest, BatchDelete: auth.ScopeAdmin, BatchExportBundle: auth.ScopeRead,
}

// batchMaxBodyBytes 是請求本體的上限：500 個代號約 20 KiB，留足空間但不讓本體無限大。
const batchMaxBodyBytes = 128 << 10

func (rt *routes) registerBatch(api huma.API) {
	// 路由層只要求已登入（scope 不互相包含：只有 ingest 的 token 也要能 rerun）；各動作需要的 scope 在 handler 依 action 判斷，
	// 在讀取任何影音之前（LIB-R16）。
	op := huma.Operation{
		OperationID: "items-batch", Method: http.MethodPost, Path: "/api/items/batch",
		Summary: "對多部影音執行同一個動作（rerun、change_profile、delete、mark_read、mark_unread、export_bundle，LIB-R13）；" +
			"dry_run 只回報影響範圍；scope 依動作：delete 要 admin，export_bundle 要 read，其他要 ingest（LIB-R16）",
		MaxBodyBytes: batchMaxBodyBytes,
	}
	op.Middlewares = append(op.Middlewares, humaRequireSignedIn)
	huma.Register(api, op, rt.batchItems)
}

// humaRequireSignedIn 在 Huma 讀取本體之前拒絕沒有通過驗證的請求（401）。
func humaRequireSignedIn(hc huma.Context, next func(huma.Context)) {
	if _, ok := principalFrom(hc.Context()); !ok {
		writeHumaError(hc, newError(http.StatusUnauthorized, CodeUnauthorized, "尚未登入或登入已失效。"))
		return
	}
	next(hc)
}

// 欄位都是選填（omitempty）：缺少或不正確時由 handler 回 422 與 LIB-R13 的訊息，不用 Huma 的通用訊息。
type batchInput struct {
	Body struct {
		Action        string   `json:"action,omitempty" doc:"rerun、change_profile、delete、mark_read、mark_unread 或 export_bundle"`
		IDs           []string `json:"ids,omitempty" doc:"影音的 ID 或代號；不帶分P的 bili:<BV> 展開成所有分P；展開、去掉重複之後 1～500 部"`
		From          string   `json:"from,omitempty" doc:"rerun、change_profile：從哪個步驟重跑"`
		Profile       string   `json:"profile,omitempty" doc:"change_profile：改用的處理方案"`
		DeleteOutputs bool     `json:"delete_outputs,omitempty" doc:"delete：同時刪除沒被改過的輸出檔（DEL-R9）"`
		DryRun        bool     `json:"dry_run,omitempty" doc:"只回報影響範圍，不改變任何資料（LIB-R14）"`
	}
}

// BatchItem 是批次中一部影音的結果，依請求的順序（去掉重複之後）。
type BatchItem struct {
	ID string `json:"id,omitempty" doc:"影音 ID；找不到時省略"`
	// Ref 是影音的代號；找不到時是請求中的寫法。
	Ref string `json:"ref" doc:"影音的代號；找不到時是請求中 ids 的寫法"`
	// Title 只在 dry_run 出現。
	Title   *string `json:"title,omitempty" doc:"影音標題（只有 dry_run）"`
	OK      bool    `json:"ok"`
	Code    string  `json:"code,omitempty" doc:"失敗時的錯誤碼，和單一影音的 API 相同（not_found、in_trash 等）"`
	Message string  `json:"message,omitempty"`
	// BundleURL 是 export_bundle 實際執行時這部影音的 bundle 下載路徑（LIB 待決問題 L-M11）。
	BundleURL string `json:"bundle_url,omitempty" doc:"export_bundle：這部影音的 bundle 下載路徑（GET /api/items/{id}/bundle）"`
}

// BatchBudget 是 dry_run 的預算檢查（LIB-R14）。
type BatchBudget struct {
	RemainingUSD *float64 `json:"remaining_usd" nullable:"true" doc:"COST-R4 的 remaining_usd；沒有月預算時是 null"`
	Exceeds      bool     `json:"exceeds" doc:"estimated_usd 是否大於 remaining_usd"`
}

// BatchDeletion 是 delete 的 dry_run 中各影音 DEL-R3 影響範圍的合計（LIB-R18）。
type BatchDeletion struct {
	Artifacts       int     `json:"artifacts"`
	Bytes           int64   `json:"bytes"`
	SpentUSD        float64 `json:"spent_usd"`
	Outputs         int     `json:"outputs" doc:"輸出檔總數"`
	ModifiedOutputs int     `json:"modified_outputs" doc:"其中被改過（不會被 delete_outputs 刪除）的數量"`
}

// BatchResult 是批次的回應。dry_run 時有 estimated_usd、unknown_estimates、budget（delete 另有 deletion）；
// 實際執行時有 succeeded、failed。
type BatchResult struct {
	Action           string         `json:"action"`
	DryRun           bool           `json:"dry_run"`
	Count            int            `json:"count" doc:"dry_run：ok 的影音數；實際執行：去掉重複之後的影音數"`
	Succeeded        *int           `json:"succeeded,omitempty"`
	Failed           *int           `json:"failed,omitempty"`
	EstimatedUSD     *float64       `json:"estimated_usd,omitempty"`
	UnknownEstimates *int           `json:"unknown_estimates,omitempty" doc:"無法估算、沒有加總的付費任務數"`
	Budget           *BatchBudget   `json:"budget,omitempty"`
	Deletion         *BatchDeletion `json:"deletion,omitempty"`
	Items            []BatchItem    `json:"items"`
}

type batchOutput struct{ Body BatchResult }

func (rt *routes) batchItems(ctx context.Context, in *batchInput) (*batchOutput, error) {
	if _, ok := principalFrom(ctx); !ok {
		return nil, newError(http.StatusUnauthorized, CodeUnauthorized, "尚未登入或登入已失效。")
	}
	b := &in.Body
	scope, ok := batchScopes[b.Action]
	if !ok {
		return nil, batchInvalid("action", "不支援的批次動作 "+clip(b.Action)+"。")
	}
	if err := requireScope(ctx, scope); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	if len(b.IDs) == 0 {
		return nil, batchInvalid("ids", "至少要選一部影音。")
	}
	if b.Action == BatchRerun || b.Action == BatchChangeProfile {
		if !pipeline.ValidStage(b.From) {
			return nil, batchInvalid("from", "from 必須是 pipeline 的步驟。")
		}
	}
	if b.Action == BatchChangeProfile {
		if b.Profile == "" {
			return nil, batchInvalid("profile", "找不到處理方案 。")
		}
		if _, err := rt.d.Pipeline.Profile(ctx, b.Profile); errors.Is(err, pipeline.ErrProfileNotFound) {
			return nil, batchInvalid("profile", "找不到處理方案 "+clip(b.Profile)+"。")
		} else if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
	}
	targets, err := rt.d.Pipeline.ResolveBatch(ctx, b.IDs)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	if len(targets) > pipeline.MaxBatchItems {
		return nil, batchInvalid("ids", "一次最多選 500 部影音。")
	}
	if b.DryRun {
		res, err := rt.batchDryRun(ctx, b.Action, b.From, b.Profile, targets)
		if err != nil {
			return nil, err
		}
		return &batchOutput{Body: res}, nil
	}
	return &batchOutput{Body: rt.batchRun(ctx, b.Action, b.From, b.Profile, b.DeleteOutputs, targets)}, nil
}

func batchInvalid(path, msg string) *Error {
	e := newError(http.StatusUnprocessableEntity, CodeInvalidInput, msg)
	e.Errors = []validate.FieldError{{Path: path, Message: msg}}
	return e
}

// clip 截短使用者給的值再放進訊息：最多 64 個字元。
func clip(s string) string {
	if utf8.RuneCountInString(s) <= 64 {
		return s
	}
	return string([]rune(s)[:64]) + "…"
}

// itemOf 是一部影音結果的 id 與 ref：找不到的影音只有請求中的寫法。
func itemOf(t pipeline.BatchTarget) BatchItem {
	if t.Item.ID == "" {
		return BatchItem{Ref: t.Raw}
	}
	return BatchItem{ID: t.Item.ID, Ref: source.Ref(t.Item.Platform, t.Item.ExternalID, t.Item.Part)}
}

// fail 把單一影音的錯誤寫成結果的 code 與 message，和單一影音的 API 相同（mapErr）。
func (rt *routes) fail(ctx context.Context, it *BatchItem, err error) {
	e := asError(rt.mapErr(ctx, err))
	it.OK, it.Code, it.Message = false, e.Code, e.Message
}

// batchDryRun 只回報影響範圍：不改變任何資料、不呼叫外部服務、不寫事件（LIB-R14）。
func (rt *routes) batchDryRun(ctx context.Context, action, from, profile string, targets []pipeline.BatchTarget) (BatchResult, error) {
	res := BatchResult{Action: action, DryRun: true, Items: make([]BatchItem, 0, len(targets))}
	var total cost.Micro
	unknown := 0
	var del *BatchDeletion
	var spent cost.Micro
	if action == BatchDelete {
		del = &BatchDeletion{}
	}
	for _, t := range targets {
		it := itemOf(t)
		title := t.Item.Title
		it.Title, it.OK = &title, true
		switch {
		case t.Err != nil:
			rt.fail(ctx, &it, t.Err)
		case action == BatchRerun || action == BatchChangeProfile:
			p := ""
			if action == BatchChangeProfile {
				p = profile
			}
			est, err := rt.d.Pipeline.EstimateRerun(ctx, t.Item.ID, from, p)
			if err != nil {
				if ctx.Err() != nil {
					return BatchResult{}, ctx.Err()
				}
				rt.fail(ctx, &it, err)
				break
			}
			total += est.USD
			unknown += est.Unknown
		case action == BatchDelete:
			pv, err := rt.d.Pipeline.DeletionPreview(ctx, t.Item.ID)
			if err != nil {
				if ctx.Err() != nil {
					return BatchResult{}, ctx.Err()
				}
				rt.fail(ctx, &it, err)
				break
			}
			del.Artifacts += pv.Artifacts
			del.Bytes += pv.Bytes
			if m, ok := cost.FromUSD(pv.SpentUSD); ok {
				spent += m
			}
			for _, o := range pv.Outputs {
				del.Outputs++
				if o.Modified {
					del.ModifiedOutputs++
				}
			}
		}
		if it.OK {
			res.Count++
		}
		res.Items = append(res.Items, it)
	}
	usd := total.USD()
	res.EstimatedUSD, res.UnknownEstimates = &usd, &unknown
	if del != nil {
		del.SpentUSD = spent.USD()
		res.Deletion = del
	}
	budget, err := rt.batchBudget(ctx, total)
	if err != nil {
		return BatchResult{}, err
	}
	res.Budget = budget
	return res, nil
}

// batchBudget 是 dry_run 的預算檢查：remaining_usd 是月預算減去本月的 actual 與 reserved（最少 0，COST-R4），
// exceeds 是估算大於它（LIB-R14）；沒有月預算（或沒有成本服務）時 remaining_usd 是 null、exceeds 是 false。
func (rt *routes) batchBudget(ctx context.Context, est cost.Micro) (*BatchBudget, error) {
	out := &BatchBudget{}
	if rt.d.Cost == nil || rt.d.Store == nil {
		return out, nil
	}
	r := rt.d.Store.Read()
	b, err := rt.d.Cost.Budget(ctx, r)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	if b.Monthly == nil {
		return out, nil
	}
	m, err := rt.d.Cost.Month(ctx, r, b, rt.d.Cost.Now())
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	left := max(*b.Monthly-m.Total(), 0)
	v := left.USD()
	out.RemainingUSD, out.Exceeds = &v, est > left
	return out, nil
}

// batchRun 逐部執行；每部是獨立的交易，一部失敗不影響其他部，也不回復已經成功的（LIB-R15）。完成後寫一筆 items.batch。
func (rt *routes) batchRun(ctx context.Context, action, from, profile string, deleteOutputs bool, targets []pipeline.BatchTarget) BatchResult {
	res := BatchResult{Action: action, Count: len(targets), Items: make([]BatchItem, 0, len(targets))}
	succeeded, failed := 0, 0
	for _, t := range targets {
		it := itemOf(t)
		it.OK = true
		var err error
		switch {
		case ctx.Err() != nil:
			// client 中斷：剩下的影音不再處理，也不經 mapErr 逐筆記錯誤 log（L-M15）。
			it.OK, it.Code, it.Message = false, CodeBatchCancelled, "請求已中斷，這部影音沒有處理。"
			failed++
			res.Items = append(res.Items, it)
			continue
		case t.Err != nil:
			err = t.Err
		case action == BatchRerun:
			_, err = rt.d.Pipeline.Rerun(ctx, t.Item.ID, pipeline.RerunRequest{From: from})
		case action == BatchChangeProfile:
			// Rerun 帶 profile 會把影音的處理方案改成它，之後不帶 profile 的重跑也用它（D7、LIB-R17）。
			_, err = rt.d.Pipeline.Rerun(ctx, t.Item.ID, pipeline.RerunRequest{From: from, Profile: profile})
		case action == BatchDelete:
			_, err = rt.trashOne(ctx, t.Item.ID, deleteOutputs)
		case action == BatchMarkRead:
			err = rt.d.Pipeline.SetReadState(ctx, t.Item.ID, store.ReadRead)
		case action == BatchMarkUnread:
			err = rt.d.Pipeline.SetReadState(ctx, t.Item.ID, store.ReadUnread)
		case action == BatchExportBundle:
			// bundle v0 一份只有一部影音（BND 不在範圍內：一次匯出多部的單一 bundle）：逐部回傳下載路徑（L-M11）。
			it.BundleURL = "/api/items/" + url.PathEscape(t.Item.ID) + "/bundle"
		}
		if err != nil {
			rt.fail(ctx, &it, err)
			it.BundleURL = ""
			failed++
		} else {
			succeeded++
		}
		res.Items = append(res.Items, it)
	}
	res.Succeeded, res.Failed = &succeeded, &failed
	rt.emitBatch(ctx, action, len(targets), succeeded, failed)
	return res
}

// emitBatch 寫入 items.batch 事件（LIB-R15）。各影音已經完成，事件寫不進去只記 log，不讓整個請求失敗。
func (rt *routes) emitBatch(ctx context.Context, action string, count, succeeded, failed int) {
	if rt.d.Store == nil {
		return
	}
	wctx := context.WithoutCancel(ctx)
	err := rt.d.Store.Tx(wctx, func(r *store.Repo) error {
		return r.Emit(wctx, events.Event{Type: events.TypeItemsBatch, Action: action, Count: &count, Succeeded: &succeeded, Failed: &failed})
	})
	if err != nil {
		rt.d.Log.WarnContext(ctx, "寫入 items.batch 事件失敗", "error", err.Error())
	}
}
