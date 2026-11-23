package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// AGT：Agent 透過 MCP 領取 LLM 的 chunk 任務（ADR-0004）。
//
// executor 是 agent 的 LLM 步驟與 api 走同一個引擎（llm.go）：內建 worker 執行 plan（切 chunk、查快取）與
// assemble（合併、寫出產出），只有 chunk 任務的 executor 是 agent，等 Agent 領取。Agent 提交的輸出用
// 與 RunChunk 相同的 Spec.Validate 驗證（AGT-R5），通過後用 finishChunkTx 寫回，與 api 的 chunk 任務完全相同。
// 租約到期、逾時沒人領取由 AgentSweeper 處理（worker 每一輪開頭執行）。

// agent 區段的預設值（AGT-R4、R9；max_claims 與 on_validation_failure 的預設依 /goal 採保守預設，見 AGT 待決問題）。
const (
	DefaultAgentMaxClaims = 4
	DefaultAgentLease     = 10 * time.Minute
	DefaultAgentTimeout   = 24 * time.Hour

	OnTimeoutFallbackAPI = "fallback_api"
	OnTimeoutNotify      = "notify"

	OnValidationRequeue     = "requeue"
	OnValidationFallbackAPI = "fallback_api"

	// AgentMaxValidationFailures 是同一份租約連續驗證失敗幾次後放回佇列（AGT-R6）。
	AgentMaxValidationFailures = 3
)

// ActionSwitchToAPI 是 Agent 任務逾時通知的動作（AGT-R9）。
const ActionSwitchToAPI = "改用 API 處理"

// 放回原因（tasks.release_reason）中系統自己寫的值；Agent 的 release_task 寫它給的原因。
const (
	releaseLeaseExpired     = "lease_expired"
	releaseValidationFailed = "validation_failed"
)

// AgentSettings 是 agent 區段（AGT 的「處理方案」介面）。
type AgentSettings struct {
	MaxClaims           int
	Lease               time.Duration
	Timeout             time.Duration
	OnTimeout           string
	OnValidationFailure string
}

// Agent 取出 agent 區段，沒有設定的欄位用預設值。
func (e Effective) Agent() (AgentSettings, error) {
	s := AgentSettings{
		MaxClaims: DefaultAgentMaxClaims, Lease: DefaultAgentLease, Timeout: DefaultAgentTimeout,
		OnTimeout: OnTimeoutFallbackAPI, OnValidationFailure: OnValidationRequeue,
	}
	m, _ := e["agent"].(map[string]any)
	if v, ok := m["max_claims"].(float64); ok && v >= 1 && v == float64(int(v)) {
		s.MaxClaims = int(v)
	}
	for key, dst := range map[string]*time.Duration{"lease": &s.Lease, "timeout": &s.Timeout} {
		raw, ok := m[key].(string)
		if !ok || raw == "" {
			continue
		}
		d, err := auth.ParseTokenDuration(raw)
		if err != nil {
			return s, invalidf("agent.%s 必須是時間長度（例如 10m、24h）。", key)
		}
		*dst = d
	}
	if v, ok := m["on_timeout"].(string); ok && v != "" {
		if v != OnTimeoutFallbackAPI && v != OnTimeoutNotify {
			return s, invalidf("agent.on_timeout 不支援 %q。", v)
		}
		s.OnTimeout = v
	}
	if v, ok := m["on_validation_failure"].(string); ok && v != "" {
		if v != OnValidationRequeue && v != OnValidationFallbackAPI {
			return s, invalidf("agent.on_validation_failure 不支援 %q。", v)
		}
		s.OnValidationFailure = v
	}
	return s, nil
}

// AgentError 是 Agent 操作的錯誤：Code 是錯誤碼（lease_expired、not_found、invalid_request、validation_failed…），
// Status 是對應的 HTTP 狀態碼，Reason 是驗證失敗的原因（例如 cue_count），Message 給 Agent 看。
type AgentError struct {
	Status  int
	Code    string
	Reason  string
	Message string
	// Returned 為 true 代表任務已放回佇列（連續驗證失敗，AGT-R6），Agent 不再持有它。
	Returned bool
}

func (e *AgentError) Error() string { return e.Code + ": " + e.Message }

