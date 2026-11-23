package pipeline

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// 列表單頁筆數（LIB-R4）。
const (
	DefaultItemLimit = 50
	MaxItemLimit     = 200
	// MaxQueryLen 是標題搜尋字串的長度上限（字元數）。
	MaxQueryLen = 200
)

// ItemCursor 是列表中的位置：排序是 (created_at, id) 由新到舊，游標是上一頁最後一筆。
// CreatedAtMs 是 Unix 毫秒，和資料庫的精度一致。
type ItemCursor struct {
	CreatedAtMs int64
	ID          string
}

// ItemQuery 是影音列表的條件（LIB-R1、R3、R4、R7）。空字串與 nil 代表不篩選；多個條件之間是 AND。
type ItemQuery struct {
	Platform   string
	Channel    string
	ErrorClass string
	// Lang 是原文語言。
	Lang string
	// Timing 是 timing origin：目前的 transcript 產出中至少有一個 cue 是這個值。
	Timing string
	// Q 是標題的部分字串，不分大小寫；呼叫端先去掉前後空白。
	Q string
	// Status 的 key 是步驟，值是可接受的狀態：同一個步驟的多個值是 OR，不同步驟之間是 AND。
	// 步驟取最新的一次執行；沒有任何執行紀錄的步驟視為 pending。
	Status        map[string][]string
	PublishedFrom *time.Time
	PublishedTo   *time.Time
	DurationMin   *int
	DurationMax   *int
	// ReadState 是 read 或 unread（LIB-R19）；空字串代表不篩選，其他值 422。
	ReadState string
	// Limit 小於 1 時用 DefaultItemLimit，超過 MaxItemLimit 時用 MaxItemLimit。
	Limit int
	// After 不為 nil 時，只回傳排在它之後的影音。
	After *ItemCursor
}

// ItemPage 是列表的一頁。
type ItemPage struct {
	Items []store.Item
	// Total 是符合條件的總數，和分頁無關。
	Total int
	// Next 是下一頁的起點；最後一頁為 nil。
	Next *ItemCursor
}

// 步驟狀態的完整清單（store.Status*）。
var itemStatuses = []string{
	store.StatusPending, store.StatusRunning, store.StatusWaiting, store.StatusAwaitingAgent,
	store.StatusBlocked, store.StatusDone, store.StatusSkipped, store.StatusFailed,
}

// ValidStatus 回報 s 是不是已知的步驟狀態。
func ValidStatus(s string) bool { return slices.Contains(itemStatuses, s) }

// Validate 檢查條件；不合法時回傳 *InvalidError。
func (q ItemQuery) Validate() error {
	if q.ReadState != "" && q.ReadState != store.ReadRead && q.ReadState != store.ReadUnread {
		return invalidf("read_state 必須是 read 或 unread。")
	}
	switch q.Platform {
	case "", source.PlatformYouTube, source.PlatformBilibili, source.PlatformPodcast, source.PlatformLocal:
	default:
		return invalidf("platform 必須是 youtube、bilibili、podcast 或 local。")
	}
	for stage, vals := range q.Status {
		if !ValidStage(stage) {
			return invalidf("status 的步驟必須是 %s 其中之一，收到 %q。", strings.Join(Stages, "、"), stage)
		}
		for _, v := range vals {
			if !ValidStatus(v) {
				return invalidf("status 的狀態必須是 %s 其中之一，收到 %q。", strings.Join(itemStatuses, "、"), v)
			}
		}
	}
	if q.Timing != "" && !subtitle.TimingOrigin(q.Timing).Valid() {
		return invalidf("timing 必須是 source、merged、silence、interpolated 或 model。")
	}
	if q.DurationMin != nil && *q.DurationMin < 0 || q.DurationMax != nil && *q.DurationMax < 0 {
		return invalidf("duration_min、duration_max 不得小於 0。")
	}
	if n := len([]rune(q.Q)); n > MaxQueryLen {
		return invalidf("q 最多 %d 個字元。", MaxQueryLen)
	}
	return nil
}

func (q ItemQuery) filter() store.ItemFilter {
	return store.ItemFilter{
		Platform: q.Platform, Channel: q.Channel, ErrorClass: q.ErrorClass, Lang: q.Lang, Timing: q.Timing, Q: q.Q,
		Status: q.Status, PublishedFrom: q.PublishedFrom, PublishedTo: q.PublishedTo,
		DurationMin: q.DurationMin, DurationMax: q.DurationMax, ReadState: q.ReadState,
	}
}

// ListItems 依 (created_at, id) 由新到舊列出符合條件的影音（不含垃圾桶中的）。
// Total 用同一組篩選條件（不含游標）另外計算。
func (p *Pipeline) ListItems(ctx context.Context, q ItemQuery) (ItemPage, error) {
	if err := q.Validate(); err != nil {
		return ItemPage{}, err
	}
	limit := q.Limit
	if limit < 1 {
		limit = DefaultItemLimit
	}
	limit = min(limit, MaxItemLimit)
	var after *store.ItemPos
	if q.After != nil {
		after = &store.ItemPos{CreatedAtMs: q.After.CreatedAtMs, ID: q.After.ID}
	}
	r := p.st.Read()
	f := q.filter()
	// 多取一筆來判斷有沒有下一頁。
	items, err := r.ListItemsPage(ctx, f, after, limit+1)
	if err != nil {
		return ItemPage{}, err
	}
	total, err := r.CountItems(ctx, f)
	if err != nil {
		return ItemPage{}, err
	}
	page := ItemPage{Total: total}
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		page.Next = &ItemCursor{CreatedAtMs: last.CreatedAt.UnixMilli(), ID: last.ID}
	}
	page.Items = items
	return page, nil
}
