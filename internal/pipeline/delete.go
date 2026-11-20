package pipeline

import (
	"errors"
	"path/filepath"
)

// ErrInTrash 表示影音在垃圾桶中（DEL-R1、R10）：讀取與處理它的操作回 404，新增它回 409，錯誤碼都是 in_trash。
// 需要知道是哪部影音時用 *InTrashError，它與 ErrInTrash 相等（errors.Is）。
var ErrInTrash = errors.New("這部影音在垃圾桶中。")

// InTrashError 是帶著影音 ID 的 ErrInTrash，API 把 ID 放進回應的 item_id。
type InTrashError struct{ ItemID string }

func (e *InTrashError) Error() string        { return ErrInTrash.Error() }
func (e *InTrashError) Is(target error) bool { return target == ErrInTrash }

// MediaCacheDir 是平台影音的媒體快取目錄：<資料目錄>/media/<影音 ID>（DEL-R2）。
// 刪除時只能用資料庫中、已確認是標準形式 UUID 的影音 ID 組出路徑。
func MediaCacheDir(dataDir, itemID string) string {
	return filepath.Join(dataDir, "media", itemID)
}