// AgentError 回傳錯誤的欄位（mcptools.AgentFailure）。
func (e *AgentError) AgentError() (status int, code, reason, message string, returned bool) {
	return e.Status, e.Code, e.Reason, e.Message, e.Returned
}

// 錯誤碼。
const (
	AgentCodeLeaseExpired     = "lease_expired"
	AgentCodeNotFound         = "not_found"
	AgentCodeInvalid          = "invalid_request"
	AgentCodeValidationFailed = "validation_failed"
	AgentCodeConflict         = "conflict"
)

func errLeaseExpired() *AgentError {
	return &AgentError{Status: 409, Code: AgentCodeLeaseExpired, Message: "租約已失效：任務已到期、被放回、被別人領取或改用 API 執行。請重新領取。"}
}

func agentInvalid(format string, a ...any) *AgentError {
	return &AgentError{Status: 422, Code: AgentCodeInvalid, Message: fmt.Sprintf(format, a...)}
}

// AgentKinds 是 Agent 省略 kinds 時領取的任務種類（步驟）。translate 的 agent 還不支援（見 Handle）。
var AgentKinds = []string{StageSegment, StageCorrect}

// agentClaimableKinds 是 kinds 可以指定的任務種類：AgentKinds 加上 M3 的 chapters（AGT 的 M3 修訂）。
// 省略 kinds 時仍只領 AgentKinds，既有的 Agent 不會突然收到新的任務。
var agentClaimableKinds = append(slices.Clone(AgentKinds), StageChapters, StageNote, TaskNoteMap)

// AgentTask 是領取到的一個任務與完成它需要的全部上下文（AGT-R2）。
type AgentTask struct {
	TaskID     string    `json:"task_id"`
	LeaseToken string    `json:"lease_token"`
	LeaseUntil time.Time `json:"lease_until"`
	Kind       string    `json:"kind"`
	Item       string    `json:"item"`
	ItemID     string    `json:"item_id"`
	Chunk      int       `json:"chunk"`
	Chunks     int       `json:"chunks"`
	// Prompt 是與 API executor 送給 provider 相同的內容：system 是系統指示，user 是固定前綴（<video_metadata>）
	// 加上資料區塊（<transcript_data>）。
	Prompt AgentPrompt `json:"prompt"`
	// Context 是前文（只供參考，不輸出）；Cues 是要處理的字幕。兩者都是不可信的資料（LLM-R9）。
	Context      []llm.Cue      `json:"context"`
	Cues         []llm.Cue      `json:"cues"`
	OutputSchema map[string]any `json:"output_schema"`
}

// AgentPrompt 是渲染後的 prompt（LLM-R3）。
type AgentPrompt struct {
	System string `json:"system"`
	User   string `json:"user"`
}

// SetAgentLimit 設定 Agent 領取時的資源類別上限（AGT-R2：不超過 llm 類別的並行上限）。
// 沒有設定時用 queue.DefaultLimit。
func (p *Pipeline) SetAgentLimit(limit func(class string) int) { p.agentLimit = limit }

func (p *Pipeline) classLimit(class string) int {
	if p.agentLimit != nil {
		return p.agentLimit(class)
	}
	return queue.DefaultLimit(class)
}

// defaultAgentSettings 是不屬於任何影音的 agent 設定（max_claims）：全域設定加上預設處理方案。
func (p *Pipeline) defaultAgentSettings(ctx context.Context) (AgentSettings, error) {
	l, err := p.currentLayers(ctx, p.st.Read(), "", "", nil, nil)
	if err != nil {
		return AgentSettings{}, err
	}
	eff, err := BuildEffective(l.Global, l.Profile.Config)
	if err != nil {
		return AgentSettings{}, err
	}
	return eff.Agent()
}

// runAgentSettings 是 stage run 的設定快照中的 agent 設定（lease、timeout、on_timeout、on_validation_failure）。
func (p *Pipeline) runAgentSettings(ctx context.Context, r *store.Repo, run store.StageRun) (Effective, AgentSettings, error) {
	snap, err := r.Snapshot(ctx, run.ConfigSnapshotID)
	if err != nil {
		return nil, AgentSettings{}, err
	}
	eff, err := ParseEffective(snap)
	if err != nil {
		return nil, AgentSettings{}, err
	}
	as, err := eff.Agent()
	return eff, as, err
}

