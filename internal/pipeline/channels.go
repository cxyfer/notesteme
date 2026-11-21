package pipeline

import (
	"context"
	"time"
)

// ChannelOverrides 是頻道層的覆寫（PRF-R8）。ChannelID 為空字串代表頻道還不存在（用 natural key 讀取時）。
type ChannelOverrides struct {
	ChannelID  string
	Platform   string
	ExternalID string
	Overrides  map[string]any
	UpdatedAt  time.Time
}

// 以下是頻道覆寫的 service（M2 S1/S2 設計第 6.3 節）。
// TODO(WP-2)：實作；契約階段回傳 *NotImplementedError，API 層（WP-4）據此回 501。

// ChannelOverrides 讀取頻道覆寫。key 是頻道的 UUID，或 "<platform>:<external_id>"（D14）。
// UUID 找不到回傳 store.ErrNotFound；natural key 找不到時不是錯誤（ChannelID 為空、Overrides 為空物件）。
func (p *Pipeline) ChannelOverrides(ctx context.Context, key string) (ChannelOverrides, error) {
	return ChannelOverrides{}, &NotImplementedError{What: "頻道覆寫"}
}

// PatchChannelOverrides 以 RFC 7396 merge patch 修改頻道覆寫（驗證種類是 KindOverride）。
// natural key 找不到頻道時建立它（title 留空）；platform 只能是 youtube、bilibili、podcast（D14）。
func (p *Pipeline) PatchChannelOverrides(ctx context.Context, key string, patch map[string]any) (ChannelOverrides, error) {
	return ChannelOverrides{}, &NotImplementedError{What: "頻道覆寫"}
}
