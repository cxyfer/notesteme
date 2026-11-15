package pipeline

import (
	"context"
	"time"
)

// 這個檔案是永久刪除、刪除預覽與到期清理（DEL-R3、R4、R6、R7、R11），由 DEL-B2 實作。
// 骨架先回傳 *NotImplementedError（API 對應成 501）。

// PreviewOutput 是刪除預覽中的一個輸出檔（DEL-R3）；由 DEL-B2 擴充。
type PreviewOutput struct {
	Path     string
	Modified bool
}

// Preview 是刪除影音的影響範圍（DEL-R3）；由 DEL-B2 擴充。
type Preview struct {
	// Artifacts 是所有版本的產出數。
	Artifacts int
	// Bytes 是產出加上 media、uploads 的大小。
	Bytes int64
	// SpentUSD 是這部影音實際花費的總和。
	SpentUSD float64
	Outputs  []PreviewOutput
}

// Purge 永久刪除垃圾桶中的影音；影音不在垃圾桶中時回傳 store.ErrNotFound。
func (p *Pipeline) Purge(ctx context.Context, id string) error {
	return &NotImplementedError{What: "永久刪除"}
}

// PurgeAll 清空垃圾桶，回傳永久刪除的影音數。
func (p *Pipeline) PurgeAll(ctx context.Context) (int, error) {
	return 0, &NotImplementedError{What: "清空垃圾桶"}
}

// PurgeExpired 永久刪除 deleted_at 不晚於 now 減去 retention 的影音，回傳永久刪除的影音數。
// now 來自注入的 clock，由 housekeeping 呼叫。
func (p *Pipeline) PurgeExpired(ctx context.Context, now time.Time, retention time.Duration) (int, error) {
	return 0, &NotImplementedError{What: "清理過期的垃圾桶"}
}

// DeletionPreview 回傳刪除影音的影響範圍，唯讀。影音在垃圾桶中時回傳 *InTrashError（DEL-R1）。
func (p *Pipeline) DeletionPreview(ctx context.Context, id string) (Preview, error) {
	return Preview{}, &NotImplementedError{What: "刪除預覽"}
}
