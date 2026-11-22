package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// newAgentFlowRig 建立 correct 走 agent 的 rig：4 句的 segmented、每句一個 chunk，所以有 4 個 Agent 任務。
// agent 是處理方案的 agent 區段（nil 用預設）；stage 覆寫 stages.correct（nil 用 agent＋anthropic／m）。
func newAgentFlowRig(t *testing.T, agent, stage map[string]any) (*rig, store.Item, *chunkLLM) {
	t.Helper()
	fake := &chunkLLM{}
	r, it := newLLMRigCap(t, fake, 1)
	if stage == nil {
		stage = map[string]any{"executor": "agent", "provider": "anthropic", "model": "m", "prompt": "correct-default"}
	}
	over := map[string]any{"stages": map[string]any{"correct": stage}}
	if agent != nil {
		over["agent"] = agent
	}
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageCorrect, Until: StageCorrect, Overrides: over}); err != nil {
		t.Fatalf("Rerun：%v", err)
	}
	r.run()
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusAwaitingAgent {
		t.Fatalf("correct = %+v，預期 awaiting_agent", st)
	}
	return r, it, fake
}

// newAgentNoProviderRig 是 correct 走 agent、處理方案沒有設定 provider 與 model 的 rig（影音只有一個 chunk）。
func newAgentNoProviderRig(t *testing.T, agent map[string]any) (*rig, store.Item) {
	t.Helper()
	r := newAgentRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	over := agentCorrect()
	if agent != nil {
		over["agent"] = agent
	}
	it := r.ingest(watch, over, "")
	r.run()
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusAwaitingAgent {
		t.Fatalf("correct = %+v，預期 awaiting_agent", st)
	}
	return r, it
}

// kindPrompts 是 PromptResolver 的替身：prompt 的類型取自引用的前綴（translate-default 是 translate）。
type kindPrompts struct{}

func (kindPrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	kind, _, _ := strings.Cut(id, "-")
	return ResolvedPrompt{ID: id, Kind: kind, Version: 1, SHA256: "sha-" + id, Body: "照做。"}, nil
}

func claim(t *testing.T, r *rig, agent string, max int) []AgentTask {
	t.Helper()
	got, err := r.p.ClaimAgentTasks(context.Background(), agent, []string{StageCorrect}, max)
	if err != nil {
		t.Fatalf("ClaimAgentTasks(%s)：%v", agent, err)
	}
	return got
}

// echoOutput 是一對一照抄的輸出（每句加上 "!"，和 chunkLLM 的 echo 相同）；drop 為 true 時少一句。
func echoOutput(at AgentTask, drop bool) json.RawMessage {
	cues := make([]llm.Cue, 0, len(at.Cues))
	for _, c := range at.Cues {
		cues = append(cues, llm.Cue{I: c.I, Text: c.Text + "!"})
	}
	if drop {
		cues = cues[:len(cues)-1]
	}
	b, _ := json.Marshal(map[string]any{"cues": cues})
	return b
}

func agentErr(t *testing.T, err error, code string) *AgentError {
	t.Helper()
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("錯誤 = %v，預期 %s", err, code)
	}
	return ae
}

