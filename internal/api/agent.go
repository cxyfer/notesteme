package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

// AGT：改用 API 執行（AGT-R11）的路由，以及 MCP 的任務 tool 用的 Agent 介面。

// CodeNoAgentTasks 是步驟或任務沒有可以改用 API 執行的 Agent 任務（409）；CodeNoAPIConfigured 是處理方案沒有設定 provider 與 model。
const (
	CodeNoAgentTasks    = "no_agent_tasks"
	CodeNoAPIConfigured = "api_not_configured"
)

type switchItemInput struct {
	ID   string `path:"id" doc:"影音 ID 或代號"`
	Body struct {
		Stage    string `json:"stage" minLength:"1" doc:"要切換的步驟（correct、segment）"`
		Executor string `json:"executor" enum:"api" doc:"改用的執行方式；目前只有 api"`
	}
}

type switchTaskInput struct {
	ID   string `path:"id" doc:"任務 ID"`
	Body struct {
		Executor string `json:"executor" enum:"api" doc:"改用的執行方式；目前只有 api"`
	}
}

type switchOutput struct{ Body pipeline.SwitchResult }

func (rt *routes) registerAgent(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-switch-executor", Method: http.MethodPost, Path: "/api/items/{id}/switch-executor",
		Summary: "把影音某個步驟等待中或已被領取的 Agent 任務改用 API 執行，原本的租約立即失效（AGT-R11）",
	}, auth.ScopeIngest, rt.switchItemExecutor)
	registerScoped(api, huma.Operation{
		OperationID: "tasks-switch-executor", Method: http.MethodPost, Path: "/api/tasks/{id}/switch-executor",
		Summary: "把一個等待中或已被領取的 Agent 任務改用 API 執行，原本的租約立即失效（AGT-R11）",
	}, auth.ScopeIngest, rt.switchTaskExecutor)
}

func (rt *routes) switchItemExecutor(ctx context.Context, in *switchItemInput) (*switchOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	res, err := rt.d.Pipeline.SwitchStageExecutor(ctx, in.ID, in.Body.Stage, in.Body.Executor)
	if err != nil {
		return nil, rt.switchErr(ctx, err)
	}
	return &switchOutput{Body: res}, nil
}

func (rt *routes) switchTaskExecutor(ctx context.Context, in *switchTaskInput) (*switchOutput, error) {
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	res, err := rt.d.Pipeline.SwitchTaskExecutor(ctx, in.ID, in.Body.Executor)
	if err != nil {
		return nil, rt.switchErr(ctx, err)
	}
	return &switchOutput{Body: res}, nil
}

func (rt *routes) switchErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, pipeline.ErrNoAgentTasks):
		return newError(http.StatusConflict, CodeNoAgentTasks, err.Error())
	case errors.Is(err, pipeline.ErrNoAPIConfigured):
		return newError(http.StatusConflict, CodeNoAPIConfigured, err.Error())
	}
	return rt.mapErr(ctx, err)
}

// agentBackend 把 mcptools 的 Agent 介面接到 pipeline（AGT-R2～R8）。pipeline 的使用者錯誤轉成 *pipeline.AgentError，
// 其他錯誤原樣回傳（MCP 回 internal_error，細節只進 log）。
type agentBackend struct {
	p   *pipeline.Pipeline
	log func(msg string, err error)
}

func (a agentBackend) wrap(err error) error {
	if err == nil {
		return nil
	}
	var ae *pipeline.AgentError
	var inv *pipeline.InvalidError
	switch {
	case errors.As(err, &ae):
		return ae
	case errors.As(err, &inv):
		return &pipeline.AgentError{Status: http.StatusUnprocessableEntity, Code: pipeline.AgentCodeInvalid, Message: inv.Error()}
	case errors.Is(err, store.ErrNotFound):
		return &pipeline.AgentError{Status: http.StatusNotFound, Code: pipeline.AgentCodeNotFound, Message: "找不到這個任務。"}
	}
	if a.log != nil {
		a.log("Agent 任務操作失敗", err)
	}
	return err
}

func (a agentBackend) Claim(ctx context.Context, agent string, kinds []string, max int) (any, error) {
	tasks, err := a.p.ClaimAgentTasks(ctx, agent, kinds, max)
	if err != nil {
		return nil, a.wrap(err)
	}
	return tasks, nil
}

func (a agentBackend) Extend(ctx context.Context, agent, taskID, token string) (time.Time, error) {
	until, err := a.p.ExtendAgentLease(ctx, agent, taskID, token)
	return until, a.wrap(err)
}

func (a agentBackend) Submit(ctx context.Context, agent, taskID, token string, output json.RawMessage, model string) error {
	return a.wrap(a.p.SubmitAgentTask(ctx, agent, taskID, token, pipeline.AgentSubmission{Output: output, ModelReported: model}))
}

func (a agentBackend) Release(ctx context.Context, agent, taskID, token, reason string) error {
	return a.wrap(a.p.ReleaseAgentTask(ctx, agent, taskID, token, reason))
}

// agentName 是呼叫者作為 Agent 的名稱：token 的名稱；session 與本機（owner）是 "owner"。
func agentName(p auth.Principal) string {
	if p.Kind == "token" {
		return p.TokenName
	}
	return "owner"
}
