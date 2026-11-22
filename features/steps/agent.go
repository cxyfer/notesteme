package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cucumber/godog"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cxyfer/notesteme/features/internal/fakellm"
)

// registerAgent 註冊「Agent 與 MCP」的步驟，句型見 docs/specs/steps.md。
// MCP 的步驟在 mcp.go（S9）；Agent 領取與提交任務的步驟（S10）在這裡，透過 MCP SDK client 呼叫 AGT 的 tool。
func registerAgent(r *Registry, s *Scenario) {
	r.Step(`an agent {string} using the token {string}`, s.anAgentUsingToken)
	r.Step(`the agent {string} claims up to {int} tasks of kind {string}`, s.agentClaims)
	r.Step(`the agent {string} holds {int} tasks`, s.agentHolds)
	r.Step(`the agent {string} submits {string} for its task {int}`, s.agentSubmits)
	r.Step(`the agent {string} submits {string} for all its tasks`, s.agentSubmitsAll)
	r.Step(`no task is held by both {string} and {string}`, s.noTaskHeldByBoth)
	r.Step(`the agent {string} extends the lease of its task {int}`, s.agentExtends)
	r.Step(`the agent {string} releases its task {int} with the reason {string}`, s.agentReleases)
	r.Step(`the submission is accepted`, s.submissionAccepted)
	r.Step(`the submission is rejected with {string}`, s.submissionRejected)
	r.Step(`the MCP client with the token {string} calls {string} with:`, s.mcpCalls)
	r.Step(`the MCP client with the token {string} lists the tools`, s.mcpListsTools)
	r.Step(`the MCP client with the token {string} lists the prompts`, s.mcpListsPrompts)
	r.Step(`the tool result contains:`, s.toolResultContains)
	r.Step(`the tool call fails with {string}`, s.toolCallFailsWith)
	r.Step(`the listed names include {string}`, s.listedNamesInclude)
	r.Step(`the listed names do not include {string}`, s.listedNamesDoNotInclude)
	r.Step(`the description of the tool {string} contains {string}`, s.toolDescriptionContains)
}

// agentState 是一個 Agent 身分：使用的 token，以及它領取到、還沒交出去的任務（依領取順序）。
type agentState struct {
	token string
	tasks []claimedTask
}

// claimedTask 是 claim_tasks 回傳的一個任務中，提交時需要的部分。
type claimedTask struct {
	ID      string           `json:"task_id"`
	Token   string           `json:"lease_token"`
	Kind    string           `json:"kind"`
	Chunk   int              `json:"chunk"`
	Chunks  int              `json:"chunks"`
	Context []map[string]any `json:"context"`
	Cues    []map[string]any `json:"cues"`
}

// agentsState 是場景中的 Agent 與最近一次提交的結果。
type agentsState struct {
	byName map[string]*agentState
	// submit 是最近一次 submit_task 的結果；submitted 表示已經提交過。
	submit    *mcp.CallToolResult
	submitErr error
	submitted bool
}

func (s *Scenario) anAgentUsingToken(name, token string) error {
	if _, ok := s.World.Refs["token:"+token]; !ok {
		return fmt.Errorf("還沒建立名稱為 %q 的 token", token)
	}
	if s.agents.byName == nil {
		s.agents.byName = map[string]*agentState{}
	}
	s.agents.byName[name] = &agentState{token: token}
	return nil
}

func (s *Scenario) agent(name string) (*agentState, error) {
	a, ok := s.agents.byName[name]
	if !ok {
		return nil, fmt.Errorf("還沒建立名稱為 %q 的 Agent", name)
	}
	return a, nil
}