func taskByID(t *testing.T, r *rig, id string) store.Task {
	t.Helper()
	tk, err := r.st.Read().Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// AGT-R2：任務附帶完成它需要的上下文；prompt 與 API executor 送給 provider 的相同（固定前綴加資料區塊），
// output_schema 是步驟的 schema；lease_until 是領取時間加上 agent.lease。
func TestAgentClaimCarriesFullContext(t *testing.T) {
	r, _, _ := newAgentFlowRig(t, map[string]any{"lease": "7m"}, nil)
	got := claim(t, r, "a1", 1)
	if len(got) != 1 {
		t.Fatalf("領取 %d 個，預期 1", len(got))
	}
	at := got[0]
	if at.Chunk != 1 || at.Chunks != 4 || len(at.Cues) != 1 || at.Cues[0].Text != "alpha" || at.Kind != StageCorrect {
		t.Errorf("任務 = %+v", at)
	}
	if !at.LeaseUntil.Equal(r.clk.Now().Add(7 * time.Minute)) {
		t.Errorf("lease_until = %v，預期現在加 7 分鐘", at.LeaseUntil)
	}
	if !strings.Contains(at.Prompt.System, llm.GuardInstruction) || !strings.Contains(at.Prompt.User, llm.MetadataOpen) ||
		!strings.Contains(at.Prompt.User, llm.DataOpen) || !strings.Contains(at.Prompt.User, `"text":"alpha"`) {
		t.Errorf("prompt = %+v", at.Prompt)
	}
	if at.OutputSchema["type"] != "object" {
		t.Errorf("output_schema = %v", at.OutputSchema)
	}
	if tk := taskByID(t, r, at.TaskID); tk.State != store.TaskLeased || tk.LeaseOwner != "a1" || tk.Attempts != 1 {
		t.Errorf("任務 = %+v", tk)
	}
	if st := latestStage(t, r, at.ItemID, StageCorrect); st.Status != store.StatusRunning {
		t.Errorf("領取之後 correct = %s，預期 running", st.Status)
	}
}

// AGT-R3：多個 Agent 同時領取，同一個任務只會被一個 Agent 拿到，全部任務都被領走。
func TestAgentClaimIsAtomic(t *testing.T) {
	r, _, _ := newAgentFlowRig(t, nil, nil)
	var (
		mu   sync.Mutex
		seen = map[string]string{}
		wg   sync.WaitGroup
		errs []error
	)
	for i := range 8 {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			got, err := r.p.ClaimAgentTasks(context.Background(), name, nil, 4)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			for _, at := range got {
				if other, dup := seen[at.TaskID]; dup {
					errs = append(errs, fmt.Errorf("任務 %s 同時被 %s 與 %s 領取", at.TaskID, other, name))
				}
				seen[at.TaskID] = name
			}
		}(fmt.Sprintf("agent-%d", i))
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatal(errors.Join(errs...))
	}
	if len(seen) != 4 {
		t.Errorf("領走 %d 個任務，預期 4 個全部", len(seen))
	}
}

// AGT-R2：一次最多 max_claims 個；一個 Agent 持有的加上這次領取的不超過 llm 類別的上限；暫停時（上限 0）不發出任務。
func TestAgentClaimLimits(t *testing.T) {
	r, _, _ := newAgentFlowRig(t, nil, nil)
	limit := 3
	r.p.SetAgentLimit(func(string) int { return limit })
	if got := claim(t, r, "a1", 10); len(got) != 3 {
		t.Fatalf("第一次領取 %d 個，預期 3（llm 上限）", len(got))
	}
	if got := claim(t, r, "a1", 10); len(got) != 0 {
		t.Errorf("已持有 3 個時再領到 %d 個，預期 0", len(got))
	}
	if got := claim(t, r, "a2", 10); len(got) != 1 {
		t.Errorf("另一個 Agent 領到 %d 個，預期剩下的 1 個", len(got))
	}

	r2, _, _ := newAgentFlowRig(t, nil, nil)
	r2.p.SetAgentLimit(func(string) int { return 0 })
	if got := claim(t, r2, "a1", 10); len(got) != 0 {
		t.Errorf("暫停時領到 %d 個，預期 0", len(got))
	}
	if _, err := r2.p.ClaimAgentTasks(context.Background(), "a1", []string{StageTranslate}, 1); err == nil {
		t.Error("領取 translate 應回錯誤（還不支援 agent）")
	}
}

