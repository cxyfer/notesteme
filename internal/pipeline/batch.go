package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// Batch API（BATCH）：訂閱與回補的 LLM chunk 改用 provider 的 batch，等結果的期間任務是 waiting、不占用並行上限。
//
//   - 送出（submitBatch）：worker 領取到符合 BATCH-R1 的 chunk 任務時，收集同一輪中其他可以領取、provider 與 model
//     相同的 chunk 任務（最多 100 個），在一個寫入交易中以 batch 單價預留、把任務改成 waiting、寫入 batches，
//     交易之後才送出 HTTP（BATCH-R2、R7）。送出失敗時這些任務改走一般 API（submit_failed）。
//   - 輪詢（PollBatch，排程工作 batch.poll）：每 5 分鐘查詢一次；結束後依 custom_id（任務 ID）把結果交給對應的 chunk，
//     驗證與一般 API 相同（BATCH-R4）；逾時就取消、改走一般 API（BATCH-R5）；已被移除的任務的結果記為 discarded（BATCH-R9）。
//
// 數值先寫死（BATCH 待決問題 H2）。
const (
	batchPollInterval = 5 * time.Minute
	batchDeadline     = 24 * time.Hour
	// batchCandidateScan 是找同一輪其他任務時最多讀取的候選數（過濾之後最多 llm.MaxBatchRequests 個）。
	batchCandidateScan = 4 * llm.MaxBatchRequests
)

// 改走一般 API 的原因（BATCH-R8 的 fallback_reason）。
const (
	BatchFallbackTimeout       = "timeout"
	BatchFallbackRequestFailed = "request_failed"
	BatchFallbackBatchFailed   = "batch_failed"
	BatchFallbackInvalidOutput = "invalid_output"
	BatchFallbackSubmitFailed  = "submit_failed"
)

// 來源紀錄的 chunks.<n>.mode（BATCH-R8）。
const (
	batchModeBatch = "batch"
	batchModeAPI   = "api"
)

// noticeForeignResults 是 results_url 在其他主機時附在影音訊息上的提醒（BATCH-R11）。
const noticeForeignResults = "Batch API 回傳了其他主機的結果網址，已改用一般 API。"

// batchFallback 是 chunk 任務改走一般 API 時 payload 的 batch 欄位。
type batchFallback struct {
	Reason      string     `json:"reason"`
	BatchID     string     `json:"batch_id,omitempty"` // provider 的 batch ID
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	// Retry 是 batch 的結果沒有通過驗證時的錯誤說明（invalid_output）：一般 API 的第一次請求就附上（LLM-R5）。
	Retry  string `json:"retry,omitempty"`
	Notice string `json:"notice,omitempty"`
	// Prior 是 translate 已經付費的 batch 結果：句群的切分標記不符（TRN-R4）時帶到一般 API，只補送逐 cue 的請求。
	// 這時 Reason 是空字串，來源紀錄記 mode: batch（chunk 的譯文來自 batch）。
	Prior *trnPrior `json:"prior,omitempty"`
}

