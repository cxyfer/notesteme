package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/cost"
)

// 這個檔案是總覽 GET /api/overview（LIB-R21、R22）。

func (rt *routes) registerOverview(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "overview-get", Method: http.MethodGet, Path: "/api/overview",
		Summary: "總覽：處理中、需要你處理、今天的動態、最近完成、本月花費、磁碟用量與提醒（LIB-R21）；垃圾桶中的影音不出現",
	}, auth.ScopeRead, rt.getOverview)
}

type overviewInput struct {
	TZ string `query:"tz" required:"false" doc:"「今天」用的 IANA 時區；預設是預算的時區（budget.timezone）"`
}

// OverviewProgress 是執行中步驟的任務完成數。
type OverviewProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// OverviewNowItem 是「現在」的一部影音。
type OverviewNowItem struct {
	ID       string           `json:"id"`
	Ref      string           `json:"ref"`
	Title    string           `json:"title"`
	Stage    string           `json:"stage" doc:"最早開始的執行中步驟"`
	Progress OverviewProgress `json:"progress" doc:"那個步驟的任務完成數"`
}

// OverviewNow 是「現在」：有步驟 running 的影音，最多 3 部。
type OverviewNow struct {
	Items []OverviewNowItem `json:"items"`
	Total int               `json:"total"`
	More  int               `json:"more" doc:"total 減去列出的數量"`
}

// OverviewNeedItem 是「需要你處理」的一部影音。
type OverviewNeedItem struct {
	ID      string   `json:"id"`
	Ref     string   `json:"ref"`
	Title   string   `json:"title"`
	Stage   string   `json:"stage" doc:"pipeline 順序中第一個 blocked 或 failed 的步驟"`
	Status  string   `json:"status" enum:"blocked,failed"`
	Reason  string   `json:"reason" doc:"blocked 的原因（block_reason），或 failed 的錯誤分類（error_class）"`
	Message string   `json:"message"`
	Actions []string `json:"actions"`
}

// OverviewNeeds 是「需要你處理」：最多 50 部，依步驟的更新時間由新到舊。
type OverviewNeeds struct {
	Items []OverviewNeedItem `json:"items"`
	Total int                `json:"total"`
}

// OverviewEvent 是「今天」的一筆事件。
type OverviewEvent struct {
	At    time.Time `json:"at"`
	Type  string    `json:"type"`
	Item  string    `json:"item,omitempty" doc:"影音的代號"`
	Title string    `json:"title,omitempty"`
}

// OverviewDoneItem 是「最近完成」的一部影音。
type OverviewDoneItem struct {
	ID     string    `json:"id"`
	Ref    string    `json:"ref"`
	Title  string    `json:"title"`
	DoneAt time.Time `json:"done_at"`
}

// OverviewMonth 是本月的花費與預算（COST-R4 的數字）。
type OverviewMonth struct {
	Month       string   `json:"month"`
	SpentUSD    float64  `json:"spent_usd"`
	ReservedUSD float64  `json:"reserved_usd"`
	MonthlyUSD  *float64 `json:"monthly_usd" nullable:"true" doc:"沒有月預算時是 null"`
	Timezone    string   `json:"timezone"`
}

// OverviewDisk 是磁碟用量（LIB-R22）；從來沒有統計完成時各數字是 null。
type OverviewDisk struct {
	DataBytes      *int64     `json:"data_bytes" nullable:"true"`
	MediaBytes     *int64     `json:"media_bytes" nullable:"true"`
	ArtifactsBytes *int64     `json:"artifacts_bytes" nullable:"true"`
	UploadsBytes   *int64     `json:"uploads_bytes" nullable:"true"`
	FreeBytes      *int64     `json:"free_bytes" nullable:"true"`
	MeasuredAt     *time.Time `json:"measured_at" nullable:"true"`
}