// agentCall 以 Agent 的 token 呼叫 tool。JSON-RPC 層的錯誤回傳 err；tool 的錯誤在 res.IsError。
func (s *Scenario) agentCall(a *agentState, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(s.ctx, mcpCallTimeout)
	defer cancel()
	cs, err := s.mcpSession(ctx, a.token)
	if err != nil {
		return nil, err
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && s.ctx.Err() == nil {
		return nil, fmt.Errorf("MCP tool %s 在 %d 秒內沒有回應", tool, int(mcpCallTimeout.Seconds()))
	}
	return res, err
}

// agentClaims 呼叫 claim_tasks。結果也記成最近一次的 tool 呼叫（`the tool result contains:`、`the tool call fails with` 斷言它）；
// tool 回錯誤時步驟本身不失敗，由之後的斷言判斷。
func (s *Scenario) agentClaims(name string, max int, kind string) error {
	a, err := s.agent(name)
	if err != nil {
		return err
	}
	res, err := s.agentCall(a, "claim_tasks", map[string]any{"kinds": []string{kind}, "max": max})
	s.mcp.result, s.mcp.err, s.mcp.called = res, err, true
	if err != nil || res.IsError {
		return nil
	}
	var out struct {
		Tasks []claimedTask `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(toolText(res)), &out); err != nil {
		return fmt.Errorf("claim_tasks 的結果不是預期的格式：%w（%s）", err, toolText(res))
	}
	a.tasks = append(a.tasks, out.Tasks...)
	return nil
}

// held 回傳 Agent 目前實際持有的任務：資料庫中仍以領取時的 lease_token 被這個 Agent（token 名稱）領取的。
func (s *Scenario) held(a *agentState) ([]claimedTask, error) {
	if err := s.prepareOwner(); err != nil {
		return nil, err
	}
	var out []claimedTask
	for _, t := range a.tasks {
		ok, err := s.World.DBAgentHolds(t.ID, t.Token, a.token)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Scenario) agentHolds(name string, want int) error {
	a, err := s.agent(name)
	if err != nil {
		return err
	}
	if toolNotImplemented(s.mcp.result) {
		return godog.ErrPending
	}
	got, err := s.held(a)
	if err != nil {
		return err
	}
	if len(got) != want {
		return fmt.Errorf("Agent %s 持有 %d 個任務，預期 %d（領取過 %d 個）", name, len(got), want, len(a.tasks))
	}
	return nil
}

// task 回傳 Agent 領取到的第 n 個任務（從 1 起算，依領取順序；已交出去的不算）。
func (a *agentState) task(n int) (claimedTask, error) {
	if n < 1 || n > len(a.tasks) {
		return claimedTask{}, fmt.Errorf("Agent 只領取到 %d 個任務，沒有第 %d 個", len(a.tasks), n)
	}
	return a.tasks[n-1], nil
}

// drop 從 Agent 的任務中拿掉已交出去的任務（提交成功或放回）。
func (a *agentState) drop(id string) {
	a.tasks = slices.DeleteFunc(a.tasks, func(t claimedTask) bool { return t.ID == id })
}

// submitTask 以 fixture 產生輸出並提交（和 fakellm 回答 API executor 的請求用同一份 fixture）。
func (s *Scenario) submitTask(a *agentState, t claimedTask, fixture string) error {
	sub, err := fakellm.RenderSubmission(s.World.FixturesDir, fixture, fakellm.GeneratorInput{
		Stage: t.Kind, HasData: true, Chunk: t.Chunk, Chunks: t.Chunks, Context: t.Context, Cues: t.Cues,
	})
	if err != nil {
		return err
	}
	args := map[string]any{"task_id": t.ID, "lease_token": t.Token}
	if sub.Text != nil {
		args["output"] = *sub.Text
	} else {
		args["output"] = json.RawMessage(sub.Output)
	}
	if sub.Model != "" {
		args["model"] = sub.Model
	}
	res, err := s.agentCall(a, "submit_task", args)
	s.agents.submit, s.agents.submitErr, s.agents.submitted = res, err, true
	if err == nil && !res.IsError {
		a.drop(t.ID)
	}
	return nil
}

func (s *Scenario) agentSubmits(name, fixture string, n int) error {
	a, err := s.agent(name)
	if err != nil {
		return err
	}
	t, err := a.task(n)
	if err != nil {
		return err
	}
	return s.submitTask(a, t, fixture)
}

// agentSubmitsAll 依領取順序提交 Agent 目前持有的每個任務；任何一個沒有被接受時步驟失敗。
func (s *Scenario) agentSubmitsAll(name, fixture string) error {
	a, err := s.agent(name)
	if err != nil {
		return err
	}
	tasks, err := s.held(a)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		if toolNotImplemented(s.mcp.result) {
			return godog.ErrPending
		}
		return fmt.Errorf("Agent %s 沒有持有任何任務", name)
	}
	for _, t := range tasks {
		if err := s.submitTask(a, t, fixture); err != nil {
			return err
		}
		if err := s.submissionAccepted(); err != nil {
			return fmt.Errorf("任務 %s（chunk %d）：%w", t.ID, t.Chunk, err)
		}
	}
	return nil
}

func (s *Scenario) noTaskHeldByBoth(x, y string) error {
	a, err := s.agent(x)
	if err != nil {
		return err
	}
	b, err := s.agent(y)
	if err != nil {
		return err
	}
	if toolNotImplemented(s.mcp.result) {
		return godog.ErrPending
	}
	for _, t := range a.tasks {
		for _, u := range b.tasks {
			if t.ID == u.ID {
				return fmt.Errorf("任務 %s（chunk %d）同時被 %s 與 %s 領取", t.ID, t.Chunk, x, y)
			}
		}
	}
	return nil
}

// agentOp 呼叫 extend_lease 或 release_task；tool 回錯誤時步驟失敗（場景預期這些操作成功）。
func (s *Scenario) agentOp(name string, n int, tool string, extra map[string]any) (claimedTask, error) {
	a, err := s.agent(name)
	if err != nil {
		return claimedTask{}, err
	}
	t, err := a.task(n)
	if err != nil {
		if toolNotImplemented(s.mcp.result) {
			return claimedTask{}, godog.ErrPending
		}
		return claimedTask{}, err
	}
	args := map[string]any{"task_id": t.ID, "lease_token": t.Token}
	for k, v := range extra {
		args[k] = v
	}
	res, err := s.agentCall(a, tool, args)
	if err != nil {
		return t, fmt.Errorf("%s：%w", tool, err)
	}
	if toolNotImplemented(res) {
		return t, godog.ErrPending
	}
	if res.IsError {
		return t, fmt.Errorf("%s 失敗：%s", tool, toolText(res))
	}
	return t, nil
}

func (s *Scenario) agentExtends(name string, n int) error {
	_, err := s.agentOp(name, n, "extend_lease", nil)
	return err
}

func (s *Scenario) agentReleases(name string, n int, reason string) error {
	t, err := s.agentOp(name, n, "release_task", map[string]any{"reason": reason})
	if err == nil {
		s.agents.byName[name].drop(t.ID)
	}
	return err
}

func (s *Scenario) submissionAccepted() error {
	if !s.agents.submitted {
		return errors.New("還沒有提交任何任務")
	}
	if s.agents.submitErr != nil {
		return fmt.Errorf("submit_task 呼叫失敗：%v", s.agents.submitErr)
	}
	if toolNotImplemented(s.agents.submit) {
		return godog.ErrPending
	}
	if s.agents.submit.IsError {
		return fmt.Errorf("提交被拒絕：%s", toolText(s.agents.submit))
	}
	return nil
}

func (s *Scenario) submissionRejected(want string) error {
	if !s.agents.submitted {
		return errors.New("還沒有提交任何任務")
	}
	if toolNotImplemented(s.agents.submit) {
		return godog.ErrPending
	}
	var msg string
	switch {
	case s.agents.submitErr != nil:
		msg = s.agents.submitErr.Error()
	case s.agents.submit.IsError:
		msg = toolText(s.agents.submit)
	default:
		return fmt.Errorf("預期提交被拒絕（含 %q），但被接受了：%s", want, toolText(s.agents.submit))
	}
	if !strings.Contains(msg, want) {
		return fmt.Errorf("提交被拒絕，但錯誤不含 %q：%s", want, msg)
	}
	return nil
}