// batchRecord 是 llm_chunks.batch_json：來源紀錄 chunks.<n> 的 batch 欄位（BATCH-R8）。
type batchRecord struct {
	Mode           string     `json:"mode"`
	BatchID        string     `json:"batch_id,omitempty"`
	SubmittedAt    *time.Time `json:"submitted_at,omitempty"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	FallbackReason string     `json:"fallback_reason,omitempty"`
	// Notice 只給步驟的訊息用，不寫進來源紀錄。
	Notice string `json:"notice,omitempty"`
}

func (f *batchFallback) record() json.RawMessage {
	rec := batchRecord{
		Mode: batchModeAPI, BatchID: f.BatchID, SubmittedAt: f.SubmittedAt, EndedAt: f.EndedAt,
		FallbackReason: f.Reason, Notice: f.Notice,
	}
	if f.Prior != nil {
		rec.Mode, rec.FallbackReason = batchModeBatch, ""
	}
	b, _ := json.Marshal(rec)
	return b
}

// batchWork 是一個 chunk 任務送進 batch、以及驗證 batch 結果需要的內容。opts 與一般 API 的第一次請求是同一份
// （BATCH-R2：請求的內容完全相同）。同一個 batch 可以混有不同步驟的任務，所以依每個任務自己的步驟組出（prepareBatchWork）。
type batchWork struct {
	provider, model string
	// opts 是 RunChunk 的輸入（Spec、Model、Fixed、Input、MaxRetries；不含 Client 與 hook）。
	opts llm.RunOptions
	// trn 是 translate 的 chunk：結果要分回各 cue（TRN-R3、R4、R7）；其他步驟是 nil。
	trn *trnChunkWork
}

// prepareBatchWork 依 stage run 的步驟組出 batchWork；chunk 已經有結果時 done 為 true。
func (p *Pipeline) prepareBatchWork(ctx context.Context, r *store.Repo, t store.Task, run store.StageRun, it store.Item, eff Effective) (*batchWork, bool, error) {
	if run.Stage == StageTranslate {
		w, done, err := p.trnPrepareChunk(ctx, r, t, run, it, eff)
		if err != nil || done {
			return nil, done, err
		}
		return w.batchWork(), false, nil
	}
	w, done, err := p.prepareChunk(ctx, r, t, run, it, eff)
	if err != nil || done {
		return nil, done, err
	}
	return w.batchWork(), false, nil
}

func (w *chunkWork) batchWork() *batchWork {
	return &batchWork{provider: w.job.cfg.Provider, model: w.job.cfg.Model, opts: w.opts}
}

// BatchAPI 是 provider 的 Batch API（*llm.BatchClient 實作它）；測試可以替換（SetBatchAPI）。
type BatchAPI interface {
	Submit(ctx context.Context, reqs []llm.BatchRequest) (string, error)
	Status(ctx context.Context, id string) (llm.BatchStatus, error)
	Cancel(ctx context.Context, id string) (llm.BatchStatus, error)
	Results(ctx context.Context, st llm.BatchStatus, fn func(llm.BatchResult) error) error
	DecodeBatchBody(raw []byte, spec *llm.Spec) (llm.Response, error)
}

// errNoBatchClient 是 provider 的 client 不是 HTTP client（單元測試的替身）：不能用 batch，照一般 API 執行。
var errNoBatchClient = errors.New("pipeline: 這個 provider 沒有 Batch API 的 client")

// SetBatchAPI 讓測試替換 Batch API 的 client；f 為 nil 時用 provider 的 HTTP client。
func (p *Pipeline) SetBatchAPI(f func(ctx context.Context, provider string) (BatchAPI, error)) {
	p.batchAPIFor = f
}

func (p *Pipeline) batchAPI(ctx context.Context, provider string) (BatchAPI, error) {
	if p.batchAPIFor != nil {
		return p.batchAPIFor(ctx, provider)
	}
	if p.llm == nil {
		return nil, errNoBatchClient
	}
	c, err := p.llm.Client(ctx, provider)
	if err != nil {
		return nil, err
	}
	bc := llm.NewBatchClient(c)
	if bc == nil {
		return nil, errNoBatchClient
	}
	return bc, nil
}

// stageBatch 回傳 stages.<stage>.batch（BATCH-R1）；沒有設定時是 off。
func stageBatch(eff Effective, stage string) string {
	all, err := eff.stages()
	if err != nil || all == nil {
		return BatchOff
	}
	entry, _ := all[stage].(map[string]any)
	if v, _ := entry["batch"].(string); v == BatchAuto {
		return BatchAuto
	}
	return BatchOff
}

// batchEligible 判斷 chunk 任務是否改用 Batch API（BATCH-R1）：處理方案設定 auto、優先序是 subscription 或 backfill、
// executor 是 api、provider 是內建 base_url 的 anthropic 或 openai。已經從 batch 改走一般 API 的任務不再送進 batch。
// LLM 引擎的步驟（segment、correct、chapters、note、summary）由 llmChunk 呼叫，translate 由 trnChunk 呼叫。
func (p *Pipeline) batchEligible(t store.Task, run store.StageRun, cfg StageConfig, eff Effective, pl llmPayload) bool {
	if pl.Batch != nil || p.llm == nil || t.ChunkIndex == nil || t.Executor != queue.ExecutorBuiltin {
		return false
	}
	if t.Priority > queue.PrioritySubscription || run.Executor != ExecutorAPI {
		return false
	}
	if !slices.Contains(batchStages, run.Stage) || stageBatch(eff, run.Stage) != BatchAuto {
		return false
	}
	prov, ok := p.llm.Provider(cfg.Provider)
	return ok && llm.BatchSupported(prov)
}

// batchWaitingState 是等待 batch 的步驟狀態（BATCH-R3）。
func batchWaitingState(deadline time.Time) store.StageState {
	return store.StageState{
		Status:  store.StatusWaiting,
		Message: fmt.Sprintf("已送出 Batch API，等待結果，最晚 %s UTC。", deadline.UTC().Format("2006-01-02 15:04")),
	}
}

// batchItem 是送進 batch 的一個 chunk 任務。
type batchItem struct {
	task store.Task
	run  store.StageRun
	item store.Item
	req  llm.Request
}

// call 是這個請求的計費資料；token 數與一般 API 的預留用同樣的估算（COST-R5），Reserve 再乘上 batch 折扣（BATCH-R7）。
// batch 只用在內建 base_url 的 provider（BATCH-R1），所以 CustomBase 一律是 false。
func (b batchItem) call(provider, model string) cost.Call {
	in, out := requestTokens(b.req)
	return cost.Call{
		ItemID: b.item.ID, ItemTitle: b.item.Title, StageRunID: b.run.ID, TaskID: b.task.ID, Stage: b.run.Stage,
		Provider: provider, Model: model, Batch: true, InputTokens: in, Output: out,
	}
}

// errBatchSkip 讓送出的交易回復，任務照一般 API 執行（步驟已不是最新等少見的情況）。
var errBatchSkip = errors.New("pipeline: 這次不送 batch")

// submitBatch 把已領取的 chunk 任務 t 與同一輪其他符合的任務送成一個 batch（BATCH-R2）。
// handled 為 false 時呼叫端照一般 API 執行；t 自己超過預算時回傳 budget_blocked（呼叫端照 COST-R8 處理）。
func (p *Pipeline) submitBatch(ctx context.Context, t store.Task, run store.StageRun, it store.Item, w *batchWork) (bool, error) {
	provider, model := w.provider, w.model
	api, err := p.batchAPI(ctx, provider)
	if err != nil {
		// 沒有 key 等問題交給一般 API 的路徑回報（LLM-R12）。
		return false, nil
	}
	own, err := llm.ChunkRequest(w.opts)
	if err != nil {
		return false, err
	}
	items := []batchItem{{task: t, run: run, item: it, req: own}}
	items = append(items, p.batchCandidates(ctx, t, provider, model)...)

	id := store.NewID()
	now := p.clock.Now()
	deadline := now.Add(batchDeadline)
	var entries []store.BatchEntry
	var reqs []llm.BatchRequest
	err = p.st.Tx(ctx, func(tx *store.Repo) error {
		entries, reqs = nil, nil
		runs, err := tx.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		if cur := latestRuns(runs)[run.Stage]; cur.ID != run.ID || stageStopped(cur.Status) {
			return errBatchSkip
		}
		waiting := map[string]bool{}
		for i, b := range items {
			if i == 0 {
				if err := tx.ParkLeasedTask(ctx, t.ID, t.LeaseToken, id); err != nil {
					return err
				}
			} else if ok, err := tx.ParkPendingTask(ctx, b.task.ID, id); err != nil {
				return err
			} else if !ok {
				continue // 同一輪已被其他 worker 領取
			}
			var resID string
			if p.cost != nil {
				// BATCH-R7：以 batch 單價預留，和一般任務一樣在同一個交易中檢查預算。
				res, err := p.cost.Reserve(ctx, tx, b.call(provider, model))
				if cost.IsBudgetBlocked(err) && i > 0 {
					// 超過預算的任務不放進 batch；之後被領取時照 COST-R8 blocked。
					if _, err := tx.UnparkTask(ctx, b.task.ID, id, b.task.Payload); err != nil {
						return err
					}
					continue
				}
				if err != nil {
					return err
				}
				resID = res.ID
			}
			entries = append(entries, store.BatchEntry{Task: b.task.ID, Run: b.run.ID, Chunk: *b.task.ChunkIndex, Reservation: resID})
			reqs = append(reqs, llm.BatchRequest{CustomID: b.task.ID, Request: b.req})
			if !waiting[b.run.ID] {
				waiting[b.run.ID] = true
				if err := tx.SetStageState(ctx, b.run.ID, batchWaitingState(deadline)); err != nil {
					return err
				}
			}
		}
		if err := tx.InsertBatch(ctx, store.Batch{
			ID: id, Provider: provider, Model: model, SubmittedAt: now, DeadlineAt: deadline, Entries: entries,
		}); err != nil {
			return err
		}
		// 送出的結果沒有記下來（程序中斷）時，第一次輪詢把這些任務改走一般 API。
		return tx.ScheduleJob(ctx, store.BatchPollKind, id, now.Add(batchPollInterval))
	})
	switch {
	case errors.Is(err, errBatchSkip):
		return false, nil
	case errors.Is(err, store.ErrLeaseLost):
		// 租約已失效：任務已被取消或放回，什麼都不做。
		return true, nil
	case err != nil:
		return true, err
	}

	// 送出在交易之外；寫回不受 worker 停止影響。
	wctx := context.WithoutCancel(ctx)
	external, serr := api.Submit(ctx, reqs)
	if serr == nil {
		err = p.st.Tx(wctx, func(tx *store.Repo) error { return tx.SetBatchExternalID(wctx, id, external) })
	} else {
		// BATCH-R2：送出本身失敗（429、5xx、連線錯誤）時改走一般 API。
		p.log.WarnContext(ctx, "送出 Batch API 失敗，改走一般 API", "batch", id, "provider", provider, "requests", len(reqs), "error", serr)
		err = p.st.Tx(wctx, func(tx *store.Repo) error {
			b, err := tx.Batch(wctx, id)
			if err != nil {
				return err
			}
			if err := p.fallbackLive(wctx, tx, b, batchFallback{Reason: BatchFallbackSubmitFailed}); err != nil {
				return err
			}
			if _, err := tx.DeleteScheduledJob(wctx, store.BatchPollKind, id); err != nil {
				return err
			}
			end := p.clock.Now()
			return tx.SetBatchState(wctx, id, store.BatchFailed, &end, "")
		})
	}
	if err != nil {
		return true, err
	}
	p.log.InfoContext(ctx, "已送出 Batch API", "batch", id, "provider", provider, "requests", len(reqs), "submitted", serr == nil)
	p.wake()
	return true, nil
}

// batchCandidates 找出同一輪中其他可以加入 batch 的任務（BATCH-R2）：provider、model 相同、也符合 BATCH-R1。
// 讀不到或組不出請求的任務略過，留給 worker 照常處理。
func (p *Pipeline) batchCandidates(ctx context.Context, t store.Task, provider, model string) []batchItem {
	r := p.st.Read()
	cands, err := r.BatchCandidateTasks(ctx, t.ID, queue.PrioritySubscription, batchCandidateScan)
	if err != nil {
		p.log.WarnContext(ctx, "讀取 batch 的候選任務失敗", "error", err)
		return nil
	}
	var out []batchItem
	for _, c := range cands {
		if len(out)+1 >= llm.MaxBatchRequests {
			break
		}
		b, ok := p.batchCandidate(ctx, r, c, provider, model)
		if ok {
			out = append(out, b)
		}
	}
	return out
}

func (p *Pipeline) batchCandidate(ctx context.Context, r *store.Repo, c store.Task, provider, model string) (batchItem, bool) {
	run, err := r.StageRun(ctx, c.StageRunID)
	if err != nil {
		return batchItem{}, false
	}
	it, err := r.ItemByID(ctx, run.ItemID)
	if err != nil || it.DeletedAt != nil {
		return batchItem{}, false
	}
	snap, err := r.Snapshot(ctx, run.ConfigSnapshotID)
	if err != nil {
		return batchItem{}, false
	}
	eff, err := ParseEffective(snap)
	if err != nil {
		return batchItem{}, false
	}
	cfg, err := eff.Stage(run.Stage)
	if err != nil || cfg.Provider != provider || cfg.Model != model {
		return batchItem{}, false
	}
	var pl llmPayload
	if len(c.Payload) > 0 && json.Unmarshal(c.Payload, &pl) != nil {
		return batchItem{}, false
	}
	if pl.Phase != llmPhaseChunk || !p.batchEligible(c, run, cfg, eff, pl) {
		return batchItem{}, false
	}
	w, done, err := p.prepareBatchWork(ctx, r, c, run, it, eff)
	if err != nil || done {
		return batchItem{}, false
	}
	req, err := llm.ChunkRequest(w.opts)
	if err != nil {
		return batchItem{}, false
	}
	return batchItem{task: c, run: run, item: it, req: req}, true
}

// unpark 讓等待 batch 的任務改走一般 API（BATCH-R4、R5）：任務回到 pending，payload 記下原因；
// release 為 true 時刪除 batch 的預留（BATCH-R7：一般 API 會以一般單價重新預留）。任務已經不在 batch 中時回傳 false。
func (p *Pipeline) unpark(ctx context.Context, tx *store.Repo, b store.Batch, e store.BatchEntry, fb batchFallback, release bool) (bool, error) {
	task, err := tx.Task(ctx, e.Task)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if task.State != store.TaskWaiting || task.BatchID != b.ID {
		return false, nil
	}
	var pl llmPayload
	if len(task.Payload) > 0 {
		_ = json.Unmarshal(task.Payload, &pl) // 格式不對時重新組出
	}
	if pl.ItemID == "" {
		if run, err := tx.StageRun(ctx, e.Run); err == nil {
			pl.ItemID = run.ItemID
		}
	}
	pl.Phase, pl.Batch = llmPhaseChunk, &fb
	payload, err := json.Marshal(pl)
	if err != nil {
		return false, err
	}
	ok, err := tx.UnparkTask(ctx, task.ID, b.ID, payload)
	if err != nil || !ok {
		return ok, err
	}
	if release && e.Reservation != "" && p.cost != nil {
		if err := p.cost.Release(ctx, tx, cost.Reservation{ID: e.Reservation}); err != nil {
			return false, err
		}
	}
	run, err := tx.StageRun(ctx, e.Run)
	if err != nil {
		return false, err
	}
	if run.Status == store.StatusWaiting {
		if err := tx.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending, Message: fb.Notice}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// fallbackLive 讓 batch 中所有還在等待的任務改走一般 API，並刪除它們的預留。
func (p *Pipeline) fallbackLive(ctx context.Context, tx *store.Repo, b store.Batch, fb batchFallback) error {
	for _, e := range b.Entries {
		if _, err := p.unpark(ctx, tx, b, e, fb, true); err != nil {
			return err
		}
	}
	return nil
}

// liveEntries 回傳 batch 中還在等待結果的請求（沒有被重跑、取消或刪除移除的，BATCH-R9）。
func liveEntries(ctx context.Context, r *store.Repo, b store.Batch) ([]store.BatchEntry, error) {
	var out []store.BatchEntry
	for _, e := range b.Entries {
		t, err := r.Task(ctx, e.Task)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if t.State == store.TaskWaiting && t.BatchID == b.ID {
			out = append(out, e)
		}
	}
	return out, nil
}

// PollBatch 是排程工作 batch.poll（BATCH-R4、R5、R9）。回傳下一次輪詢的時間；done 為 true 時 batch 已經結束，
// 排程工作結束。回傳錯誤時排程器依退避重試（例如 provider 暫時無法連線）。
func (p *Pipeline) PollBatch(ctx context.Context, id string, now time.Time) (next time.Time, done bool, err error) {
	r := p.st.Read()
	b, err := r.Batch(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return time.Time{}, true, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if !b.Open() {
		return time.Time{}, true, nil
	}
	wctx := context.WithoutCancel(ctx)
	if b.ExternalID == "" {
		// 送出之後沒有記下 provider 的 batch ID（程序中斷）：視同送出失敗。
		err := p.st.Tx(wctx, func(tx *store.Repo) error {
			if err := p.fallbackLive(wctx, tx, b, batchFallback{Reason: BatchFallbackSubmitFailed}); err != nil {
				return err
			}
			return tx.SetBatchState(wctx, b.ID, store.BatchFailed, &now, "")
		})
		p.wake()
		return time.Time{}, err == nil, err
	}
	api, err := p.batchAPI(ctx, b.Provider)
	if err != nil {
		return p.abandonBatch(ctx, b, now, err)
	}
	st, err := api.Status(ctx, b.ExternalID)
	if err != nil {
		return p.abandonBatch(ctx, b, now, err)
	}
	if st.State == llm.BatchInProgress {
		live, err := liveEntries(ctx, r, b)
		if err != nil {
			return time.Time{}, false, err
		}
		timedOut := !now.Before(b.DeadlineAt)
		if b.State != store.BatchSubmitted || (!timedOut && len(live) > 0) {
			return now.Add(batchPollInterval), false, nil
		}
		// BATCH-R5：逾時就取消，還沒有結果的任務立刻改走一般 API；BATCH-R9：所有任務都被移除時取消這個 batch。
		cst, cerr := api.Cancel(ctx, b.ExternalID)
		if cerr != nil {
			p.log.WarnContext(ctx, "取消 batch 失敗", "batch", b.ID, "provider", b.Provider, "error", cerr)
		}
		err = p.st.Tx(wctx, func(tx *store.Repo) error {
			if timedOut {
				if err := p.fallbackLive(wctx, tx, b, batchFallback{
					Reason: BatchFallbackTimeout, BatchID: b.ExternalID, SubmittedAt: &b.SubmittedAt,
				}); err != nil {
					return err
				}
			}
			if cerr != nil {
				return nil // 下一次輪詢再取消
			}
			return tx.SetBatchState(wctx, b.ID, store.BatchCancelling, nil, "")
		})
		if err != nil {
			return time.Time{}, false, err
		}
		p.wake()
		if cerr != nil || cst.State == llm.BatchInProgress {
			return now.Add(batchPollInterval), false, nil
		}
		b.State, st = store.BatchCancelling, cst
	}
	if err := p.finishBatch(ctx, api, b, st, now); err != nil {
		return p.abandonBatch(ctx, b, now, err)
	}
	return time.Time{}, true, nil
}

// abandonBatch 處理輪詢的錯誤：還沒逾時就回傳錯誤（排程器依退避重試）；已經逾時（查詢或取回結果一直失敗）時
// 不再等待，還沒有結果的任務改走一般 API（batch_failed），batch 標為 failed、停止輪詢，否則任務會永遠停在 waiting。
// provider 端之後才產生的結果無法取回，也不會記帳。
func (p *Pipeline) abandonBatch(ctx context.Context, b store.Batch, now time.Time, cause error) (time.Time, bool, error) {
	if now.Before(b.DeadlineAt) {
		return time.Time{}, false, cause
	}
	p.log.WarnContext(ctx, "batch 逾時且無法取得結果，改走一般 API", "batch", b.ID, "provider", b.Provider, "error", cause)
	wctx := context.WithoutCancel(ctx)
	err := p.st.Tx(wctx, func(tx *store.Repo) error {
		if err := p.fallbackLive(wctx, tx, b, batchFallback{
			Reason: BatchFallbackBatchFailed, BatchID: b.ExternalID, SubmittedAt: &b.SubmittedAt,
		}); err != nil {
			return err
		}
		// 已經被移除的任務的預留也一併刪除：這個 batch 不會再結算。
		for _, e := range b.Entries {
			if e.Reservation != "" && p.cost != nil {
				if err := p.cost.Release(wctx, tx, cost.Reservation{ID: e.Reservation}); err != nil {
					return err
				}
			}
		}
		return tx.SetBatchState(wctx, b.ID, store.BatchFailed, &now, "")
	})
	if err != nil {
		return time.Time{}, false, err
	}
	p.wake()
	return time.Time{}, true, nil
}

// batchVerdict 是交易之前算好的一個結果：解碼、驗證（BATCH-R4）。
type batchVerdict struct {
	res     llm.BatchResult
	resp    llm.Response
	decoded bool
	out     json.RawMessage
	verr    *llm.ValidationError
	retries int // 這個步驟的 max_validation_retries
	// trnRes、trnPrior 是 translate 的結果分回各 cue 之後（TRN-R3、R7）：可以直接寫回的 chunk 結果，
	// 或句群的切分標記不符、要在一般 API 補送逐 cue 的請求（TRN-R4）。只在通過驗證或沒有驗證重試額度時設定。
	trnRes   *store.ChunkResult
	trnPrior *trnPrior
}

// finishBatch 取回已結束的 batch 的結果並交給對應的任務（BATCH-R4、R9、R11）。
func (p *Pipeline) finishBatch(ctx context.Context, api BatchAPI, b store.Batch, st llm.BatchStatus, now time.Time) error {
	entries := map[string]store.BatchEntry{}
	for _, e := range b.Entries {
		entries[e.Task] = e
	}
	results := map[string]llm.BatchResult{}
	notice := ""
	rerr := api.Results(ctx, st, func(res llm.BatchResult) error {
		if _, ok := entries[res.CustomID]; !ok {
			return nil // 不是這個 batch 的請求（不可信的輸入）
		}
		if _, dup := results[res.CustomID]; !dup {
			results[res.CustomID] = res
		}
		return nil
	})
	switch {
	case errors.Is(rerr, llm.ErrForeignResults):
		// BATCH-R11：不取用其他主機的結果；這個 batch 視為 batch_failed。
		p.log.WarnContext(ctx, "batch 的結果網址不在 provider 的主機上，改走一般 API", "batch", b.ID, "provider", b.Provider)
		results, notice = map[string]llm.BatchResult{}, noticeForeignResults
	case errors.Is(rerr, llm.ErrBatchResultsTooLarge):
		p.log.WarnContext(ctx, "batch 的結果超過大小上限，改走一般 API", "batch", b.ID, "provider", b.Provider)
		results = map[string]llm.BatchResult{}
	case rerr != nil:
		return rerr
	}
	ended := now
	if st.EndedAt != nil {
		ended = st.EndedAt.UTC()
	}
	// 解碼與驗證在交易之外（讀字幕檔）；交易中再確認任務仍在等待。
	verdicts := map[string]*batchVerdict{}
	for tid, res := range results {
		e := entries[tid]
		v := &batchVerdict{res: res}
		verdicts[tid] = v
		if res.Kind != llm.ResultSucceeded {
			continue
		}
		w, err := p.batchChunkWork(ctx, e)
		if err != nil {
			// 輸入已經不存在（例如影音被刪除）：只記帳。
			if resp, derr := api.DecodeBatchBody(res.Body, nil); derr == nil {
				v.resp = resp
			}
			continue
		}
		resp, derr := api.DecodeBatchBody(res.Body, w.opts.Spec)
		if derr != nil {
			continue
		}
		v.resp, v.decoded, v.retries = resp, true, w.opts.MaxRetries
		v.out, v.verr = llm.ValidateResponse(w.opts.Spec, w.opts.Input, resp)
		if w.trn != nil && (v.verr == nil || v.retries < 1) {
			if v.trnRes, v.trnPrior, err = w.trn.batchOutcome(v.out, v.verr, resp.Usage); err != nil {
				// 分不回各 cue（不應該發生）：當成不能解碼，改走一般 API。
				p.log.WarnContext(p.taskLogCtx(ctx, tid, e.Run), "batch 的譯文無法分回各句，改走一般 API", "batch", b.ID, "error", err)
				v.decoded, v.trnRes, v.trnPrior = false, nil, nil
			}
		}
	}
	final := store.BatchEnded
	switch {
	case b.State == store.BatchCancelling:
		final = store.BatchCancelled
	case st.State == llm.BatchTerminated:
		final = store.BatchFailed
	}
	wctx := context.WithoutCancel(ctx)
	var wake bool
	// Batch API 的結果是付費拿到的：durable commit（NFR-REL-3）。
	err := p.st.TxDurable(wctx, func(tx *store.Repo) error {
		wake = false
		for _, e := range b.Entries {
			w, err := p.applyBatchResult(wctx, tx, b, e, verdicts[e.Task], ended, notice)
			if err != nil {
				return err
			}
			wake = wake || w
		}
		return tx.SetBatchState(wctx, b.ID, final, &ended, notice)
	})
	if err != nil {
		return err
	}
	if wake {
		p.wake()
	}
	return nil
}

// batchChunkWork 重新組出一個請求的 chunk 與驗證用的輸入（與送出時相同，prepareLLM、trnJobFor 是確定性的）；
// 依這個請求自己的步驟組出（同一個 batch 可以混有不同步驟）。
func (p *Pipeline) batchChunkWork(ctx context.Context, e store.BatchEntry) (*batchWork, error) {
	r := p.st.Read()
	t, err := r.Task(ctx, e.Task)
	if err != nil {
		return nil, err
	}
	run, err := r.StageRun(ctx, e.Run)
	if err != nil {
		return nil, err
	}
	it, err := r.ItemByID(ctx, run.ItemID)
	if err != nil {
		return nil, err
	}
	snap, err := r.Snapshot(ctx, run.ConfigSnapshotID)
	if err != nil {
		return nil, err
	}
	eff, err := ParseEffective(snap)
	if err != nil {
		return nil, err
	}
	w, done, err := p.prepareBatchWork(ctx, r, t, run, it, eff)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, errors.New("chunk 已經有結果")
	}
	return w, nil
}

// applyBatchResult 在交易中處理一個請求的結果。回傳 true 代表建立了任務（要喚醒 worker）。
func (p *Pipeline) applyBatchResult(ctx context.Context, tx *store.Repo, b store.Batch, e store.BatchEntry, v *batchVerdict, ended time.Time, notice string) (bool, error) {
	task, err := tx.Task(ctx, e.Task)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	live := err == nil && task.State == store.TaskWaiting && task.BatchID == b.ID
	resv := cost.Reservation{ID: e.Reservation}
	run, rerr := tx.StageRun(ctx, e.Run)
	call := cost.Call{StageRunID: e.Run, TaskID: e.Task, Provider: b.Provider, Model: b.Model, Batch: true}
	if rerr == nil {
		call.ItemID, call.Stage = run.ItemID, run.Stage
		if it, err := tx.ItemByID(ctx, run.ItemID); err == nil {
			call.ItemTitle = it.Title
		}
	}
	usage := func(outcome string) cost.Usage {
		u := cost.Usage{Outcome: outcome}
		if v != nil {
			u.InputTokens, u.OutputTokens, u.ProviderUSD = v.resp.Usage.InputTokens, v.resp.Usage.OutputTokens, v.resp.CostUSD
		}
		return u
	}
	settle := func(outcome string) error {
		if p.cost == nil || call.ItemID == "" {
			return nil
		}
		return p.cost.SettleBatch(ctx, tx, resv, call, usage(outcome))
	}
	release := func() error {
		if p.cost == nil || e.Reservation == "" {
			return nil
		}
		return p.cost.Release(ctx, tx, resv)
	}
	fb := batchFallback{BatchID: b.ExternalID, SubmittedAt: &b.SubmittedAt, EndedAt: &ended, Notice: notice}

	if !live {
		// BATCH-R9：已經沒有對應任務的成功結果不寫入產出，但要記帳；canceled 等沒有費用的結果刪除預留。
		if v != nil && v.res.Kind == llm.ResultSucceeded && rerr == nil {
			return false, settle(cost.OutcomeDiscarded)
		}
		return false, release()
	}
	if v == nil {
		// 沒有結果：provider 標為 failed、expired、cancelled，或結果不能取用（BATCH-R4、R11）。
		fb.Reason = BatchFallbackBatchFailed
		_, err := p.unpark(ctx, tx, b, e, fb, true)
		return err == nil, err
	}
	switch v.res.Kind {
	case llm.ResultSucceeded:
		if !v.decoded {
			// 回應不是預期的格式：同個別請求失敗（provider 有回應，記 http_error）。
			if err := settle(cost.OutcomeHTTPError); err != nil {
				return false, err
			}
			fb.Reason = BatchFallbackRequestFailed
			_, err := p.unpark(ctx, tx, b, e, fb, false)
			return err == nil, err
		}
		outcome := cost.OutcomeOK
		if v.verr != nil {
			outcome = cost.OutcomeInvalid
		}
		if err := settle(outcome); err != nil {
			return false, err
		}
		switch {
		case v.trnPrior != nil:
			// translate 的句群切分標記不符（TRN-R4）：帶著這個結果到一般 API，只補送逐 cue 的請求。
			fb.Prior = v.trnPrior
			_, err := p.unpark(ctx, tx, b, e, fb, false)
			return err == nil, err
		case v.trnRes != nil:
			return p.finishBatchChunk(ctx, tx, b, e, run, task, v, *v.trnRes, ended)
		case v.verr == nil:
			return p.finishBatchChunk(ctx, tx, b, e, run, task, v, store.ChunkResult{Status: store.ChunkDone, Output: v.out}, ended)
		case v.retries < 1:
			// 沒有驗證重試的額度：照 LLM-R5 的步驟退回規則處理。
			res := store.ChunkResult{Status: store.ChunkFallback, Reason: v.verr.Reason}
			if len(v.verr.Detail) > 0 {
				res.Detail, _ = json.Marshal(v.verr.Detail)
			}
			return p.finishBatchChunk(ctx, tx, b, e, run, task, v, res, ended)
		default:
			fb.Reason, fb.Retry = BatchFallbackInvalidOutput, llm.RetryNote(v.verr)
			_, err := p.unpark(ctx, tx, b, e, fb, false)
			return err == nil, err
		}
	case llm.ResultErrored:
		if err := settle(cost.OutcomeHTTPError); err != nil {
			return false, err
		}
		fb.Reason = BatchFallbackRequestFailed
		_, err := p.unpark(ctx, tx, b, e, fb, false)
		return err == nil, err
	default:
		// expired、canceled：provider 不計費。
		fb.Reason = BatchFallbackRequestFailed
		_, err := p.unpark(ctx, tx, b, e, fb, true)
		return err == nil, err
	}
}

// finishBatchChunk 寫入經 batch 完成的 chunk（BATCH-R4、R8），之後與一般 API 相同（finishChunkTx）。
func (p *Pipeline) finishBatchChunk(ctx context.Context, tx *store.Repo, b store.Batch, e store.BatchEntry, run store.StageRun, task store.Task, v *batchVerdict, res store.ChunkResult, ended time.Time) (bool, error) {
	ok, err := tx.FinishBatchTask(ctx, task.ID, b.ID)
	if err != nil || !ok {
		return false, err
	}
	if res.Usage, err = json.Marshal(v.resp.Usage); err != nil {
		return false, err
	}
	rec, err := json.Marshal(batchRecord{Mode: batchModeBatch, BatchID: b.ExternalID, SubmittedAt: &b.SubmittedAt, EndedAt: &ended})
	if err != nil {
		return false, err
	}
	if err := tx.SetLLMChunkBatch(ctx, run.ID, e.Chunk, rec); err != nil {
		return false, err
	}
	return finishChunkTx(ctx, tx, run.ItemID, run, task.Priority, e.Chunk, res)
}

// BatchView 是 GET /api/items/:id 的步驟在等待 batch 時的 batch 欄位（BATCH 的介面）。
type BatchView struct {
	Provider    string    `json:"provider"`
	SubmittedAt time.Time `json:"submitted_at"`
	DeadlineAt  time.Time `json:"deadline_at"`
}

// waitingBatch 回傳 stage run 正在等待的 batch；沒有時是 nil。
func waitingBatch(ctx context.Context, r *store.Repo, run store.StageRun) (*BatchView, error) {
	if run.Status != store.StatusWaiting {
		return nil, nil
	}
	b, err := r.WaitingBatchOfRun(ctx, run.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &BatchView{Provider: b.Provider, SubmittedAt: b.SubmittedAt.UTC(), DeadlineAt: b.DeadlineAt.UTC()}, nil
}