// ClaimAgentTasks 讓 agent 領取最多 max 個 kinds 種類的任務（AGT-R2、R3）。領取的數量不超過 agent.max_claims，
// 而且這個 Agent 持有的 llm 任務加上這次領取的，不超過 llm 類別的並行上限（依 /goal 的保守預設，見 AGT 待決問題）。
// 計數與領取在同一個寫入交易中：兩個 Agent 同時領取不會拿到同一個任務，同一個 Agent 並行領取也不會超過上限。
func (p *Pipeline) ClaimAgentTasks(ctx context.Context, agent string, kinds []string, max int) ([]AgentTask, error) {
	if agent == "" {
		return nil, agentInvalid("缺少 Agent 的名稱。")
	}
	for _, k := range kinds {
		if !slices.Contains(agentClaimableKinds, k) {
			return nil, agentInvalid("不支援的任務種類 %q（可以領取：%s）。", k, strings.Join(agentClaimableKinds, "、"))
		}
	}
	if max < 0 {
		return nil, agentInvalid("max 必須是正整數。")
	}
	if len(kinds) == 0 {
		kinds = AgentKinds
	}
	ds, err := p.defaultAgentSettings(ctx)
	if err != nil {
		return nil, err
	}
	n := ds.MaxClaims
	if max > 0 {
		n = min(n, max)
	}
	var claimed []store.Task
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		claimed = nil
		held, err := r.CountAgentHeld(ctx, agent, queue.ResourceLLM)
		if err != nil {
			return err
		}
		room := min(n, p.classLimit(queue.ResourceLLM)-held)
		if room <= 0 {
			return nil
		}
		got, err := r.ClaimTasks(ctx, store.ClaimOptions{
			Owner: agent, Max: room, Lease: DefaultAgentLease, Executor: ExecutorAgent,
			ResourceClasses: []string{queue.ResourceLLM}, Kinds: kinds, AgingInterval: queue.AgingInterval,
		})
		if err != nil {
			return err
		}
		// 租約長度依各任務的設定快照（agent.lease）。
		now := p.clock.Now()
		for i, t := range got {
			run, err := r.StageRun(ctx, t.StageRunID)
			if err != nil {
				return err
			}
			_, as, err := p.runAgentSettings(ctx, r, run)
			if err != nil {
				return err
			}
			until := now.Add(as.Lease)
			if err := r.ExtendAgentLease(ctx, t.ID, t.LeaseToken, agent, until); err != nil {
				return err
			}
			got[i].LeaseUntil = &until
		}
		claimed = got
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]AgentTask, 0, len(claimed))
	for _, t := range claimed {
		at, err := p.agentTaskContext(ctx, t)
		if err != nil {
			// 組不出上下文（例如輸入的字幕產出已不存在）：放回佇列、不計次數，記 log；不讓其他任務領不到。
			p.log.Warn("無法組出 Agent 任務的上下文，放回佇列", "task", t.ID, "error", err)
			_ = p.st.Tx(ctx, func(r *store.Repo) error {
				return p.requeueAgent(ctx, r, t, "context_unavailable", false)
			})
			continue
		}
		out = append(out, at)
	}
	return out, nil
}

// agentChunk 是一個 Agent 任務對應的 stage run、影音、llmJob 與 chunk 輸入。
type agentChunk struct {
	run  store.StageRun
	item store.Item
	eff  Effective
	job  *llmJob
	plan chunkPlan
	in   llm.ChunkInput
}

// loadAgentChunk 依任務重建 chunk 的輸入：與 llmChunk 相同的 prepareLLM，所以 prompt 與驗證都和 API executor 一致。
func (p *Pipeline) loadAgentChunk(ctx context.Context, t store.Task) (*agentChunk, error) {
	if t.ChunkIndex == nil {
		return nil, errPermanent("Agent 任務沒有 chunk 編號")
	}
	r := p.st.Read()
	run, err := r.StageRun(ctx, t.StageRunID)
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
	row, err := r.LLMChunk(ctx, run.ID, *t.ChunkIndex)
	if err != nil {
		return nil, err
	}
	var plan chunkPlan
	if err := json.Unmarshal(row.Plan, &plan); err != nil {
		return nil, errPermanent("chunk 的記錄不正確：" + err.Error())
	}
	job, err := p.prepareLLM(ctx, it, run.Stage, eff, plan.Input)
	if err != nil {
		return nil, err
	}
	if plan.To > len(job.cues) || plan.From < 1 || plan.Chunks != len(job.chunks) {
		return nil, errPermanent(fmt.Sprintf("chunk %d 的範圍與輸入字幕不符", plan.Index))
	}
	in, err := p.chunkInputFor(ctx, r, run.ID, job, plan.Chunk)
	if err != nil {
		return nil, err
	}
	return &agentChunk{run: run, item: it, eff: eff, job: job, plan: plan, in: in}, nil
}

