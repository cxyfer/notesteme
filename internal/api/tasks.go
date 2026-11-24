package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

type taskInput struct {
	ID string `path:"id" doc:"任務 ID（GET /api/queue 的 tasks[].id 或 failed[].id）"`
}

// registerTaskControl 註冊單一任務的重試與取消（PIPE 介面）。回應是任務所屬影音的最新詳情。
func (rt *routes) registerTaskControl(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "tasks-retry", Method: http.MethodPost, Path: "/api/tasks/{id}/retry",
		Summary: "手動重試任務所屬的步驟（PIPE-R4）：failed、blocked 或已取消的步驟回到 pending，以原任務的優先順序與資源類別建立新任務；其他狀態回 409",
	}, auth.ScopeAdmin, rt.retryTask)
	registerScoped(api, huma.Operation{
		OperationID: "tasks-cancel", Method: http.MethodPost, Path: "/api/tasks/{id}/cancel",
		Summary: "只取消這個任務所屬的步驟（PIPE-R15 的語意）：步驟改為 skipped（cancelled），執行中的子程序整組結束；已取消時也回 200，已結束回 409",
	}, auth.ScopeAdmin, rt.cancelTask)
}

func (rt *routes) retryTask(ctx context.Context, in *taskInput) (*itemDetailOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	itemID, err := rt.d.Pipeline.RetryTask(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return rt.itemDetail(ctx, itemID)
}

// cancelTask 先在資料庫中取消步驟與任務，再讓 worker 結束執行中的任務，等它們結束才回應（同 cancelItem）。
func (rt *routes) cancelTask(ctx context.Context, in *taskInput) (*itemDetailOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	itemID, taskIDs, err := rt.d.Pipeline.CancelTask(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	if rt.d.Tasks != nil && len(taskIDs) > 0 {
		if err := rt.d.Tasks.Cancel(ctx, taskIDs); err != nil {
			return nil, rt.mapErr(ctx, err)
		}
	}
	return rt.itemDetail(ctx, itemID)
}

func (rt *routes) itemDetail(ctx context.Context, itemID string) (*itemDetailOutput, error) {
	d, err := rt.d.Pipeline.Item(ctx, itemID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &itemDetailOutput{Body: toDetail(d)}, nil
}