// AGT-R5、R10：提交走和 API 相同的驗證；所有 chunk 都完成後 assemble，產出的來源紀錄標成 agent、
// 模型沒有驗證；Agent 的結果不進步驟層與 chunk 層的快取，之後同設定的 api 重跑會真的呼叫 provider。
func TestAgentSubmitCompletesStageWithAgentProvenance(t *testing.T) {
	r, it, fake := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	tasks := claim(t, r, "claude-code", 10)
	if len(tasks) != 4 {
		t.Fatalf("領取 %d 個", len(tasks))
	}
	for _, at := range tasks {
		if err := r.p.SubmitAgentTask(ctx, "claude-code", at.TaskID, at.LeaseToken, AgentSubmission{Output: echoOutput(at, false), ModelReported: "m-rep\x1b"}); err != nil {
			t.Fatalf("提交 chunk %d：%v", at.Chunk, err)
		}
		if tk := taskByID(t, r, at.TaskID); tk.State != store.TaskDone {
			t.Errorf("任務 = %+v，預期 done", tk)
		}
	}
	r.run()
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusDone || st.CacheKey != "" {
		t.Fatalf("correct = %+v，預期 done、沒有步驟層的快取鍵", st)
	}
	art, err := r.st.Read().Artifact(ctx, st.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	var prov map[string]any
	if err := json.Unmarshal(art.Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	if prov["executor"] != "agent" || prov["agent"] != "claude-code" || prov["model_reported"] != "m-rep" || prov["model_verified"] != false {
		t.Errorf("來源紀錄 = %v", prov)
	}
	if _, ok := prov["provider"]; ok {
		t.Errorf("沒有呼叫 provider，來源紀錄不應有 provider：%v", prov)
	}
	if n := fake.callCount(); n != 0 {
		t.Errorf("provider 被呼叫 %d 次", n)
	}
	// 同設定改用 api 重跑：Agent 的結果不重用。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageCorrect, Until: StageCorrect, Overrides: map[string]any{
		"stages": map[string]any{"correct": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "correct-default"}},
	}}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if n := fake.callCount(); n != 4 {
		t.Errorf("api 重跑呼叫 provider %d 次，預期 4（不重用 Agent 的 chunk）", n)
	}
}

// AGT-R5、R6：驗證失敗回傳原因、租約保留；同一份租約連續 3 次失敗放回佇列（計入 attempts），步驟回到 awaiting_agent。
func TestAgentValidationFailuresRequeue(t *testing.T) {
	r, it, _ := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	at := claim(t, r, "a1", 1)[0]
	for i := 1; i <= 2; i++ {
		err := r.p.SubmitAgentTask(ctx, "a1", at.TaskID, at.LeaseToken, AgentSubmission{Output: echoOutput(at, true)})
		ae := agentErr(t, err, AgentCodeValidationFailed)
		if ae.Reason != llm.ReasonCueCount || ae.Returned || !strings.HasPrefix(ae.Message, "cue_count") {
			t.Errorf("第 %d 次：%+v", i, ae)
		}
		if tk := taskByID(t, r, at.TaskID); tk.State != store.TaskLeased || tk.LeaseToken != at.LeaseToken || tk.AgentFailures != i {
			t.Errorf("第 %d 次之後任務 = %+v，預期租約保留", i, tk)
		}
	}
	err := r.p.SubmitAgentTask(ctx, "a1", at.TaskID, at.LeaseToken, AgentSubmission{Output: echoOutput(at, true)})
	if ae := agentErr(t, err, AgentCodeValidationFailed); !ae.Returned {
		t.Errorf("第 3 次：%+v，預期放回佇列", ae)
	}
	tk := taskByID(t, r, at.TaskID)
	if tk.State != store.TaskPending || tk.Executor != ExecutorAgent || tk.Attempts != 1 || tk.ReleaseReason != releaseValidationFailed || tk.AgentFailures != 0 {
		t.Errorf("任務 = %+v，預期 pending、attempts 1", tk)
	}
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusAwaitingAgent {
		t.Errorf("correct = %s，預期 awaiting_agent", st.Status)
	}
	// 放回之後，舊的 token 不能再提交。
	err = r.p.SubmitAgentTask(ctx, "a1", at.TaskID, at.LeaseToken, AgentSubmission{Output: echoOutput(at, false)})
	agentErr(t, err, AgentCodeLeaseExpired)
	// 重新領取時失敗次數重新計算。
	again := claim(t, r, "a2", 1)
	if len(again) != 1 || again[0].TaskID != at.TaskID {
		t.Fatalf("重新領取 = %+v", again)
	}
	err = r.p.SubmitAgentTask(ctx, "a2", at.TaskID, again[0].LeaseToken, AgentSubmission{Output: echoOutput(at, true)})
	if ae := agentErr(t, err, AgentCodeValidationFailed); ae.Returned {
		t.Error("重新領取後的第一次失敗不應放回")
	}
}

