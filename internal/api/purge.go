package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// 這個檔案是刪除預覽、永久刪除與清空垃圾桶（DEL-R3、R4、R6），由 DEL-B2 實作。
// 骨架先讓 pipeline 回 *NotImplementedError，經 mapErr 回應 501。

// PreviewOutput 是刪除預覽中的一個輸出檔。
type PreviewOutput struct {
	Path     string `json:"path"`
	Modified bool   `json:"modified"`
}

func (rt *routes) registerPurge(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "items-deletion-preview", Method: http.MethodGet, Path: "/api/items/{id}/deletion-preview",
		Summary: "刪除的影響範圍：產出數、可釋放的位元組、已花費的金額與輸出檔；唯讀（DEL-R3）",
	}, rt.deletionPreview)
	huma.Register(api, huma.Operation{
		OperationID: "trash-purge", Method: http.MethodDelete, Path: "/api/trash/{id}",
		Summary: "立即永久刪除垃圾桶中的影音（DEL-R4、R6）",
	}, rt.purgeItem)
	huma.Register(api, huma.Operation{
		OperationID: "trash-empty", Method: http.MethodDelete, Path: "/api/trash",
		Summary: "清空垃圾桶，回傳永久刪除的數量（DEL-R4）",
	}, rt.emptyTrash)
}

type deletionPreviewOutput struct {
	Body struct {
		Artifacts int             `json:"artifacts" doc:"所有版本的產出數"`
		Bytes     int64           `json:"bytes" doc:"產出加上 media、uploads 的大小"`
		SpentUSD  float64         `json:"spent_usd" doc:"這部影音實際花費的總和"`
		Outputs   []PreviewOutput `json:"outputs"`
	}
}

func (rt *routes) deletionPreview(ctx context.Context, in *itemInput) (*deletionPreviewOutput, error) {
	if err := requireOwner(ctx, "查看刪除的影響範圍"); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	p, err := rt.d.Pipeline.DeletionPreview(ctx, in.ID)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &deletionPreviewOutput{}
	out.Body.Artifacts, out.Body.Bytes, out.Body.SpentUSD = p.Artifacts, p.Bytes, p.SpentUSD
	out.Body.Outputs = make([]PreviewOutput, 0, len(p.Outputs))
	for _, o := range p.Outputs {
		out.Body.Outputs = append(out.Body.Outputs, PreviewOutput{Path: o.Path, Modified: o.Modified})
	}
	return out, nil
}

type purgedOutput struct {
	Body struct {
		Purged int `json:"purged" doc:"永久刪除的影音數"`
	}
}

func (rt *routes) purgeItem(ctx context.Context, in *itemInput) (*purgedOutput, error) {
	if err := requireOwner(ctx, "永久刪除影音"); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	if err := rt.d.Pipeline.Purge(ctx, in.ID); err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &purgedOutput{}
	out.Body.Purged = 1
	return out, nil
}

func (rt *routes) emptyTrash(ctx context.Context, _ *struct{}) (*purgedOutput, error) {
	if err := requireOwner(ctx, "清空垃圾桶"); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	n, err := rt.d.Pipeline.PurgeAll(ctx)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &purgedOutput{}
	out.Body.Purged = n
	return out, nil
}
