package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/pipeline"
)

// 這個檔案是刪除預覽、永久刪除與清空垃圾桶（DEL-R3、R4、R6），由 DEL-B2 實作。
// 骨架先讓 pipeline 回 *NotImplementedError，經 mapErr 回應 501。

// PreviewOutput 是刪除預覽中的一個輸出檔：指定 delete_outputs 時，modified 為 false 的會被刪除，為 true 的保留。
// 已經不存在的檔案不列。
type PreviewOutput struct {
	Path     string `json:"path"`
	Modified bool   `json:"modified" doc:"內容與寫入時的紀錄不同（或不是一般檔案、不在允許的目錄內）：不會被刪除"`
	Reason   string `json:"reason,omitempty" doc:"保留的原因：modified、outside_roots 或 unreadable；會被刪除的沒有"`
}

func (rt *routes) registerPurge(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "items-deletion-preview", Method: http.MethodGet, Path: "/api/items/{id}/deletion-preview",
		Summary: "刪除的影響範圍：產出數、可釋放的位元組、已花費的金額與輸出檔；唯讀（DEL-R3）",
	}, auth.ScopeRead, rt.deletionPreview)
	registerScoped(api, huma.Operation{
		OperationID: "trash-purge", Method: http.MethodDelete, Path: "/api/trash/{id}",
		Summary: "立即永久刪除垃圾桶中的影音（DEL-R4、R6）",
	}, auth.ScopeAdmin, rt.purgeItem)
	registerScoped(api, huma.Operation{
		OperationID: "trash-empty", Method: http.MethodDelete, Path: "/api/trash",
		Summary: "清空垃圾桶，回傳永久刪除的數量（DEL-R4）",
	}, auth.ScopeAdmin, rt.emptyTrash)
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
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
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
		out.Body.Outputs = append(out.Body.Outputs, PreviewOutput{Path: o.Path, Modified: o.Modified, Reason: o.Reason})
	}
	return out, nil
}

type purgedOutput struct {
	Body struct {
		Purged      int          `json:"purged" doc:"永久刪除的影音數"`
		KeptOutputs []KeptOutput `json:"kept_outputs,omitempty" doc:"指定 delete_outputs 時保留下來的輸出檔（DEL-R9）；沒有保留任何檔案時省略"`
	}
}

// purgeBody 是永久刪除可帶的選項；不帶 body 等同 {}。
type purgeBody struct {
	DeleteOutputs bool `json:"delete_outputs,omitempty" doc:"同時刪除沒被改過的輸出檔；改過的保留並列在 kept_outputs（DEL-R9）"`
}

func keptOutputs(list []pipeline.KeptOutput) []KeptOutput {
	out := make([]KeptOutput, 0, len(list))
	for _, k := range list {
		out = append(out, KeptOutput{Path: k.Path, Reason: k.Reason})
	}
	return out
}

type purgeItemInput struct {
	ID   string `path:"id" doc:"影音 ID"`
	Body *purgeBody
}

type emptyTrashInput struct {
	Body *purgeBody
}

// purgedItemOutput 是永久刪除單一影音的回應：影音已經不存在，所以帶出它的 ID 與代號（CLI 輸出代號）。
type purgedItemOutput struct {
	Body struct {
		Purged int    `json:"purged" doc:"永久刪除的影音數，一律是 1"`
		ID     string `json:"id" doc:"被刪除的影音的內部 ID"`
		Ref    string `json:"ref" doc:"被刪除的影音的短代號，例如 yt:abc123xxxxx"`
		// KeptOutputs 是指定 delete_outputs 時保留下來的輸出檔（DEL-R9）。
		KeptOutputs []KeptOutput `json:"kept_outputs,omitempty"`
	}
}

func (rt *routes) purgeItem(ctx context.Context, in *purgeItemInput) (*purgedItemOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	var opts pipeline.PurgeOptions
	if in.Body != nil {
		opts.DeleteOutputs = in.Body.DeleteOutputs
	}
	done, err := rt.d.Pipeline.PurgeItemWith(ctx, in.ID, opts)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}
	out := &purgedItemOutput{}
	out.Body.Purged, out.Body.ID, out.Body.Ref = 1, done.ID, done.Ref
	out.Body.KeptOutputs = keptOutputs(done.KeptOutputs)
	return out, nil
}

func (rt *routes) emptyTrash(ctx context.Context, in *emptyTrashInput) (*purgedOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	var opts pipeline.PurgeOptions
	if in.Body != nil {
		opts.DeleteOutputs = in.Body.DeleteOutputs
	}
	n, kept, err := rt.d.Pipeline.PurgeAllWith(ctx, opts)
	if err != nil {
		// 部分失敗：已經刪除的不會復原，回應照錯誤格式（client 以非 2xx 判斷失敗），並帶 purged 告知已刪幾部。
		e := asError(rt.mapErr(ctx, err))
		e.Purged = &n
		return nil, e
	}
	out := &purgedOutput{}
	out.Body.Purged = n
	out.Body.KeptOutputs = keptOutputs(kept)
	return out, nil
}