// AGT-R6：on_validation_failure: fallback_api 時，連續失敗的任務改用 API executor。
func TestAgentValidationFailuresFallBackToAPI(t *testing.T) {
	r, _, fake := newAgentFlowRig(t, map[string]any{"on_validation_failure": "fallback_api"}, nil)
	ctx := context.Background()
	at := claim(t, r, "a1", 1)[0]
	for range AgentMaxValidationFailures {
		_ = r.p.SubmitAgentTask(ctx, "a1", at.TaskID, at.LeaseToken, AgentSubmission{Output: echoOutput(at, true)})
	}
	if tk := taskByID(t, r, at.TaskID); tk.Executor != queue.ExecutorBuiltin || tk.State != store.TaskPending {
		t.Fatalf("任務 = %+v，預期交給內建 worker", tk)
	}
	r.run()
	if n := fake.callCount(); n != 1 {
		t.Errorf("provider 被呼叫 %d 次，預期 1（只有那個 chunk）", n)
	}
}

// AGT-R8：release_task 立即放回、記錄原因、不計入 attempts。
func TestAgentReleaseDoesNotCountAttempt(t *testing.T) {
	r, it, _ := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	at := claim(t, r, "a1", 1)[0]
	if err := r.p.ReleaseAgentTask(ctx, "a1", at.TaskID, at.LeaseToken, "out of context window\n"); err != nil {
		t.Fatal(err)
	}
	tk := taskByID(t, r, at.TaskID)
	if tk.State != store.TaskPending || tk.Attempts != 0 || tk.ReleaseReason != "out of context window" || tk.LeaseToken != "" {
		t.Errorf("任務 = %+v", tk)
	}
	if tk.DeadlineAt == nil || !tk.DeadlineAt.Equal(r.clk.Now().Add(DefaultAgentTimeout)) {
		t.Errorf("領取期限 = %v，預期從現在重新計算", tk.DeadlineAt)
	}
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusAwaitingAgent {
		t.Errorf("correct = %s", st.Status)
	}
	// 別的 Agent 的 token 不能放回。
	again := claim(t, r, "a2", 1)[0]
	err := r.p.ReleaseAgentTask(ctx, "a1", again.TaskID, again.LeaseToken, "")
	agentErr(t, err, AgentCodeLeaseExpired)
}

// AGT-R4、R7：租約到期後不能提交；sweeper 把任務放回佇列並計入 attempts；被重新領取後舊 token 失效。
// 延長租約從現在起算一個租約期間。
func TestAgentLeaseExpiryAndExtend(t *testing.T) {
	r, _, _ := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	tasks := claim(t, r, "a1", 2)
	keep, lose := tasks[0], tasks[1]
	r.clk.Advance(8 * time.Minute)
	until, err := r.p.ExtendAgentLease(ctx, "a1", keep.TaskID, keep.LeaseToken)
	if err != nil || !until.Equal(r.clk.Now().Add(DefaultAgentLease)) {
		t.Fatalf("ExtendAgentLease = %v, %v", until, err)
	}
	r.clk.Advance(3 * time.Minute)
	// 到期但 sweeper 還沒執行：一樣不能提交，也不能延長。
	agentErr(t, r.p.SubmitAgentTask(ctx, "a1", lose.TaskID, lose.LeaseToken, AgentSubmission{Output: echoOutput(lose, false)}), AgentCodeLeaseExpired)
	if _, err := r.p.ExtendAgentLease(ctx, "a1", lose.TaskID, lose.LeaseToken); err == nil {
		t.Error("到期的租約不應能延長")
	}
	if err := r.p.AgentSweeper().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if tk := taskByID(t, r, lose.TaskID); tk.State != store.TaskPending || tk.Attempts != 1 || tk.ReleaseReason != releaseLeaseExpired {
		t.Errorf("到期的任務 = %+v，預期 pending、attempts 1", tk)
	}
	if tk := taskByID(t, r, keep.TaskID); tk.State != store.TaskLeased || tk.LeaseToken != keep.LeaseToken {
		t.Errorf("延長過的任務 = %+v，預期仍被領取", tk)
	}
	other := claim(t, r, "a2", 10)
	var re AgentTask
	for _, at := range other {
		if at.TaskID == lose.TaskID {
			re = at
		}
	}
	if re.TaskID == "" {
		t.Fatalf("a2 沒有領到放回的任務：%+v", other)
	}
	if tk := taskByID(t, r, lose.TaskID); tk.Attempts != 2 {
		t.Errorf("重新領取後 attempts = %d，預期 2", tk.Attempts)
	}
	agentErr(t, r.p.SubmitAgentTask(ctx, "a1", lose.TaskID, lose.LeaseToken, AgentSubmission{Output: echoOutput(lose, false)}), AgentCodeLeaseExpired)
	if err := r.p.SubmitAgentTask(ctx, "a2", re.TaskID, re.LeaseToken, AgentSubmission{Output: echoOutput(re, false)}); err != nil {
		t.Errorf("新的持有者提交：%v", err)
	}
}

