package pipeline

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案是總覽（LIB-R21）中來自影音與步驟的部分：現在、需要你處理、今天、最近完成。
// 本月花費、提醒與磁碟用量由 API 組合 cost 與 diskusage 的結果。垃圾桶中的影音不出現在任何區塊。

// 總覽各區塊的上限（LIB-R21）。
const (
	OverviewNowLimit   = 3
	OverviewNeedsLimit = 50
	OverviewTodayLimit = 50
	OverviewDoneLimit  = 5
)

// OverviewRunning 是「現在」的一部影音：最早開始的執行中步驟，以及它的任務完成數。
type OverviewRunning struct {
	ID, Ref, Title, Stage string
	Done, Total           int
}

// OverviewNeed 是「需要你處理」的一部影音：pipeline 順序中第一個 blocked 或 failed 的步驟。
type OverviewNeed struct {
	ID, Ref, Title, Stage, Status, Reason, Message string
	Actions                                        []string
	UpdatedAt                                      time.Time
}

// OverviewEvent 是「今天」的一筆事件；Item 是影音代號（沒有時是空字串）。
type OverviewEvent struct {
	At          time.Time
	Type        string
	Item, Title string
}

// OverviewDone 是「最近完成」的一部影音。
type OverviewDone struct {
	ID, Ref, Title string
	DoneAt         time.Time
}

// Overview 是總覽中來自影音與步驟的區塊。
type Overview struct {
	Now        []OverviewRunning
	NowTotal   int
	Needs      []OverviewNeed
	NeedsTotal int
	Today      []OverviewEvent
	RecentDone []OverviewDone
}

// Overview 組出總覽；today 從 since（請求時區的今天 00:00）起算。只讀取，不寫入。
func (p *Pipeline) Overview(ctx context.Context, since time.Time) (Overview, error) {
	r := p.st.Read()
	var out Overview
	runs, err := r.OverviewAttentionRuns(ctx, store.StatusRunning, store.StatusBlocked, store.StatusFailed)
	if err != nil {
		return Overview{}, err
	}
	running, needs := map[string]store.AttentionRun{}, map[string]store.AttentionRun{}
	for _, a := range runs {
		switch a.Status {
		case store.StatusRunning:
			// 同一部影音有多個執行中的步驟時取最早開始的。
			if cur, ok := running[a.ItemID]; !ok || startOf(a).Before(startOf(cur)) {
				running[a.ItemID] = a
			}
		default:
			if cur, ok := needs[a.ItemID]; !ok || slices.Index(Stages, a.Stage) < slices.Index(Stages, cur.Stage) {
				needs[a.ItemID] = a
			}
		}
	}

	now := mapValues(running)
	slices.SortFunc(now, func(a, b store.AttentionRun) int {
		if c := startOf(a).Compare(startOf(b)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	out.NowTotal = len(now)
	for _, a := range now[:min(len(now), OverviewNowLimit)] {
		done, total, err := r.StageRunTaskProgress(ctx, a.ID)
		if err != nil {
			return Overview{}, err
		}
		out.Now = append(out.Now, OverviewRunning{ID: a.ItemID, Ref: attentionRef(a), Title: a.Title, Stage: a.Stage, Done: done, Total: total})
	}

	need := mapValues(needs)
	slices.SortFunc(need, func(a, b store.AttentionRun) int {
		if c := b.UpdatedAt().Compare(a.UpdatedAt()); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	out.NeedsTotal = len(need)
	for _, a := range need[:min(len(need), OverviewNeedsLimit)] {
		reason := a.BlockReason
		if a.Status == store.StatusFailed || reason == "" {
			reason = a.ErrorClass
		}
		out.Needs = append(out.Needs, OverviewNeed{ID: a.ItemID, Ref: attentionRef(a), Title: a.Title, Stage: a.Stage, Status: a.Status,
			Reason: reason, Message: a.Message, Actions: a.Actions, UpdatedAt: a.UpdatedAt()})
	}

	evs, err := r.OverviewEvents(ctx, since, OverviewTodayLimit)
	if err != nil {
		return Overview{}, err
	}
	for _, e := range evs {
		var payload struct {
			Item  string `json:"item"`
			Title string `json:"title"`
		}
		_ = json.Unmarshal(e.Payload, &payload) // 格式不對時只列類型與時間
		title := e.Title
		if payload.Title != "" {
			title = payload.Title // item.deleted、item.purged 帶著當時的標題
		}
		out.Today = append(out.Today, OverviewEvent{At: e.At, Type: e.Type, Item: payload.Item, Title: title})
	}

	done, err := r.OverviewRecentDone(ctx, OverviewDoneLimit)
	if err != nil {
		return Overview{}, err
	}
	for _, d := range done {
		out.RecentDone = append(out.RecentDone, OverviewDone{ID: d.ID, Ref: source.Ref(d.Platform, d.ExternalID, d.Part), Title: d.Title, DoneAt: d.DoneAt})
	}
	return out, nil
}

// startOf 是執行中步驟的開始時間；還沒有記錄時用建立時間。
func startOf(a store.AttentionRun) time.Time {
	if a.StartedAt != nil {
		return *a.StartedAt
	}
	return a.CreatedAt
}

func attentionRef(a store.AttentionRun) string { return source.Ref(a.Platform, a.ExternalID, a.Part) }

func mapValues(m map[string]store.AttentionRun) []store.AttentionRun {
	out := make([]store.AttentionRun, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
