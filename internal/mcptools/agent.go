package mcptools

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// agentTools 是 AGT 的任務 tool（AGT-R2～R8，需要 agent scope，AGT-R12）。
//
// S9 先宣告名稱、scope 與參數，呼叫時回 not_implemented（和 HTTP 尚未實作的路由回 501 相同的做法）；
// S10 實作時只要替換這裡的 handler，tools/list 的過濾與 scope 檢查不用改。
func agentTools() []tool {
	return []tool{
		{ToolSpec{"claim_tasks", ScopeAgent}, addAgentStub[claimArgs]("claim_tasks",
			"領取最多 max 個指定種類的 LLM 任務，每個任務附帶租約與完成它所需的上下文（AGT-R2）。需要 agent scope。")},
		{ToolSpec{"extend_lease", ScopeAgent}, addAgentStub[leaseArgs]("extend_lease",
			"延長任務的租約一個租約期間（AGT-R4）。需要 agent scope。")},
		{ToolSpec{"submit_task", ScopeAgent}, addAgentStub[submitArgs]("submit_task",
			"提交任務的結果，驗證與 API executor 相同；失敗時租約保留，可以修正後重交（AGT-R5）。需要 agent scope。")},
		{ToolSpec{"release_task", ScopeAgent}, addAgentStub[releaseArgs]("release_task",
			"放回任務並記錄原因，不計入嘗試次數（AGT-R8）。需要 agent scope。")},
	}
}

type claimArgs struct {
	Kinds []string `json:"kinds,omitempty" jsonschema:"任務種類，例如 correct、segment、translate"`
	Max   int      `json:"max,omitempty" jsonschema:"最多領取幾個"`
}

type leaseArgs struct {
	TaskID     string `json:"task_id" jsonschema:"任務 ID"`
	LeaseToken string `json:"lease_token" jsonschema:"領取時拿到的 lease_token"`
}

type submitArgs struct {
	TaskID     string `json:"task_id" jsonschema:"任務 ID"`
	LeaseToken string `json:"lease_token" jsonschema:"領取時拿到的 lease_token"`
	Output     any    `json:"output" jsonschema:"符合任務 output schema 的 JSON"`
}

type releaseArgs struct {
	TaskID     string `json:"task_id" jsonschema:"任務 ID"`
	LeaseToken string `json:"lease_token" jsonschema:"領取時拿到的 lease_token"`
	Reason     string `json:"reason,omitempty" jsonschema:"放回的原因"`
}

// addAgentStub 註冊一個還沒實作的 AGT tool。
func addAgentStub[In any](name, desc string) func(s *mcp.Server, d *Deps) {
	return func(s *mcp.Server, _ *Deps) {
		mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc},
			func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, any, error) {
				return errorResult(apiError{Status: http.StatusNotImplemented, Code: "not_implemented", Message: "這個功能尚未實作。"}), nil, nil
			})
	}
}
