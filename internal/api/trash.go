package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 這個檔案是移到垃圾桶、垃圾桶列表與還原（DEL-R1、R4、R5）。

// KeptOutput 是刪除時保留下來的輸出檔（DEL-R9）。
type KeptOutput struct {
	Path   string `json:"path"`
	Reason string `json:"reason" doc:"modified（內容改過，或不是一般檔案）、outside_roots（路徑不在允許的目錄內）或 unreadable"`
}

// TrashedItem 是垃圾桶中的一部影音。
type TrashedItem struct {
	ID        string    `json:"id"`
	Ref       string    `json:"ref" doc:"影音的短代號，例如 yt:abc123xxxxx"`
	Title     string    `json:"title"`
	DeletedAt time.Time `json:"deleted_at"`
	PurgeAt   time.Time `json:"purge_at" doc:"到期後永久刪除的時間"`
}

func (rt *routes) registerTrash(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-delete", Method: http.MethodDelete, Path: "/api/items/{id}",
		Summary: "刪除影音，移到垃圾桶（DEL-R1）；已在垃圾桶中回 404 in_trash",
	}, auth.ScopeAdmin, rt.trashItem)
	registerScoped(api, huma.Operation{
		OperationID: "trash-list", Method: http.MethodGet, Path: "/api/trash",
		Summary: "列出垃圾桶中的影音（DEL-R4）",
	}, auth.ScopeRead, rt.listTrash)
	registerScoped(api, huma.Operation{
		OperationID: "trash-restore", Method: http.MethodPost, Path: "/api/trash/{id}/restore",
		Summary: "從垃圾桶還原（DEL-R5）；影音不在垃圾桶中回 404",
	}, auth.ScopeAdmin, rt.restoreItem)
}

type trashItemInput struct {
	ID string `path:"id" doc:"影音 ID"`
	// Body 用指標：不帶內容時等同 {}。
	Body *struct {
		DeleteOutputs bool `json:"delete_outputs,omitempty" doc:"同時刪除沒被改過的輸出檔；改過的保留並列在 kept_outputs（DEL-R9）"`
	}
}

type trashItemOutput struct {
	Body struct {
		ID          string       `json:"id"`
		Ref         string       `json:"ref"`
		DeletedAt   time.Time    `json:"deleted_at"`
		PurgeAt     time.Time    `json:"purge_at"`
		KeptOutputs []KeptOutput `json:"kept_outputs"`
	}
}

func (rt *routes) trashItem(ctx context.Context, in *trashItemInput) (*trashItemOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	// 執行中的任務由 worker 結束並等它們結束（PIPE-R15）；沒有 worker 時（nil）只改資料庫。
	opts := pipeline.TrashOptions{}
	if rt.d.Tasks != nil {
		opts.Canceller = rt.d.Tasks
	}
	if in.Body != nil {
		opts.DeleteOutputs = in.Body.DeleteOutputs
	}
	res, err := rt.d.Pipeline.Trash(ctx, in.ID, opts)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &trashItemOutput{}
	out.Body.ID, out.Body.Ref, out.Body.DeletedAt, out.Body.PurgeAt = res.ID, res.Ref, res.DeletedAt, res.PurgeAt
	out.Body.KeptOutputs = make([]KeptOutput, 0, len(res.KeptOutputs))
	for _, k := range res.KeptOutputs {
		out.Body.KeptOutputs = append(out.Body.KeptOutputs, KeptOutput{Path: k.Path, Reason: k.Reason})
	}
	return out, nil
}

type trashListOutput struct {
	Body struct {
		Items []TrashedItem `json:"items"`
		Total int           `json:"total"`
	}
}

func (rt *routes) listTrash(ctx context.Context, _ *struct{}) (*trashListOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	items, err := rt.d.Pipeline.TrashList(ctx)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &trashListOutput{}
	out.Body.Items = make([]TrashedItem, 0, len(items))
	for _, it := range items {
		out.Body.Items = append(out.Body.Items, toTrashedItem(it))
	}
	out.Body.Total = len(out.Body.Items)
	return out, nil
}

func toTrashedItem(it pipeline.TrashedItem) TrashedItem {
	return TrashedItem{ID: it.ID, Ref: it.Ref, Title: it.Title, DeletedAt: it.DeletedAt, PurgeAt: it.PurgeAt}
}

type restoreOutput struct{ Body TrashedItem }

func (rt *routes) restoreItem(ctx context.Context, in *itemInput) (*restoreOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	it, err := rt.d.Pipeline.Restore(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	return &restoreOutput{Body: toTrashedItem(it)}, nil
}
