package store

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ItemFilter 是影音列表的篩選條件（LIB-R1）；零值的欄位代表不篩選。多個條件之間是 AND。
type ItemFilter struct {
	Platform   string
	Channel    string
	ErrorClass string
	// Lang 是原文語言，不分大小寫。
	Lang string
	// Timing 要求目前的 transcript 產出中至少有一個 cue 是這個 timing origin（LIB-R7）。
	Timing string
	// Q 是標題的部分字串，不分大小寫（LIB-R3）。
	Q string
	// Blocked 要求至少有一個步驟的最新執行是 blocked 且原因是這個值（例如 awaiting_user，DIG-R6）。
	Blocked string
	// Status 的 key 是步驟，值是可接受的狀態：同一個步驟的多個值是 OR，不同步驟之間是 AND；步驟的狀態取最新的一次執行。
	Status        map[string][]string
	PublishedFrom *time.Time
	PublishedTo   *time.Time
	DurationMin   *int
	DurationMax   *int
	// ReadState 是 ReadRead 或 ReadUnread（LIB-R19）；空字串代表不篩選。
	ReadState string
}

// ItemPos 是列表中的位置：排序是 (created_at, id) 由大到小，游標是最後一筆的位置。
type ItemPos struct {
	CreatedAtMs int64
	ID          string
}

// statusFilter 是 SQL 參數 status_filters 的一個元素。
type statusFilter struct {
	Stage string `json:"stage"`
	// Statuses 是 "|a|b|" 的格式（狀態名稱不含 |），SQL 用 instr 比對最新狀態，避免每列展開一次 json_each。
	Statuses string `json:"statuses"`
}

// statusFiltersJSON 把 Status 轉成 JSON 陣列；依步驟排序，讓同樣的條件得到同樣的參數。
func statusFiltersJSON(status map[string][]string) (string, error) {
	fs := make([]statusFilter, 0, len(status))
	for stage, vals := range status {
		fs = append(fs, statusFilter{Stage: stage, Statuses: "|" + strings.Join(vals, "|") + "|"})
	}
	slices.SortFunc(fs, func(a, b statusFilter) int { return strings.Compare(a.Stage, b.Stage) })
	b, err := json.Marshal(fs)
	return string(b), err
}

func intPtr64(p *int) *int64 {
	if p == nil {
		return nil
	}
	v := int64(*p)
	return &v
}

// ListItemsPage 依 (created_at, id) 由新到舊列出符合條件的影音（不含垃圾桶中的），最多 limit 筆；
// after 不為 nil 時只回傳排在它之後的（keyset 分頁）。
func (r *Repo) ListItemsPage(ctx context.Context, f ItemFilter, after *ItemPos, limit int) ([]Item, error) {
	status, err := statusFiltersJSON(f.Status)
	if err != nil {
		return nil, err
	}
	afterMs, afterID := int64(math.MaxInt64), ""
	if after != nil {
		afterMs, afterID = after.CreatedAtMs, after.ID
	}
	rows, err := r.q.ListItemsPage(ctx, sqlcdb.ListItemsPageParams{
		Platform: f.Platform, ChannelID: f.Channel, Lang: f.Lang,
		PublishedFrom: msPtr(f.PublishedFrom), PublishedTo: msPtr(f.PublishedTo),
		DurationMin: intPtr64(f.DurationMin), DurationMax: intPtr64(f.DurationMax),
		QFold: FoldTitle(f.Q), ErrorClass: f.ErrorClass, Timing: f.Timing, Blocked: f.Blocked, StatusFilters: status, ReadState: f.ReadState,
		AfterCreatedAt: afterMs, AfterID: afterID, PageSize: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		out = append(out, itemFromRow(row))
	}
	return out, nil
}

// CountItems 回傳符合條件的影音總數（不含垃圾桶中的）；和分頁無關。
func (r *Repo) CountItems(ctx context.Context, f ItemFilter) (int, error) {
	status, err := statusFiltersJSON(f.Status)
	if err != nil {
		return 0, err
	}
	n, err := r.q.CountItemsFiltered(ctx, sqlcdb.CountItemsFilteredParams{
		Platform: f.Platform, ChannelID: f.Channel, Lang: f.Lang,
		PublishedFrom: msPtr(f.PublishedFrom), PublishedTo: msPtr(f.PublishedTo),
		DurationMin: intPtr64(f.DurationMin), DurationMax: intPtr64(f.DurationMax),
		QFold: FoldTitle(f.Q), ErrorClass: f.ErrorClass, Timing: f.Timing, Blocked: f.Blocked, StatusFilters: status, ReadState: f.ReadState,
	})
	return int(n), err
}
