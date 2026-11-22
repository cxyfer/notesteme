package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// agentTools 是 AGT 的任務 tool（AGT-R2～R8，需要 agent scope，AGT-R12）。
//
// 這四個 tool 沒有對應的 HTTP 路由（AGT 規格的介面只有 MCP），直接呼叫 Deps.Agent；
// Agent 的名稱是呼叫者 token 的名稱（Deps.AgentName，AGT-R10）。依 /goal 的保守預設，見 MCP 規格待決問題 S3。
func agentTools() []tool {
	return []tool{
		{ToolSpec{"claim_tasks", ScopeAgent}, addClaimTasks},
		{ToolSpec{"extend_lease", ScopeAgent}, addExtendLease},
		{ToolSpec{"submit_task", ScopeAgent}, addSubmitTask},
		{ToolSpec{"release_task", ScopeAgent}, addReleaseTask},
	}
}

// AgentBackend 是 Agent 任務的操作（pipeline 的 ClaimAgentTasks 等），由 internal/api 接上。
// 錯誤實作 AgentFailure 時轉成對應的 tool 錯誤，其他錯誤回 internal_error。
type AgentBackend interface {
	Claim(ctx context.Context, agent string, kinds []string, max int) (any, error)
	Extend(ctx context.Context, agent, taskID, token string) (time.Time, error)
	Submit(ctx context.Context, agent, taskID, token string, output json.RawMessage, modelReported string) error
	Release(ctx context.Context, agent, taskID, token, reason string) error
}

// AgentFailure 是 Agent 操作的預期錯誤（租約失效、驗證失敗…）。
type AgentFailure interface {
	error
	AgentError() (status int, code, reason, message string, returned bool)
}

type claimArgs struct {
	Kinds []string `json:"kinds,omitempty" jsonschema:"任務種類：correct、segment；省略時兩種都領"`
	Max   int      `json:"max,omitempty" jsonschema:"最多領取幾個；省略時用處理方案的 agent.max_claims"`
}

type leaseArgs struct {
	TaskID     string `json:"task_id" jsonschema:"任務 ID"`
	LeaseToken string `json:"lease_token" jsonschema:"領取時拿到的 lease_token"`
}

type submitArgs struct {
	TaskID     string `json:"task_id" jsonschema:"任務 ID"`
	LeaseToken string `json:"lease_token" jsonschema:"領取時拿到的 lease_token"`
	Output     any    `json:"output" jsonschema:"符合任務 output_schema 的 JSON 物件（也可以是內容為 JSON 的字串）"`
	Model      string `json:"model,omitempty" jsonschema:"你使用的模型名稱（只做記錄，不驗證）"`
}

type releaseArgs struct {
	TaskID     string `json:"task_id" jsonschema:"任務 ID"`
	LeaseToken string `json:"lease_token" jsonschema:"領取時拿到的 lease_token"`
	Reason     string `json:"reason,omitempty" jsonschema:"放回的原因"`
}

// agentReady 檢查 Agent 的依賴；沒有接上時回錯誤結果。
func agentReady(d *Deps) *mcp.CallToolResult {
	if d.Agent == nil {
		return errorResult(apiError{Status: http.StatusNotImplemented, Code: "not_implemented", Message: "這個功能尚未實作。"})
	}
	if d.AgentName == "" {
		return errorResult(apiError{Status: http.StatusForbidden, Code: "forbidden", Message: "無法判斷 Agent 的身分。"})
	}
	return nil
}

// agentErrorResult 把 Agent 操作的錯誤轉成 tool 的錯誤結果；文字是「code: message」，驗證失敗時 message 以原因（例如 cue_count）開頭。
func agentErrorResult(err error) *mcp.CallToolResult {
	var f AgentFailure
	if errors.As(err, &f) {
		status, code, reason, msg, returned := f.AgentError()
		res := errorResult(apiError{Status: status, Code: code, Message: msg})
		res.StructuredContent = map[string]any{
			"status": status, "code": code, "message": msg, "reason": reason, "returned": returned,
		}
		return res
	}
	return internalError()
}

func addClaimTasks(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{Name: "claim_tasks", Description: "領取最多 max 個指定種類的 LLM 任務（AGT-R2）。每個任務附帶 lease_token、lease_until，" +
		"以及完成它需要的全部上下文：prompt（system 與 user，和 API 送給模型的內容相同）、context 與 cues（要處理的字幕）、output_schema。" +
		"依 prompt 處理 cues，再用 submit_task 提交符合 output_schema 的 JSON。" + UntrustedNote + "需要 agent scope。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, in claimArgs) (*mcp.CallToolResult, any, error) {
			if r := agentReady(d); r != nil {
				return r, nil, nil
			}
			if in.Max < 0 {
				return invalid("max 必須是正整數。"), nil, nil
			}
			tasks, err := d.Agent.Claim(ctx, d.AgentName, in.Kinds, in.Max)
			if err != nil {
				return agentErrorResult(err), nil, nil
			}
			return jsonResult(map[string]any{"tasks": tasks}), nil, nil
		})
}

func addExtendLease(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{Name: "extend_lease", Description: "延長任務的租約一個租約期間（從現在起算，AGT-R4）。租約已失效時回 lease_expired。需要 agent scope。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, in leaseArgs) (*mcp.CallToolResult, any, error) {
			if r := agentReady(d); r != nil {
				return r, nil, nil
			}
			until, err := d.Agent.Extend(ctx, d.AgentName, in.TaskID, in.LeaseToken)
			if err != nil {
				return agentErrorResult(err), nil, nil
			}
			return jsonResult(map[string]any{"task_id": in.TaskID, "lease_until": until.UTC().Format(time.RFC3339)}), nil, nil
		})
}

func addSubmitTask(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{Name: "submit_task", Description: "提交任務的結果，驗證與 API executor 相同（AGT-R5）。驗證失敗時回 validation_failed 與原因，" +
		"租約保留，可以修正後重交；同一份租約連續 3 次失敗時任務放回佇列（AGT-R6）。model 是你使用的模型（只記錄，不驗證）。需要 agent scope。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, in submitArgs) (*mcp.CallToolResult, any, error) {
			if r := agentReady(d); r != nil {
				return r, nil, nil
			}
			// output 可以是 JSON 物件，或內容是 JSON 的字串（有些 client 只能送字串）；驗證由 Spec.Validate 負責。
			var out []byte
			switch v := in.Output.(type) {
			case nil:
				return invalid("缺少 output。"), nil, nil
			case string:
				out = []byte(v)
			default:
				b, err := json.Marshal(v)
				if err != nil {
					return invalid("output 必須是 JSON 物件。"), nil, nil
				}
				out = b
			}
			if err := d.Agent.Submit(ctx, d.AgentName, in.TaskID, in.LeaseToken, out, in.Model); err != nil {
				return agentErrorResult(err), nil, nil
			}
			return jsonResult(map[string]any{"task_id": in.TaskID, "accepted": true}), nil, nil
		})
}

func addReleaseTask(s *mcp.Server, d *Deps) {
	mcp.AddTool(s, &mcp.Tool{Name: "release_task", Description: "立即放回任務並記錄原因，不計入嘗試次數（AGT-R8）。需要 agent scope。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, in releaseArgs) (*mcp.CallToolResult, any, error) {
			if r := agentReady(d); r != nil {
				return r, nil, nil
			}
			if err := d.Agent.Release(ctx, d.AgentName, in.TaskID, in.LeaseToken, in.Reason); err != nil {
				return agentErrorResult(err), nil, nil
			}
			return jsonResult(map[string]any{"task_id": in.TaskID, "released": true}), nil, nil
		})
}