func (p *Pipeline) agentTaskContext(ctx context.Context, t store.Task) (AgentTask, error) {
	c, err := p.loadAgentChunk(ctx, t)
	if err != nil {
		return AgentTask{}, err
	}
	data, err := llm.DataBlock(c.in.Chunk.Index, c.in.Chunks, c.in.Context, c.in.Cues)
	if err != nil {
		return AgentTask{}, err
	}
	at := AgentTask{
		TaskID: t.ID, LeaseToken: t.LeaseToken, Kind: c.job.taskKind(c.plan.Index),
		Item: source.Ref(c.item.Platform, c.item.ExternalID, c.item.Part), ItemID: c.item.ID,
		Chunk: c.plan.Index, Chunks: len(c.job.chunks),
		Prompt:  AgentPrompt{System: c.job.fixedFor(c.plan.Chunk).System, User: c.job.fixedFor(c.plan.Chunk).Prefix + "\n\n" + data},
		Context: c.in.Context, Cues: c.in.Cues, OutputSchema: c.job.specFor(c.plan.Chunk).Schema,
	}
	if t.LeaseUntil != nil {
		at.LeaseUntil = t.LeaseUntil.UTC()
	}
	return at, nil
}

// heldTask 讀取 agent 以 token 持有、還沒到期的任務；否則回傳 lease_expired（AGT-R7）或 not_found。
func (p *Pipeline) heldTask(ctx context.Context, r *store.Repo, agent, taskID, token string) (store.Task, error) {
	if taskID == "" || token == "" {
		return store.Task{}, agentInvalid("需要 task_id 與 lease_token。")
	}
	t, err := r.AgentTask(ctx, taskID)
	if errors.Is(err, store.ErrNotFound) {
		// 改用 API 執行的任務已不是 agent 任務：對持有舊 token 的 Agent 來說就是租約失效（AGT-R11）。
		if bt, berr := r.Task(ctx, taskID); berr == nil && bt.ChunkIndex != nil {
			return store.Task{}, errLeaseExpired()
		}
		return store.Task{}, &AgentError{Status: 404, Code: AgentCodeNotFound, Message: "找不到這個任務。"}
	}
	if err != nil {
		return store.Task{}, err
	}
	if t.State != "leased" || t.LeaseToken != token || t.LeaseOwner != agent ||
		t.LeaseUntil == nil || !t.LeaseUntil.After(p.clock.Now()) {
		return store.Task{}, errLeaseExpired()
	}
	return t, nil
}

// ExtendAgentLease 把租約延長到現在加上一個租約期間（agent.lease，AGT-R4），回傳新的到期時間。
func (p *Pipeline) ExtendAgentLease(ctx context.Context, agent, taskID, token string) (time.Time, error) {
	var until time.Time
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		t, err := p.heldTask(ctx, r, agent, taskID, token)
		if err != nil {
			return err
		}
		run, err := r.StageRun(ctx, t.StageRunID)
		if err != nil {
			return err
		}
		_, as, err := p.runAgentSettings(ctx, r, run)
		if err != nil {
			return err
		}
		until = p.clock.Now().Add(as.Lease).UTC()
		if err := r.ExtendAgentLease(ctx, t.ID, token, agent, until); errors.Is(err, store.ErrLeaseLost) {
			return errLeaseExpired()
		} else if err != nil {
			return err
		}
		return nil
	})
	return until, err
}

