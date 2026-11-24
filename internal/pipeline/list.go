package pipeline

import (
	"context"
	"errors"
	"regexp"
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
// CreatedAtMs 是 Unix 毫秒，和資料庫的精度一致。分組時 Group 是上一頁最後一筆的分組鍵（不透明的排序字串）。
type ItemCursor struct {
	Group       string
	CreatedAtMs int64
	ID          string
}

// 分組列表的組（LIB-R4）：ItemPage.Groups 的值。
const (
	// GroupFailed 等是依狀態分組時的組：有步驟失敗、有步驟停住、處理中（有步驟執行中、稍後檢查、等待 Agent，或有未結束的任務）、其他。
	GroupFailed     = "failed"
	GroupBlocked    = "blocked"
	GroupProcessing = "processing"
	GroupIdle       = "idle"
)

// statusGroups 把 SQL 的分組鍵（排序用的 1～4）對應到組名。
var statusGroups = map[string]string{"1": GroupFailed, "2": GroupBlocked, "3": GroupProcessing, "4": GroupIdle}

// blockedPattern 是 blocked 篩選接受的格式：停住的原因是程式內的代碼（block_reason）。
var blockedPattern = regexp.MustCompile(`^[a-z_]{1,64}$`)

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
	// Blocked 是停住的原因（例如 awaiting_user、budget_blocked、needs_action）：至少有一個步驟的最新執行是 blocked
	// 且原因是這個值。只檢查格式（blockedPattern），不列舉原因。
	Blocked string
	// Status 的 key 是步驟，值是可接受的狀態：同一個步驟的多個值是 OR，不同步驟之間是 AND。
	// 步驟取最新的一次執行；沒有任何執行紀錄的步驟視為 pending。
	Status        map[string][]string
	PublishedFrom *time.Time
	PublishedTo   *time.Time
	DurationMin   *int
	DurationMax   *int
	// ReadState 是 read 或 unread（LIB-R19）；空字串代表不篩選，其他值 422。
	ReadState string
	// GroupBy 是 channel、published 或 status（LIB-R4）；空字串代表不分組（依建立時間由新到舊），其他值 422。
	GroupBy string
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
	// Groups 與 Items 一一對應，是每部影音所屬的組（只在分組時有值）：
	// channel 是頻道 ID（沒有頻道是空字串）；published 是發布日（UTC 的 YYYY-MM-DD，沒有發布時間是空字串）；
	// status 是 GroupFailed、GroupBlocked、GroupProcessing 或 GroupIdle。
	Groups []string
	// ChannelNames 是這一頁出現的頻道 ID 對應的名稱（頻道還沒有名稱時不列）。
	ChannelNames map[string]string
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
	switch q.GroupBy {
	case "", store.GroupChannel, store.GroupPublished, store.GroupStatus:
	default:
		return invalidf("group_by 必須是 channel、published 或 status。")
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
	if q.Blocked != "" && !blockedPattern.MatchString(q.Blocked) {
		return invalidf("blocked 必須是停住的原因代碼（小寫英文字母與底線，最多 64 個字元），例如 awaiting_user。")
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
		Platform: q.Platform, Channel: q.Channel, ErrorClass: q.ErrorClass, Lang: q.Lang, Timing: q.Timing, Q: q.Q, Blocked: q.Blocked,
		Status: q.Status, PublishedFrom: q.PublishedFrom, PublishedTo: q.PublishedTo,
		DurationMin: q.DurationMin, DurationMax: q.DurationMax, ReadState: q.ReadState,
	}
}

// ListItems 列出符合條件的影音（不含垃圾桶中的）：不分組時依 (created_at, id) 由新到舊；
// 分組時（GroupBy）組依頻道名稱、發布時間（新到舊）或狀態排序，同一組連續、組內依 (created_at, id) 由新到舊，
// 游標記著分組鍵，所以一組跨頁時下一頁從同一組接著列（LIB-R4）。Total 用同一組篩選條件（不含游標）另外計算。
func (p *Pipeline) ListItems(ctx context.Context, q ItemQuery) (ItemPage, error) {
	if err := q.Validate(); err != nil {
		return ItemPage{}, err
	}
	limit := q.Limit
	if limit < 1 {
		limit = DefaultItemLimit
	}
	limit = min(limit, MaxItemLimit)
	r := p.st.Read()
	f := q.filter()
	var page ItemPage
	var err error
	if q.GroupBy == "" {
		page, err = listItemsFlat(ctx, r, f, q.After, limit)
	} else {
		page, err = listItemsGrouped(ctx, r, f, q.GroupBy, q.After, limit)
	}
	if err != nil {
		return ItemPage{}, err
	}
	if page.Total, err = r.CountItems(ctx, f); err != nil {
		return ItemPage{}, err
	}
	if page.ChannelNames, err = channelNames(ctx, r, page.Items); err != nil {
		return ItemPage{}, err
	}
	return page, nil
}

func listItemsFlat(ctx context.Context, r *store.Repo, f store.ItemFilter, after *ItemCursor, limit int) (ItemPage, error) {
	var pos *store.ItemPos
	if after != nil {
		pos = &store.ItemPos{CreatedAtMs: after.CreatedAtMs, ID: after.ID}
	}
	// 多取一筆來判斷有沒有下一頁。
	items, err := r.ListItemsPage(ctx, f, pos, limit+1)
	if err != nil {
		return ItemPage{}, err
	}
	var page ItemPage
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		page.Next = &ItemCursor{CreatedAtMs: last.CreatedAt.UnixMilli(), ID: last.ID}
	}
	page.Items = items
	return page, nil
}

