package api

import (
	"context"
	"net/http"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/danielgtaylor/huma/v2"
)

// TaskCanceller 結束正在執行的任務並等它們結束（*worker.Worker 實作它）。
type TaskCanceller interface {
	Cancel(ctx context.Context, taskIDs []string) error
}

func (rt *routes) registerCancel(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "items-cancel", Method: http.MethodPost, Path: "/api/items/{id}/cancel",
		Summary: "取消影音所有未結束的步驟，執行中的子程序整組結束（PIPE-R15）；沒有未結束的步驟時也回 200",
	}, rt.cancelItem)
}

// cancelItem 先在資料庫中取消步驟與任務，再讓 worker 結束執行中的任務，等它們結束才回應。
func (rt *routes) cancelItem(ctx context.Context, in *itemInput) (*itemDetailOutput, error) {
	if err := requireScope(ctx, auth.ScopeIngest); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	taskIDs, err := rt.d.Pipeline.CancelItem(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	if rt.d.Tasks != nil && len(taskIDs) > 0 {
		if err := rt.d.Tasks.Cancel(ctx, taskIDs); err != nil {
			return nil, rt.mapErr(ctx, err)
		}
	}
	d, err := rt.d.Pipeline.Item(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &itemDetailOutput{Body: toDetail(d)}, nil
}