// ReleaseAgentTask 立即把任務放回佇列並記錄原因，不計入 attempts（AGT-R8）。
func (p *Pipeline) ReleaseAgentTask(ctx context.Context, agent, taskID, token, reason string) error {
	reason = sanitizeAgentText(reason, maxAgentReasonLen)
	return p.st.Tx(ctx, func(r *store.Repo) error {
		t, err := p.heldTask(ctx, r, agent, taskID, token)
		if err != nil {
			return err
		}
		return p.requeueAgent(ctx, r, t, reason, false)
	})
}

// requeueAgent 把 Agent 持有的任務放回佇列：重新開始計算領取期限；步驟沒有任務被領取時回到等待 Agent。
func (p *Pipeline) requeueAgent(ctx context.Context, r *store.Repo, t store.Task, reason string, countAttempt bool) error {
	run, err := r.StageRun(ctx, t.StageRunID)
	if err != nil {
		return err
	}
	_, as, err := p.runAgentSettings(ctx, r, run)
	if err != nil {
		return err
	}
	deadline := p.clock.Now().Add(as.Timeout)
	if err := r.RequeueAgentTask(ctx, t.ID, t.LeaseToken, reason, countAttempt, &deadline); err != nil {
		if errors.Is(err, store.ErrLeaseLost) {
			return errLeaseExpired()
		}
		return err
	}
	return awaitAgentIfIdle(ctx, r, run)
}

// awaitAgentIfIdle 在 Agent 步驟沒有任何被領取的任務、但還有任務等 Agent 領取時，把步驟改回 awaiting_agent（AGT-R1）。
func awaitAgentIfIdle(ctx context.Context, r *store.Repo, run store.StageRun) error {
	if run.Executor != ExecutorAgent {
		return nil
	}
	for _, executor := range []string{ExecutorAgent, queue.ExecutorBuiltin} {
		n, err := r.CountRunTasks(ctx, run.ID, executor, "leased")
		if err != nil || n > 0 {
			return err
		}
	}
	waiting, err := r.CountRunTasks(ctx, run.ID, ExecutorAgent, "pending")
	if err != nil || waiting == 0 {
		return err
	}
	_, err = r.AwaitAgentStage(ctx, run.ID)
	return err
}

// AgentSubmission 是 submit_task 的內容。
type AgentSubmission struct {
	Output json.RawMessage
	// ModelReported 是 Agent 自己回報的模型（AGT-R10，可省略，不驗證）。
	ModelReported string
}

// SubmitAgentTask 驗證並寫回 Agent 的結果（AGT-R5）。驗證和 API executor 一樣用 Spec.Validate（同一個 Spec 與 chunk 輸入）；
// 失敗時回傳 validation_failed（Reason 是失敗的原因），租約保留；同一份租約連續 AgentMaxValidationFailures 次失敗時
// 任務放回佇列，agent.on_validation_failure 是 fallback_api 時改用 API executor（AGT-R6）。
func (p *Pipeline) SubmitAgentTask(ctx context.Context, agent, taskID, token string, sub AgentSubmission) error {
	t, err := p.heldTask(ctx, p.st.Read(), agent, taskID, token)
	if err != nil {
		return err
	}
	c, err := p.loadAgentChunk(ctx, t)
	if err != nil {
		return err
	}
	out, verr := c.job.specFor(c.plan.Chunk).Validate(c.in, sub.Output)
	if verr != nil {
		return p.rejectSubmission(ctx, agent, t, verr)
	}
	res := result{task: taskDone, keepState: true, inTx: func(ctx context.Context, tx *store.Repo) (bool, error) {
		wake, err := finishChunkTx(ctx, tx, c.item.ID, c.run, t.Priority, c.plan.Index, store.ChunkResult{
			Status: store.ChunkDone, Output: out, Usage: json.RawMessage(`{}`),
			By: store.ChunkProducer{Executor: ExecutorAgent, Agent: agent, ModelReported: sanitizeAgentText(sub.ModelReported, maxAgentModelLen)},
		})
		if err != nil || p.cost == nil {
			return wake, err
		}
		// Agent 的結果記為 unknown、0 元，不預留、不計入預算（COST-R3）；只記在這條提交路徑，api 的 chunk 不經過這裡。
		return wake, p.cost.RecordAgent(ctx, tx, cost.Call{
			ItemID: c.item.ID, ItemTitle: c.item.Title, StageRunID: c.run.ID, TaskID: t.ID, Stage: c.run.Stage,
			Provider: c.job.cfg.Provider, Model: c.job.cfg.Model,
		}, 0, 0)
	}}
	lost, err := p.applyResult(ctx, t, c.run, c.item, res)
	if err != nil {
		return err
	}
	if lost {
		return errLeaseExpired()
	}
	return nil
}