// AGT-R7：同一個 Agent 在租約到期後重新領取同一個任務，舊的 token 一樣不能提交、延長或放回。
func TestAgentOldTokenOfSameAgentIsRejected(t *testing.T) {
	r, _, _ := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	old := claim(t, r, "a1", 1)[0]
	r.clk.Advance(11 * time.Minute)
	if err := r.p.AgentSweeper().Run(ctx); err != nil {
		t.Fatal(err)
	}
	again := claim(t, r, "a1", 1)
	if len(again) != 1 || again[0].TaskID != old.TaskID || again[0].LeaseToken == old.LeaseToken {
		t.Fatalf("重新領取 = %+v", again)
	}
	agentErr(t, r.p.SubmitAgentTask(ctx, "a1", old.TaskID, old.LeaseToken, AgentSubmission{Output: echoOutput(old, false)}), AgentCodeLeaseExpired)
	if _, err := r.p.ExtendAgentLease(ctx, "a1", old.TaskID, old.LeaseToken); err == nil {
		t.Error("舊 token 不應能延長")
	}
	agentErr(t, r.p.ReleaseAgentTask(ctx, "a1", old.TaskID, old.LeaseToken, ""), AgentCodeLeaseExpired)
	if tk := taskByID(t, r, old.TaskID); tk.State != store.TaskLeased || tk.LeaseToken != again[0].LeaseToken {
		t.Errorf("任務 = %+v，預期仍以新的 token 被領取", tk)
	}
	if err := r.p.SubmitAgentTask(ctx, "a1", old.TaskID, again[0].LeaseToken, AgentSubmission{Output: echoOutput(old, false)}); err != nil {
		t.Errorf("新的 token 提交：%v", err)
	}
}

// PIPE-R11 與 AGT-R4：重新啟動時放回別的 worker 留下的租約，但不動 Agent 的租約（它屬於 Agent，不是 worker）。
func TestRecoverStaleKeepsAgentLeases(t *testing.T) {
	r, _, _ := newAgentFlowRig(t, nil, nil)
	at := claim(t, r, "a1", 1)[0]
	if _, err := queue.New(r.st, r.clk, "w2").RecoverStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tk := taskByID(t, r, at.TaskID); tk.State != store.TaskLeased || tk.LeaseToken != at.LeaseToken || tk.Attempts != 1 {
		t.Errorf("Agent 任務 = %+v，預期仍被 a1 領取", tk)
	}
}

