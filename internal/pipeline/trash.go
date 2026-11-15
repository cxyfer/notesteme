package pipeline

import (
	"context"
	"time"
)

// 這個檔案是移到垃圾桶與還原（DEL-R1、R2、R4、R5），由 DEL-B1 實作。
// 骨架先回傳 *NotImplementedError（API 對應成 501）。

// TrashOptions 是移到垃圾桶的選項；由 DEL-B1 擴充。
type TrashOptions struct {
	// DeleteOutputs 在 M1 接受但沒有作用，因為還沒有輸出目標（DEL-R9）。
	DeleteOutputs bool
}

// KeptOutput 是刪除時保留下來的輸出檔（DEL-R9）；由 DEL-B1 擴充。
type KeptOutput struct {
	Path   string
	Reason string
}

// TrashResult 是移到垃圾桶的結果；由 DEL-B1 擴充。
type TrashResult struct {
	ID        string
	Ref       string
	DeletedAt time.Time
	// PurgeAt 是到期後永久刪除的時間（deleted_at 加保留天數）。
	PurgeAt     time.Time
	KeptOutputs []KeptOutput
}

// TrashedItem 是垃圾桶中的一部影音；由 DEL-B1 擴充。
type TrashedItem struct {
	ID        string
	Ref       string
	Title     string
	DeletedAt time.Time
	PurgeAt   time.Time
}

// Trash 把影音移到垃圾桶。已經在垃圾桶中時回傳 *InTrashError；影音不存在時回傳 store.ErrNotFound。
func (p *Pipeline) Trash(ctx context.Context, id string, opts TrashOptions) (TrashResult, error) {
	return TrashResult{}, &NotImplementedError{What: "移到垃圾桶"}
}

// Restore 從垃圾桶還原影音；影音不在垃圾桶中時回傳 store.ErrNotFound（DEL-R5）。
func (p *Pipeline) Restore(ctx context.Context, id string) (TrashedItem, error) {
	return TrashedItem{}, &NotImplementedError{What: "從垃圾桶還原"}
}

// TrashList 列出垃圾桶中的影音，刪除時間由新到舊（DEL-R4）。
func (p *Pipeline) TrashList(ctx context.Context) ([]TrashedItem, error) {
	return nil, &NotImplementedError{What: "列出垃圾桶"}
}