// rejectSubmission 記一次驗證失敗；達到上限時依 on_validation_failure 放回佇列或改用 API。
func (p *Pipeline) rejectSubmission(ctx context.Context, agent string, t store.Task, verr *llm.ValidationError) error {
	ae := &AgentError{Status: 422, Code: AgentCodeValidationFailed, Reason: verr.Reason,
		Message: verr.Reason + ": " + verr.Message}
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		if _, err := p.heldTask(ctx, r, agent, t.ID, t.LeaseToken); err != nil {
			return err
		}
		n, err := r.AddAgentFailure(ctx, t.ID, t.LeaseToken)
		if errors.Is(err, store.ErrLeaseLost) {
			return errLeaseExpired()
		}
		if err != nil {
			return err
		}
		if n < AgentMaxValidationFailures {
			ae.Message += fmt.Sprintf("（第 %d 次驗證失敗，連續 %d 次時任務會放回佇列）", n, AgentMaxValidationFailures)
			return nil
		}
		ae.Returned = true
		ae.Message += fmt.Sprintf("（連續 %d 次驗證失敗，任務已放回佇列）", n)
		run, err := r.StageRun(ctx, t.StageRunID)
		if err != nil {
			return err
		}
		eff, as, err := p.runAgentSettings(ctx, r, run)
		if err != nil {
			return err
		}
		if as.OnValidationFailure == OnValidationFallbackAPI && stageHasAPI(eff, run.Stage) {
			if _, err := r.SwitchAgentTaskToBuiltin(ctx, t.ID); err != nil {
				return err
			}
			return nil
		}
		return p.requeueAgent(ctx, r, t, releaseValidationFailed, true)
	})
	if err != nil {
		return err
	}
	if ae.Returned {
		p.wake()
	}
	return ae
}

// stageHasAPI 回報步驟在這份設定中有沒有 provider 與 model：改用 API 執行時必須有（AGT-R9）。
func stageHasAPI(eff Effective, stage string) bool {
	cfg, err := eff.Stage(stage)
	return err == nil && cfg.Provider != "" && cfg.Model != ""
}

// Agent 提供的文字的上限：寫進來源紀錄與任務，顯示在畫面上，是不可信的輸入。
const (
	maxAgentModelLen  = 200
	maxAgentReasonLen = 500
)

// sanitizeAgentText 去掉控制字元與前後空白，並截斷到 limit 個字元。
func sanitizeAgentText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit])
	}
	return s
}

// agentProvenance 依 Agent 提交的 chunk 改寫來源紀錄（AGT-R10）：executor 是 agent、agent 是 token 名稱、
// model_reported 是 Agent 回報的模型（每個 Agent chunk 都回報同一個時才寫）、model_verified 是 false。
// 沒有任何 chunk 由 provider 產生時不寫 provider 與 model（沒有呼叫它們）；混合時保留，每個 chunk 的來源在 chunks[]。
func agentProvenance(fields map[string]any, rows []store.LLMChunk, hasAPI bool) {
	var agents, models []string
	for _, row := range rows {
		if !slices.Contains(agents, row.Agent) {
			agents = append(agents, row.Agent)
		}
		if !slices.Contains(models, row.ModelReported) {
			models = append(models, row.ModelReported)
		}
	}
	fields["executor"] = ExecutorAgent
	fields["agent"] = strings.Join(agents, ",")
	if len(models) == 1 && models[0] != "" {
		fields["model_reported"] = models[0]
	}
	fields["model_verified"] = false
	if !hasAPI {
		delete(fields, "provider")
		delete(fields, "model")
	}
}

// AgentSweeper 是 worker 的定期工作（worker.Periodic）：租約到期的 Agent 任務回到佇列（AGT-R4），
// 超過 agent.timeout 沒有被領取的依 agent.on_timeout 處理（AGT-R9）。
type AgentSweeper struct{ p *Pipeline }

