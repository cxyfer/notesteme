package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// 這個檔案是批次操作的已讀狀態（LIB-R19）與總覽（LIB-R21）的查詢。

// SetItemReadState 設定影音的已讀狀態；回傳是否有改到（影音不存在或在垃圾桶中時為 false）。
func (r *Repo) SetItemReadState(ctx context.Context, id, state string) (bool, error) {
	n, err := r.q.SetItemReadState(ctx, sqlcdb.SetItemReadStateParams{ReadState: state, ID: id})
	return n > 0, err
}

// ItemsByExternalID 回傳同一個 external_id 的所有影音（含垃圾桶中的），依分P排序。
func (r *Repo) ItemsByExternalID(ctx context.Context, platform, externalID string) ([]Item, error) {
	rows, err := r.q.ItemsByExternalID(ctx, sqlcdb.ItemsByExternalIDParams{Platform: platform, ExternalID: externalID})
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(rows))
	for _, row := range rows {
		out = append(out, itemFromRow(row))
	}
	return out, nil
}

// AttentionRun 是總覽用的一筆最新 stage run（running、blocked 或 failed），帶著影音的代號欄位與標題。
type AttentionRun struct {
	ID, ItemID, Stage, Status        string
	BlockReason, ErrorClass, Message string
	Actions                          []string
	StartedAt, FinishedAt            *time.Time
	CreatedAt                        time.Time
	Platform, ExternalID             string
	Part                             int
	Title                            string
}

// UpdatedAt 是步驟最後一次改變的時間：結束、開始或建立時間中最晚有值的那個。
func (a AttentionRun) UpdatedAt() time.Time {
	switch {
	case a.FinishedAt != nil:
		return *a.FinishedAt
	case a.StartedAt != nil:
		return *a.StartedAt
	}
	return a.CreatedAt
}

// OverviewAttentionRuns 回傳垃圾桶以外的影音中，每個步驟最新一次執行的狀態是 statuses 之一的 stage run（依建立順序）。
// statuses 只能是 running、blocked、failed（部分索引 stage_runs_attention 的範圍）。
func (r *Repo) OverviewAttentionRuns(ctx context.Context, statuses ...string) ([]AttentionRun, error) {
	rows, err := r.q.OverviewAttentionRuns(ctx, "|"+strings.Join(statuses, "|")+"|")
	if err != nil {
		return nil, err
	}
	out := make([]AttentionRun, 0, len(rows))
	for _, row := range rows {
		a := AttentionRun{
			ID: row.ID, ItemID: row.ItemID, Stage: row.Stage, Status: row.Status, BlockReason: row.BlockReason,
			ErrorClass: row.ErrorClass, Message: row.ErrorMessage, StartedAt: msToTime(row.StartedAt),
			FinishedAt: msToTime(row.FinishedAt), CreatedAt: fromMs(row.CreatedAt),
			Platform: row.Platform, ExternalID: row.ExternalID, Part: int(row.Part), Title: row.Title,
		}
		if err := json.Unmarshal([]byte(row.ActionsJson), &a.Actions); err != nil || a.Actions == nil {
			a.Actions = []string{}
		}
		out = append(out, a)
	}
	return out, nil
}

// StageRunTaskProgress 回傳 stage run 已完成的任務數與任務總數（不含取消的）。
func (r *Repo) StageRunTaskProgress(ctx context.Context, stageRunID string) (done, total int, err error) {
	row, err := r.q.StageRunTaskProgress(ctx, stageRunID)
	return int(row.Done), int(row.Total), err
}

// OverviewEvent 是總覽「今天」的一筆事件；Payload 是事件的 JSON，Title 是影音目前的標題（沒有影音時是空字串）。
type OverviewEvent struct {
	Type    string
	Payload json.RawMessage
	At      time.Time
	Title   string
}

// OverviewEvents 回傳 since 之後、總覽會列出的事件，由新到舊，最多 limit 筆。
func (r *Repo) OverviewEvents(ctx context.Context, since time.Time, limit int) ([]OverviewEvent, error) {
	rows, err := r.q.OverviewEvents(ctx, sqlcdb.OverviewEventsParams{Since: since.UnixMilli(), PageSize: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]OverviewEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, OverviewEvent{Type: row.Type, Payload: json.RawMessage(row.PayloadJson), At: fromMs(row.At), Title: row.Title})
	}
	return out, nil
}

// RecentDoneItem 是最近處理完的一部影音。
type RecentDoneItem struct {
	ID, Platform, ExternalID string
	Part                     int
	Title                    string
	DoneAt                   time.Time
}

// RecentDoneScan 是 OverviewRecentDone 最多看的已結束 stage run 數：媒體庫很大時查詢仍有上限。
const RecentDoneScan = 2000

// OverviewRecentDone 回傳最近 limit 部所有步驟都已結束（done 或 skipped）的影音，依最後結束的時間由新到舊。
func (r *Repo) OverviewRecentDone(ctx context.Context, limit int) ([]RecentDoneItem, error) {
	rows, err := r.q.OverviewRecentDone(ctx, sqlcdb.OverviewRecentDoneParams{ScanLimit: RecentDoneScan, PageSize: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]RecentDoneItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, RecentDoneItem{ID: row.ID, Platform: row.Platform, ExternalID: row.ExternalID, Part: int(row.Part),
			Title: row.Title, DoneAt: fromMs(row.DoneAt)})
	}
	return out, nil
}