// AGT-R9：超過 agent.timeout 沒有被領取時，預設改用 API executor 完成。
func TestAgentTimeoutFallsBackToAPI(t *testing.T) {
	r, it, fake := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	r.clk.Advance(23 * time.Hour)
	if err := r.p.AgentSweeper().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusAwaitingAgent {
		t.Fatalf("23 小時時 correct = %s，預期仍在等待", st.Status)
	}
	r.clk.Advance(2 * time.Hour)
	if at, ok, err := r.p.AgentSweeper().NextAt(ctx); err != nil || !ok || at.After(r.clk.Now()) {
		t.Errorf("NextAt = %v, %v, %v，預期已到期", at, ok, err)
	}
	if err := r.p.AgentSweeper().Run(ctx); err != nil {
		t.Fatal(err)
	}
	r.run()
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusDone || fake.callCount() != 4 {
		t.Fatalf("correct = %+v，provider 呼叫 %d 次", st, fake.callCount())
	}
	art, _ := r.st.Read().Artifact(ctx, st.ArtifactID)
	var prov map[string]any
	_ = json.Unmarshal(art.Provenance, &prov)
	if prov["executor"] != "api" || prov["provider"] != "anthropic" {
		t.Errorf("來源紀錄 = %v", prov)
	}
}

// AGT-R9：on_timeout: notify，或 fallback_api 但沒有設定 provider 與 model 時，步驟 blocked 並提供「改用 API 處理」；只通知一次。
func TestAgentTimeoutNotifies(t *testing.T) {
	for name, c := range map[string]struct{ agent, stage map[string]any }{
		"notify":                 {agent: map[string]any{"on_timeout": "notify", "timeout": "2h"}},
		"沒有 provider 的 fallback": {agent: map[string]any{"timeout": "2h"}, stage: map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			var (
				r    *rig
				it   store.Item
				fake = &chunkLLM{}
			)
			if c.stage != nil {
				r, it = newAgentNoProviderRig(t, c.agent)
			} else {
				r, it, fake = newAgentFlowRig(t, c.agent, nil)
			}
			ctx := context.Background()
			r.clk.Advance(3 * time.Hour)
			if err := r.p.AgentSweeper().Run(ctx); err != nil {
				t.Fatal(err)
			}
			r.run()
			st := latestStage(t, r, it.ID, StageCorrect)
			if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.Message != "這個任務等待 Agent 超過 2 小時。" ||
				len(st.Actions) != 1 || st.Actions[0] != ActionSwitchToAPI {
				t.Fatalf("correct = %+v", st)
			}
			if fake.callCount() != 0 {
				t.Error("不應呼叫 provider")
			}
			if _, ok, _ := r.p.AgentSweeper().NextAt(ctx); ok {
				t.Error("通知之後不應再有期限")
			}
			// 仍可以被 Agent 領取（晚到的 Agent 可以接手）。
			if got := claim(t, r, "a1", 1); len(got) != 1 {
				t.Errorf("blocked 之後領到 %d 個", len(got))
			}
		})
	}
}

// AGT-R11：改用 API 執行時，等待中與已被領取的任務都交給內建 worker，原租約立即失效。
func TestSwitchStageExecutor(t *testing.T) {
	r, it, fake := newAgentFlowRig(t, nil, nil)
	ctx := context.Background()
	at := claim(t, r, "a1", 1)[0]
	res, err := r.p.SwitchStageExecutor(ctx, it.ID, StageCorrect, ExecutorAPI)
	if err != nil || len(res.Tasks) != 4 {
		t.Fatalf("SwitchStageExecutor = %+v, %v", res, err)
	}
	agentErr(t, r.p.SubmitAgentTask(ctx, "a1", at.TaskID, at.LeaseToken, AgentSubmission{Output: echoOutput(at, false)}), AgentCodeLeaseExpired)
	r.run()
	if st := latestStage(t, r, it.ID, StageCorrect); st.Status != store.StatusDone || fake.callCount() != 4 {
		t.Fatalf("correct = %+v，provider 呼叫 %d 次", st, fake.callCount())
	}
	if _, err := r.p.SwitchStageExecutor(ctx, it.ID, StageCorrect, ExecutorAPI); !errors.Is(err, ErrNoAgentTasks) {
		t.Errorf("完成後再切換 = %v，預期 ErrNoAgentTasks", err)
	}
}