// AgentSweeper 回傳 Pipeline 的 sweeper。
func (p *Pipeline) AgentSweeper() *AgentSweeper { return &AgentSweeper{p: p} }

// Run 處理到期的租約與逾時的任務。
func (s *AgentSweeper) Run(ctx context.Context) error {
	p := s.p
	wake := false
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		wake = false
		expired, err := r.ExpiredAgentLeases(ctx)
		if err != nil {
			return err
		}
		for _, t := range expired {
			// 到期也算一次嘗試（領取時 attempts 已加 1，這裡不減回去）。
			if err := p.requeueAgent(ctx, r, t, releaseLeaseExpired, true); err != nil && !isLeaseExpired(err) {
				return err
			}
		}
		timedOut, err := r.TimedOutAgentTasks(ctx)
		if err != nil {
			return err
		}
		done := map[string]bool{}
		for _, t := range timedOut {
			if done[t.StageRunID] {
				continue
			}
			done[t.StageRunID] = true
			w, err := p.agentTimeout(ctx, r, t.StageRunID)
			if err != nil {
				return err
			}
			wake = wake || w
		}
		return nil
	})
	if err == nil && wake {
		p.wake()
	}
	return err
}

func isLeaseExpired(err error) bool {
	var ae *AgentError
	return errors.As(err, &ae) && ae.Code == AgentCodeLeaseExpired
}

// agentTimeout 處理一個 stage run 中逾時沒人領取的任務（AGT-R9）。回傳是否把任務交給了內建 worker。
func (p *Pipeline) agentTimeout(ctx context.Context, r *store.Repo, runID string) (bool, error) {
	run, err := r.StageRun(ctx, runID)
	if err != nil {
		return false, err
	}
	runs, err := r.StageRunsByItem(ctx, run.ItemID)
	if err != nil {
		return false, err
	}
	if latestRuns(runs)[run.Stage].ID != run.ID || stageFinished(run.Status) {
		// 已被較新的重跑取代或已結束：任務不再需要，清掉期限，不通知。
		return false, r.ClearAgentDeadlines(ctx, run.ID)
	}
	eff, as, err := p.runAgentSettings(ctx, r, run)
	if err != nil {
		return false, err
	}
	if as.OnTimeout == OnTimeoutFallbackAPI && stageHasAPI(eff, run.Stage) {
		if _, err := r.SwitchAgentTasksToBuiltin(ctx, run.ID); err != nil {
			return false, err
		}
		p.log.Info("Agent 任務逾時沒有被領取，改用 API 執行", "item", run.ItemID, "stage", run.Stage)
		return true, nil
	}
	// notify，或 fallback_api 但沒有設定 provider 與 model：步驟 blocked，請使用者處理；只通知一次。
	if err := r.ClearAgentDeadlines(ctx, run.ID); err != nil {
		return false, err
	}
	leased, err := r.CountRunTasks(ctx, run.ID, ExecutorAgent, "leased")
	if err != nil || leased > 0 {
		// 有 Agent 正在處理其他 chunk：不打斷，等它們完成。
		return false, err
	}
	return false, r.SetStageState(ctx, run.ID, store.StageState{
		Status: store.StatusBlocked, BlockReason: "needs_action", ErrorClass: "needs_action",
		Message: fmt.Sprintf("這個任務等待 Agent 超過 %s。", humanDuration(as.Timeout)),
		Actions: []string{ActionSwitchToAPI},
	})
}

// stageFinished 回報步驟是否已經結束（不會再改變）。
func stageFinished(status string) bool {
	switch status {
	case store.StatusDone, store.StatusFailed, store.StatusSkipped:
		return true
	}
	return false
}

// humanDuration 把時間長度寫成中文：整數小時寫「N 小時」、整數分鐘寫「N 分鐘」，其他用 Go 的寫法。
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d 小時", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d 分鐘", d/time.Minute)
	}
	return d.String()
}

// NextAt 回傳最早到期的租約或領取期限（production 的 worker 閒置時用它決定何時醒來）。
func (s *AgentSweeper) NextAt(ctx context.Context) (time.Time, bool, error) {
	return s.p.st.Read().NextAgentTime(ctx)
}