// OverviewResult 是總覽的回應。
type OverviewResult struct {
	Now        OverviewNow        `json:"now"`
	NeedsYou   OverviewNeeds      `json:"needs_you"`
	Today      []OverviewEvent    `json:"today"`
	RecentDone []OverviewDoneItem `json:"recent_done"`
	Month      OverviewMonth      `json:"month"`
	Disk       OverviewDisk       `json:"disk"`
	Notices    []cost.Notice      `json:"notices"`
}

type overviewOutput struct{ Body OverviewResult }

func (rt *routes) getOverview(ctx context.Context, in *overviewInput) (*overviewOutput, error) {
	if err := requireScope(ctx, auth.ScopeRead); err != nil {
		return nil, err
	}
	if rt.d.Pipeline == nil {
		return nil, rt.noPipeline(ctx)
	}
	budget, hasCost := cost.DefaultBudget(), rt.d.Cost != nil && rt.d.Store != nil
	if hasCost {
		b, err := rt.d.Cost.Budget(ctx, rt.d.Store.Read())
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		budget = b
	}
	loc := budget.Location()
	if in.TZ != "" {
		l, err := cost.LoadZone(in.TZ)
		if err != nil {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, cost.ZoneMessage(in.TZ))
		}
		loc = l
	}
	now := rt.d.Clock.Now().In(loc)
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	ov, err := rt.d.Pipeline.Overview(ctx, since)
	if err != nil {
		return nil, rt.mapErr(ctx, err)
	}

	out := OverviewResult{
		Now:        OverviewNow{Items: []OverviewNowItem{}, Total: ov.NowTotal},
		NeedsYou:   OverviewNeeds{Items: []OverviewNeedItem{}, Total: ov.NeedsTotal},
		Today:      []OverviewEvent{},
		RecentDone: []OverviewDoneItem{},
		Notices:    []cost.Notice{},
	}
	for _, n := range ov.Now {
		out.Now.Items = append(out.Now.Items, OverviewNowItem{ID: n.ID, Ref: n.Ref, Title: n.Title, Stage: n.Stage,
			Progress: OverviewProgress{Done: n.Done, Total: n.Total}})
	}
	out.Now.More = ov.NowTotal - len(out.Now.Items)
	for _, n := range ov.Needs {
		out.NeedsYou.Items = append(out.NeedsYou.Items, OverviewNeedItem{ID: n.ID, Ref: n.Ref, Title: n.Title, Stage: n.Stage,
			Status: n.Status, Reason: n.Reason, Message: n.Message, Actions: n.Actions})
	}
	for _, e := range ov.Today {
		out.Today = append(out.Today, OverviewEvent{At: e.At, Type: e.Type, Item: e.Item, Title: e.Title})
	}
	for _, d := range ov.RecentDone {
		out.RecentDone = append(out.RecentDone, OverviewDoneItem{ID: d.ID, Ref: d.Ref, Title: d.Title, DoneAt: d.DoneAt})
	}
	_, _, label := cost.MonthOf(rt.d.Clock.Now(), budget.Location())
	out.Month = OverviewMonth{Month: label, Timezone: budget.Timezone}
	if budget.Monthly != nil {
		v := budget.Monthly.USD()
		out.Month.MonthlyUSD = &v
	}
	if hasCost {
		r := rt.d.Store.Read()
		m, err := rt.d.Cost.Month(ctx, r, budget, rt.d.Cost.Now())
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		out.Month.Month, out.Month.SpentUSD, out.Month.ReservedUSD = m.Month, m.Actual.USD(), m.Reserved.USD()
		notices, err := rt.d.Cost.Notices(ctx, r)
		if err != nil {
			return nil, rt.mapErr(ctx, err)
		}
		out.Notices = append(out.Notices, notices...)
	}
	if rt.d.Disk != nil {
		u := rt.d.Disk.Get(ctx)
		out.Disk = OverviewDisk{DataBytes: u.DataBytes, MediaBytes: u.MediaBytes, ArtifactsBytes: u.ArtifactsBytes,
			UploadsBytes: u.UploadsBytes, FreeBytes: u.FreeBytes, MeasuredAt: u.MeasuredAt}
	}
	return &overviewOutput{Body: out}, nil
}