// AGT-R11：沒有 provider 與 model 時不能改用 API；單一任務也可以切換。
func TestSwitchTaskExecutor(t *testing.T) {
	r, it := newAgentNoProviderRig(t, nil)
	ctx := context.Background()
	if _, err := r.p.SwitchStageExecutor(ctx, it.ID, StageCorrect, ExecutorAPI); !errors.Is(err, ErrNoAPIConfigured) {
		t.Errorf("沒有 provider 時 = %v", err)
	}
	r2, _, fake := newAgentFlowRig(t, nil, nil)
	at := claim(t, r2, "a1", 1)[0]
	if _, err := r2.p.SwitchTaskExecutor(ctx, at.TaskID, ExecutorAPI); err != nil {
		t.Fatal(err)
	}
	if tk := taskByID(t, r2, at.TaskID); tk.Executor != queue.ExecutorBuiltin || tk.LeaseToken != "" {
		t.Errorf("任務 = %+v", tk)
	}
	r2.run()
	if fake.callCount() != 1 {
		t.Errorf("provider 呼叫 %d 次，預期 1", fake.callCount())
	}
}

// translate 的 agent 還不支援：步驟以 permanent 失敗並說明，不讓 Agent 的結果繞過 TRN 的檢查。
func TestTranslateAgentFailsPermanently(t *testing.T) {
	r := newRig(t, func(d *Deps) { d.Prompts = kindPrompts{} })
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	over := map[string]any{"stages": map[string]any{
		"segment": map[string]any{"executor": "off"}, "correct": map[string]any{"executor": "off"},
		"translate": map[string]any{"executor": "agent", "prompt": "translate-default"},
	}, "language": map[string]any{"targets": []any{"ja"}}}
	it := r.ingest(watch, over, "")
	r.run()
	st := latestStage(t, r, it.ID, StageTranslate)
	if st.Status != store.StatusFailed || st.Message != "translate 還不支援 agent 執行方式，請改用 api。" {
		t.Errorf("translate = %+v", st)
	}
	if n, _ := r.st.Read().CountTasks(context.Background(), store.TaskPending); n != 0 {
		t.Errorf("不應留下等待 Agent 的任務：%d", n)
	}
}

func TestSanitizeAgentText(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
	}{
		{"  claude-opus \n", 100, "claude-opus"},
		{"a\x1b[31mb\u202ec\u2028d", 100, "a[31mbcd"},
		{"零一二三四五", 3, "零一二"},
	}
	for _, c := range cases {
		if got := sanitizeAgentText(c.in, c.limit); got != c.want {
			t.Errorf("sanitize(%q, %d) = %q，預期 %q", c.in, c.limit, got, c.want)
		}
	}
}

// Agent 設定的預設值與檢查。
func TestEffectiveAgent(t *testing.T) {
	as, err := Effective{}.Agent()
	if err != nil || as.MaxClaims != DefaultAgentMaxClaims || as.Lease != DefaultAgentLease || as.Timeout != DefaultAgentTimeout ||
		as.OnTimeout != OnTimeoutFallbackAPI || as.OnValidationFailure != OnValidationRequeue {
		t.Errorf("預設 = %+v, %v", as, err)
	}
	as, err = Effective{"agent": map[string]any{"max_claims": float64(2), "lease": "5m", "timeout": "1d", "on_timeout": "notify", "on_validation_failure": "fallback_api"}}.Agent()
	if err != nil || as.MaxClaims != 2 || as.Lease != 5*time.Minute || as.Timeout != 24*time.Hour || as.OnTimeout != OnTimeoutNotify || as.OnValidationFailure != OnValidationFallbackAPI {
		t.Errorf("設定 = %+v, %v", as, err)
	}
	if _, err := (Effective{"agent": map[string]any{"lease": "soon"}}).Agent(); err == nil {
		t.Error("lease 不是時間長度應失敗")
	}
	if humanDuration(24*time.Hour) != "24 小時" || humanDuration(90*time.Minute) != "90 分鐘" {
		t.Errorf("humanDuration = %q、%q", humanDuration(24*time.Hour), humanDuration(90*time.Minute))
	}
}