// SwitchResult 是改用 API 執行的結果。
type SwitchResult struct {
	ItemID   string   `json:"item_id"`
	Stage    string   `json:"stage"`
	Executor string   `json:"executor"`
	Tasks    []string `json:"tasks"`
}

// SwitchStageExecutor 把影音某個步驟等待中或已被領取的 Agent 任務改用 API 執行（AGT-R11）：原本的租約立即失效。
// 目前只支援改成 api；處理方案必須為這個步驟設定 provider 與 model。
func (p *Pipeline) SwitchStageExecutor(ctx context.Context, itemRef, stage, executor string) (SwitchResult, error) {
	if executor != ExecutorAPI {
		return SwitchResult{}, invalidf("executor 只能是 api。")
	}
	if !slices.Contains(agentClaimableKinds, stage) && stage != StageTranslate {
		return SwitchResult{}, invalidf("步驟 %q 不是 LLM 步驟。", stage)
	}
	it, err := p.itemByIDOrRef(ctx, itemRef)
	if err != nil {
		return SwitchResult{}, err
	}
	var out SwitchResult
	err = p.st.Tx(ctx, func(r *store.Repo) error {
		runs, err := r.StageRunsByItem(ctx, it.ID)
		if err != nil {
			return err
		}
		run, ok := latestRuns(runs)[stage]
		if !ok {
			return ErrNoAgentTasks
		}
		ids, err := p.switchRun(ctx, r, run)
		if err != nil {
			return err
		}
		out = SwitchResult{ItemID: it.ID, Stage: stage, Executor: ExecutorAPI, Tasks: ids}
		return nil
	})
	if err != nil {
		return SwitchResult{}, err
	}
	p.wake()
	return out, nil
}

// SwitchTaskExecutor 把一個等待中或已被領取的 Agent 任務改用 API 執行（AGT-R11）。
func (p *Pipeline) SwitchTaskExecutor(ctx context.Context, taskID, executor string) (SwitchResult, error) {
	if executor != ExecutorAPI {
		return SwitchResult{}, invalidf("executor 只能是 api。")
	}
	var out SwitchResult
	err := p.st.Tx(ctx, func(r *store.Repo) error {
		t, err := r.AgentTask(ctx, taskID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNoAgentTasks
		}
		if err != nil {
			return err
		}
		if t.State != "pending" && t.State != "leased" {
			return ErrNoAgentTasks
		}
		run, err := r.StageRun(ctx, t.StageRunID)
		if err != nil {
			return err
		}
		eff, _, err := p.runAgentSettings(ctx, r, run)
		if err != nil {
			return err
		}
		if !stageHasAPI(eff, run.Stage) {
			return ErrNoAPIConfigured
		}
		if _, err := r.SwitchAgentTaskToBuiltin(ctx, t.ID); err != nil {
			return err
		}
		if run.Status == store.StatusBlocked || run.Status == store.StatusAwaitingAgent {
			if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
				return err
			}
		}
		out = SwitchResult{ItemID: run.ItemID, Stage: run.Stage, Executor: ExecutorAPI, Tasks: []string{t.ID}}
		return nil
	})
	if err != nil {
		return SwitchResult{}, err
	}
	p.wake()
	return out, nil
}

// switchRun 把 stage run 中還沒完成的 Agent 任務交給內建 worker；步驟改回 pending（之後由 worker 領取）。
func (p *Pipeline) switchRun(ctx context.Context, r *store.Repo, run store.StageRun) ([]string, error) {
	if stageFinished(run.Status) {
		return nil, ErrNoAgentTasks
	}
	eff, _, err := p.runAgentSettings(ctx, r, run)
	if err != nil {
		return nil, err
	}
	if !stageHasAPI(eff, run.Stage) {
		return nil, ErrNoAPIConfigured
	}
	ids, err := r.SwitchAgentTasksToBuiltin(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrNoAgentTasks
	}
	if run.Status != store.StatusRunning {
		if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusPending}); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// 改用 API 執行的錯誤（API 對應成 409）。
var (
	ErrNoAgentTasks    = errors.New("這個步驟沒有等待 Agent 處理的任務。")
	ErrNoAPIConfigured = errors.New("處理方案沒有為這個步驟設定 provider 與 model，無法改用 API 處理。")
)