func listItemsGrouped(ctx context.Context, r *store.Repo, f store.ItemFilter, groupBy string, after *ItemCursor, limit int) (ItemPage, error) {
	var pos *store.GroupedPos
	if after != nil {
		pos = &store.GroupedPos{Group: after.Group, CreatedAtMs: after.CreatedAtMs, ID: after.ID}
	}
	keys, err := r.ListItemKeysGrouped(ctx, f, groupBy, pos, limit+1)
	if err != nil {
		return ItemPage{}, err
	}
	var page ItemPage
	if len(keys) > limit {
		keys = keys[:limit]
		last := keys[limit-1]
		page.Next = &ItemCursor{Group: last.Group, CreatedAtMs: last.CreatedAtMs, ID: last.ID}
	}
	page.Items = make([]store.Item, 0, len(keys))
	page.Groups = make([]string, 0, len(keys))
	for _, k := range keys {
		it, err := r.ItemByID(ctx, k.ID)
		if err != nil {
			return ItemPage{}, err
		}
		page.Items = append(page.Items, it)
		page.Groups = append(page.Groups, groupName(groupBy, k.Group, it))
	}
	return page, nil
}

// groupName 是影音所屬的組（ItemPage.Groups 的說明）。
func groupName(groupBy, key string, it store.Item) string {
	switch groupBy {
	case store.GroupChannel:
		return it.ChannelID
	case store.GroupPublished:
		if it.PublishedAt == nil {
			return ""
		}
		return it.PublishedAt.UTC().Format(time.DateOnly)
	}
	return statusGroups[key]
}

// channelNames 讀出這一頁出現的頻道名稱。
func channelNames(ctx context.Context, r *store.Repo, items []store.Item) (map[string]string, error) {
	out := map[string]string{}
	for _, it := range items {
		if it.ChannelID == "" {
			continue
		}
		if _, ok := out[it.ChannelID]; ok {
			continue
		}
		ch, err := r.ChannelByID(ctx, it.ChannelID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[it.ChannelID] = ch.Title
	}
	for id, name := range out {
		if name == "" {
			delete(out, id)
		}
	}
	return out, nil
}
