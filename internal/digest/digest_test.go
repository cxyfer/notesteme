package digest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// fakeSummaries 是 Summaries 的假實作：依影音 id 回傳摘要；errs 中的影音回傳錯誤。
type fakeSummaries struct {
	m    map[string]pipeline.Summary
	errs map[string]bool
}

func (f *fakeSummaries) ItemSummary(_ context.Context, id string) (pipeline.Summary, bool, error) {
	if f.errs[id] {
		return pipeline.Summary{}, false, errors.New("壞掉的摘要")
	}
	s, ok := f.m[id]
	return s, ok, nil
}

type rig struct {
	st  *store.Store
	clk *clock.Fake
	sum *fakeSummaries
	svc *Service
	ob  *notify.Outbox
}

func newRig(t *testing.T, publicURL string) *rig {
	t.Helper()
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := &rig{st: st, clk: clk, sum: &fakeSummaries{m: map[string]pipeline.Summary{}, errs: map[string]bool{}}, ob: &notify.Outbox{Clock: clk}}
	g.svc = New(Deps{Store: st, Clock: clk, Outbox: g.ob, Summaries: g.sum, PublicURL: publicURL})
	return g
}

// notifier 新增一個訂閱 events 的 notifier（只需要 store 的列，不需要密鑰）。
func (g *rig) notifier(t *testing.T, id string, events ...string) {
	t.Helper()
	err := g.st.Tx(context.Background(), func(r *store.Repo) error {
		return r.InsertNotifier(context.Background(), store.Notifier{ID: id, Kind: notify.KindWebhook, Nonce: []byte{0}, Ciphertext: []byte{0}, PublicJSON: `{}`, Events: events, Enabled: true})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// item 新增一部影音，summary 步驟在現在完成（status）；awaiting 時下一個步驟停在等你決定。
func (g *rig) item(t *testing.T, ext, title string, dur int, status string, awaiting bool) string {
	t.Helper()
	ctx := context.Background()
	var id string
	err := g.st.Tx(ctx, func(r *store.Repo) error {
		it, _, err := r.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "https://www.youtube.com/watch?v=" + ext})
		if err != nil {
			return err
		}
		id = it.ID
		d := dur
		if err := r.UpdateItemMeta(ctx, id, store.ItemMeta{Title: title, DurationS: &d}); err != nil {
			return err
		}
		snap, err := r.EnsureSnapshot(ctx, strings.Repeat("a", 64), `{}`)
		if err != nil {
			return err
		}
		run, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: id, Stage: pipeline.StageSummary, Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			return err
		}
		if err := r.SetStageState(ctx, run.ID, store.StageState{Status: status}); err != nil {
			return err
		}
		if awaiting {
			seg, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: id, Stage: pipeline.StageSegment, Status: store.StatusPending, ConfigSnapshotID: snap})
			if err != nil {
				return err
			}
			return r.SetStageState(ctx, seg.ID, store.StageState{Status: store.StatusBlocked, BlockReason: pipeline.BlockAwaitingUser})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func ptr[T any](v T) *T { return &v }

func (g *rig) outbox(t *testing.T) []store.OutboxEntry {
	t.Helper()
	list, err := g.st.Read().ListOutbox(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestScheduleNextPrevAndDST(t *testing.T) {
	ny, err := newSchedule(PeriodDaily, "02:30", "", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-03-08 02:30 在紐約不存在（夏令時間開始）：取之後第一個有效時間 03:00 EDT。
	gap := ny.next(time.Date(2026, 3, 8, 0, 0, 0, 0, ny.loc))
	if got := gap.In(ny.loc).Format("2006-01-02 15:04 MST"); got != "2026-03-08 03:00 EDT" {
		t.Errorf("跳過的時間 → %s", got)
	}
	// 2026-11-01 01:30 重複（夏令時間結束）：取第一次（EDT）。
	amb, _ := newSchedule(PeriodDaily, "01:30", "", "America/New_York")
	first := amb.next(time.Date(2026, 11, 1, 0, 0, 0, 0, amb.loc))
	if got := first.In(amb.loc).Format("15:04 MST"); got != "01:30 EDT" {
		t.Errorf("重複的時間 → %s", got)
	}
	// 嚴格晚於：剛好在排定時間時 next 是隔天、prev 是自己。
	utc, _ := newSchedule(PeriodDaily, "08:00", "", "UTC")
	at := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	if n := utc.next(at); !n.Equal(at.Add(24 * time.Hour)) {
		t.Errorf("next = %s", n)
	}
	if p := utc.prev(at); !p.Equal(at) {
		t.Errorf("prev = %s", p)
	}
	if p := utc.prev(at.Add(-time.Minute)); !p.Equal(at.Add(-24 * time.Hour)) {
		t.Errorf("prev 前一刻 = %s", p)
	}
	// 每週：只落在指定的星期（2026-11-23 是星期一）。
	wk, _ := newSchedule(PeriodWeekly, "09:00", "fri", "Asia/Taipei")
	n := wk.next(at)
	if n.In(wk.loc).Weekday() != time.Friday || n.In(wk.loc).Format("2006-01-02 15:04") != "2026-11-27 09:00" {
		t.Errorf("每週 next = %s", n.In(wk.loc))
	}
	if p := wk.prev(at); p.In(wk.loc).Format("2006-01-02") != "2026-11-20" {
		t.Errorf("每週 prev = %s", p.In(wk.loc))
	}
	if wk.period() != 7*24*time.Hour || utc.period() != 24*time.Hour {
		t.Error("period")
	}
	// 標題：每日是終點的當地日期；每週是起點～終點前一天。
	if got := utc.title(at.Add(-24*time.Hour), at, 3); got != "Notesteme 每日彙整：2026-11-23（3 部）" {
		t.Errorf("每日標題 %q", got)
	}
	end := time.Date(2026, 11, 27, 1, 0, 0, 0, time.UTC) // 台北 09:00
	if got := wk.title(end.Add(-7*24*time.Hour), end, 1); got != "Notesteme 每週彙整：2026-11-20～2026-11-26（1 部）" {
		t.Errorf("每週標題 %q", got)
	}
}

func TestScheduleRejectsBadInput(t *testing.T) {
	for _, tz := range []string{"", "Local", "/etc/localtime", "../../etc/passwd", "Asia/../UTC", "a\x00b", `a\b`, "Mars/Olympus"} {
		if _, err := loadZone(tz); err == nil {
			t.Errorf("時區 %q 應該拒絕", tz)
		}
	}
	for _, at := range []string{"", "8:00", "24:00", "12:60", "-1:00", "ab:cd", "12:000", "1200"} {
		if _, _, ok := parseClock(at); ok {
			t.Errorf("at %q 應該拒絕", at)
		}
	}
	if h, m, ok := parseClock("23:59"); !ok || h != 23 || m != 59 {
		t.Error("23:59")
	}
	if _, err := newSchedule(PeriodWeekly, "08:00", "funday", "UTC"); err == nil {
		t.Error("壞的 weekday")
	}
	if _, err := newSchedule(PeriodDaily, "8", "", "UTC"); err == nil {
		t.Error("壞的 at")
	}
	if _, err := newSchedule(PeriodDaily, "08:00", "", "Nope/Zone"); err == nil {
		t.Error("壞的時區")
	}
}

func TestCreateValidatesAndSchedules(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "")
	g.notifier(t, "phone", notify.EventDigest)
	g.notifier(t, "alerts", "alert")

	var inv *InvalidError
	cases := map[string]struct {
		in   Input
		path string
		msg  string
	}{
		"壞 id":         {Input{ID: "Bad_ID", Period: ptr(PeriodDaily), Notifiers: &[]string{"phone"}}, "id", ""},
		"壞 period":     {Input{ID: "a", Period: ptr("monthly"), Notifiers: &[]string{"phone"}}, "period", ""},
		"壞 at":         {Input{ID: "a", Period: ptr(PeriodDaily), At: ptr("25:00"), Notifiers: &[]string{"phone"}}, "at", ""},
		"每週缺 weekday":  {Input{ID: "a", Period: ptr(PeriodWeekly), Notifiers: &[]string{"phone"}}, "weekday", ""},
		"每日給 weekday":  {Input{ID: "a", Period: ptr(PeriodDaily), Weekday: ptr("mon"), Notifiers: &[]string{"phone"}}, "weekday", ""},
		"壞時區":          {Input{ID: "a", Period: ptr(PeriodDaily), Timezone: ptr("Mars/" + strings.Repeat("x", 100)), Notifiers: &[]string{"phone"}}, "timezone", "…"},
		"壞 scope":      {Input{ID: "a", Period: ptr(PeriodDaily), Scope: ptr("everything"), Notifiers: &[]string{"phone"}}, "scope", ""},
		"沒有 notifier":  {Input{ID: "a", Period: ptr(PeriodDaily), Notifiers: &[]string{}}, "notifiers", ""},
		"太多 notifier":  {Input{ID: "a", Period: ptr(PeriodDaily), Notifiers: &[]string{"a", "b", "c", "d", "e", "f"}}, "notifiers", ""},
		"重複 notifier":  {Input{ID: "a", Period: ptr(PeriodDaily), Notifiers: &[]string{"phone", "phone"}}, "notifiers", "重複"},
		"不存在 notifier": {Input{ID: "a", Period: ptr(PeriodDaily), Notifiers: &[]string{"ghost"}}, "notifiers", "找不到 notifier ghost。"},
		"沒勾彙整":         {Input{ID: "a", Period: ptr(PeriodDaily), Notifiers: &[]string{"alerts"}}, "notifiers", "notifier alerts 沒有勾選「訂閱彙整」。"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := g.svc.Create(ctx, c.in)
			if !errors.As(err, &inv) || len(inv.Fields) == 0 || inv.Fields[0].Path != c.path || !strings.Contains(inv.Fields[0].Message, c.msg) {
				t.Fatalf("err = %v（%+v）", err, inv)
			}
			if inv.Error() == "" {
				t.Error("Error() 不能是空的")
			}
		})
	}
	if list, _ := g.svc.List(ctx); len(list) != 0 {
		t.Fatalf("驗證失敗不能寫入：%+v", list)
	}

	// 預設值：08:00、UTC、subscriptions、啟用；下一次是今天 08:00 之後的下一個（現在 10:00 → 明天）。
	info, err := g.svc.Create(ctx, Input{ID: "daily", Period: ptr(PeriodDaily), Notifiers: &[]string{"phone"}})
	if err != nil {
		t.Fatal(err)
	}
	if info.At != "08:00" || info.Timezone != "UTC" || info.Scope != ScopeSubscriptions || !info.Enabled || info.Weekday != nil || info.LastRunAt != nil {
		t.Errorf("預設值 %+v", info)
	}
	if info.NextRunAt == nil || !info.NextRunAt.Equal(time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("next_run_at = %v", info.NextRunAt)
	}
	if _, err := g.svc.Create(ctx, Input{ID: "daily", Period: ptr(PeriodDaily), Notifiers: &[]string{"phone"}}); !errors.Is(err, ErrExists) {
		t.Errorf("重複 id：%v", err)
	}
	wk, err := g.svc.Create(ctx, Input{ID: "weekly", Period: ptr(PeriodWeekly), Weekday: ptr("sun"), At: ptr("20:00"), Timezone: ptr("Asia/Taipei"), Notifiers: &[]string{"phone"}, Scope: ptr(ScopeAll), Enabled: ptr(false)})
	if err != nil || wk.Weekday == nil || *wk.Weekday != "sun" || wk.NextRunAt != nil || wk.Enabled {
		t.Fatalf("停用的每週彙整：%+v %v", wk, err)
	}
	list, err := g.svc.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "daily" || list[1].ID != "weekly" {
		t.Errorf("List = %+v %v", list, err)
	}
	if got, err := g.svc.Get(ctx, "daily"); err != nil || got.NextRunAt == nil {
		t.Errorf("Get = %+v %v", got, err)
	}
	if _, err := g.svc.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get 不存在：%v", err)
	}
}

func TestPatchReschedulesAndDelete(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "")
	g.notifier(t, "phone", notify.EventDigest)
	if _, err := g.svc.Create(ctx, Input{ID: "d", Period: ptr(PeriodDaily), Notifiers: &[]string{"phone"}}); err != nil {
		t.Fatal(err)
	}
	// 改時間 → 重新排定。
	info, err := g.svc.Patch(ctx, "d", Input{At: ptr("12:00")})
	if err != nil || info.NextRunAt == nil || !info.NextRunAt.Equal(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("改 at：%+v %v", info, err)
	}
	// 改成每週再改回每日：weekday 會被清掉。
	if info, err = g.svc.Patch(ctx, "d", Input{Period: ptr(PeriodWeekly), Weekday: ptr("wed")}); err != nil || info.Weekday == nil {
		t.Fatalf("改每週：%+v %v", info, err)
	}
	if info, err = g.svc.Patch(ctx, "d", Input{Period: ptr(PeriodDaily)}); err != nil || info.Weekday != nil {
		t.Fatalf("改回每日：%+v %v", info, err)
	}
	// 只改 notifiers 不影響排程。
	before := *info.NextRunAt
	if info, err = g.svc.Patch(ctx, "d", Input{Notifiers: &[]string{"phone"}}); err != nil || !info.NextRunAt.Equal(before) {
		t.Fatalf("改 notifiers：%+v %v", info, err)
	}
	// 停用 → 刪除排程工作。
	if info, err = g.svc.Patch(ctx, "d", Input{Enabled: ptr(false)}); err != nil || info.NextRunAt != nil {
		t.Fatalf("停用：%+v %v", info, err)
	}
	if _, err := g.st.Read().ScheduledJob(ctx, Kind, "d"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("停用後不應有排程工作：%v", err)
	}
	// 停用時先記錄過 last_run_at，重新啟用時範圍重新起算。
	if err := g.st.Tx(ctx, func(r *store.Repo) error { return r.SetDigestLastRun(ctx, "d", g.clk.Now()) }); err != nil {
		t.Fatal(err)
	}
	if info, err = g.svc.Patch(ctx, "d", Input{Enabled: ptr(true)}); err != nil || info.NextRunAt == nil || info.LastRunAt != nil {
		t.Fatalf("重新啟用：%+v %v", info, err)
	}
	var inv *InvalidError
	if _, err := g.svc.Patch(ctx, "d", Input{At: ptr("noon")}); !errors.As(err, &inv) {
		t.Errorf("PATCH 也驗證：%v", err)
	}
	if _, err := g.svc.Patch(ctx, "nope", Input{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("PATCH 不存在：%v", err)
	}
	// 排程工作被刪掉但仍啟用時，PATCH 會補排。
	if err := g.st.Tx(ctx, func(r *store.Repo) error { _, err := r.DeleteScheduledJob(ctx, Kind, "d"); return err }); err != nil {
		t.Fatal(err)
	}
	if info, err = g.svc.Patch(ctx, "d", Input{}); err != nil || info.NextRunAt == nil {
		t.Errorf("補排：%+v %v", info, err)
	}
	if err := g.svc.Delete(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if err := g.svc.Delete(ctx, "d"); !errors.Is(err, ErrNotFound) {
		t.Errorf("第二次刪除：%v", err)
	}
	if _, err := g.st.Read().ScheduledJob(ctx, Kind, "d"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("刪除後不應有排程工作：%v", err)
	}
}

// runJob 直接執行排程工作 digest。
func (g *rig) runJob(t *testing.T, id string) scheduler.Result {
	t.Helper()
	ctx := context.Background()
	j, err := g.st.Read().ScheduledJob(ctx, Kind, id)
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.svc.run(ctx, scheduler.Job{Kind: Kind, Key: id, NextRunAt: j.NextRunAt}, g.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRunSendsOnceAndSkipsEmpty(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "https://notes.example")
	g.notifier(t, "phone", notify.EventDigest)
	g.notifier(t, "other", notify.EventDigest)
	if _, err := g.svc.Create(ctx, Input{ID: "all", Period: ptr(PeriodDaily), Scope: ptr(ScopeAll), Notifiers: &[]string{"phone"}}); err != nil {
		t.Fatal(err)
	}
	// 範圍 (11-23 08:00, 11-24 08:00]：10:00 完成的兩部與一部略過摘要（skipped）都算；failed 不算。
	a := g.item(t, "digaaaaaaaa", "第一部 ‮標題", 3725, store.StatusDone, true)
	g.sum.m[a] = pipeline.Summary{Text: "重點\x07一\n\n" + strings.Repeat("長", 300), TitleTranslation: ptr("First")}
	b := g.item(t, "digbbbbbbbb", "", 0, store.StatusSkipped, false)
	g.sum.errs[b] = true
	g.item(t, "digcccccccc", "失敗", 10, store.StatusFailed, false)

	g.clk.Set(time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
	res := g.runJob(t, "all")
	if res.Done || !res.Next.Equal(time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("Result = %+v", res)
	}
	out := g.outbox(t)
	if len(out) != 1 || out[0].NotifierID != "phone" || out[0].Event != notify.EventDigest {
		t.Fatalf("只送給彙整指定的 notifier：%+v", out)
	}
	var ev notify.Event
	if err := json.Unmarshal([]byte(out[0].PayloadJSON), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Summary != "Notesteme 每日彙整：2026-11-24（2 部）" {
		t.Errorf("標題 %q", ev.Summary)
	}
	for _, want := range []string{"第一部 標題（First）", "1:02:05", "重點一", "…", "等你決定", "https://notes.example/items/" + a, "yt:digbbbbbbbb", "（沒有摘要）", "等你決定：https://notes.example/library?blocked=awaiting_user"} {
		if !strings.Contains(ev.Message, want) {
			t.Errorf("內文缺 %q：\n%s", want, ev.Message)
		}
	}
	if strings.Contains(ev.Message, "‮") || strings.Contains(ev.Message, "\x07") || strings.Contains(ev.Message, "失敗") {
		t.Errorf("內文含控制字元或不該列的影音：%q", ev.Message)
	}
	var items []DigestEntry
	if err := json.Unmarshal(ev.Items, &items); err != nil || len(items) != 2 || !items[0].AwaitingUser || items[0].URL == nil || items[1].Summary != nil {
		t.Errorf("items = %+v %v", items, err)
	}
	var rng DigestRange
	if err := json.Unmarshal(ev.Digest, &rng); err != nil || rng.ID != "all" || !rng.Start.Equal(time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)) || !rng.End.Equal(g.clk.Now()) {
		t.Errorf("digest = %+v %v", rng, err)
	}
	d, _ := g.st.Read().Digest(ctx, "all")
	if d.LastRunAt == nil || !d.LastRunAt.Equal(g.clk.Now()) {
		t.Errorf("last_run_at = %v", d.LastRunAt)
	}

	// 下一次：範圍內沒有新影音 → 只記錄終點、不送出；已列過的影音不再列出。
	if err := g.st.Tx(ctx, func(r *store.Repo) error { return r.ScheduleJob(ctx, Kind, "all", res.Next) }); err != nil {
		t.Fatal(err)
	}
	g.clk.Set(res.Next)
	g.runJob(t, "all")
	if n := len(g.outbox(t)); n != 1 {
		t.Errorf("沒有影音時不送出：%d", n)
	}
	d, _ = g.st.Read().Digest(ctx, "all")
	if !d.LastRunAt.Equal(res.Next) {
		t.Errorf("空的範圍也要記錄終點：%v", d.LastRunAt)
	}

	// 停用或已刪除的彙整：工作結束。
	if _, err := g.svc.Patch(ctx, "all", Input{Enabled: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if res, err := g.svc.run(ctx, scheduler.Job{Kind: Kind, Key: "all", NextRunAt: g.clk.Now()}, g.clk.Now()); err != nil || !res.Done {
		t.Errorf("停用：%+v %v", res, err)
	}
	if res, err := g.svc.run(ctx, scheduler.Job{Kind: Kind, Key: "ghost", NextRunAt: g.clk.Now()}, g.clk.Now()); err != nil || !res.Done {
		t.Errorf("不存在：%+v %v", res, err)
	}
}

func TestRunCatchesUpAfterDowntime(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "")
	g.notifier(t, "phone", notify.EventDigest)
	if _, err := g.svc.Create(ctx, Input{ID: "d", Period: ptr(PeriodDaily), Scope: ptr(ScopeAll), Notifiers: &[]string{"phone"}}); err != nil {
		t.Fatal(err)
	}
	g.item(t, "digddddddd1", "一", 0, store.StatusDone, false)
	// 停機三天後才執行：範圍的終點是不晚於現在的最近一個排定時間，涵蓋整段錯過的期間，只送一次。
	g.clk.Set(time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC))
	res := g.runJob(t, "d")
	if !res.Next.Equal(time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("Next = %s", res.Next)
	}
	out := g.outbox(t)
	if len(out) != 1 {
		t.Fatalf("outbox = %d", len(out))
	}
	var ev notify.Event
	_ = json.Unmarshal([]byte(out[0].PayloadJSON), &ev)
	var rng DigestRange
	_ = json.Unmarshal(ev.Digest, &rng)
	if !rng.End.Equal(time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)) || !rng.Start.Equal(time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("範圍 %+v", rng)
	}
	if !strings.Contains(ev.Message, noPublicURLFooter) || strings.Contains(ev.Message, "http") {
		t.Errorf("沒有 public_url 時提示設定、不附連結：%q", ev.Message)
	}
}

func TestScopeSubscriptionsExcludesManualItems(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "")
	g.notifier(t, "phone", notify.EventDigest)
	if _, err := g.svc.Create(ctx, Input{ID: "subs", Period: ptr(PeriodDaily), Notifiers: &[]string{"phone"}}); err != nil {
		t.Fatal(err)
	}
	g.item(t, "digmanual01", "手動新增", 0, store.StatusDone, false)
	p, err := g.svc.Preview(ctx, "subs")
	if err != nil || len(p.Items) != 0 {
		t.Errorf("subscriptions 範圍不含手動新增的影音：%+v %v", p, err)
	}
	g.clk.Set(time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
	g.runJob(t, "subs")
	if n := len(g.outbox(t)); n != 0 {
		t.Errorf("不應送出：%d", n)
	}
}

func TestPreviewDoesNotSendAndListsAtMost50(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "https://n.example")
	g.notifier(t, "phone", notify.EventDigest)
	if _, err := g.svc.Create(ctx, Input{ID: "d", Period: ptr(PeriodDaily), Scope: ptr(ScopeAll), Notifiers: &[]string{"phone"}}); err != nil {
		t.Fatal(err)
	}
	for i := range 53 {
		g.item(t, fmt.Sprintf("dig%08d", i), fmt.Sprintf("影音 %d", i), 60, store.StatusDone, false)
	}
	p, err := g.svc.Preview(ctx, "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != maxListed || !strings.Contains(p.Text, "還有 3 部，請到 Notesteme 查看。") || !strings.HasPrefix(p.Text, "Notesteme 每日彙整：2026-11-23（53 部）") {
		t.Errorf("預覽 %d 部：%q", len(p.Items), p.Text[:min(len(p.Text), 200)])
	}
	if strings.Contains(p.Text, "等你決定：") {
		t.Error("沒有等你決定的影音時不附連結")
	}
	if n := len(g.outbox(t)); n != 0 {
		t.Errorf("預覽不送出：%d", n)
	}
	d, _ := g.st.Read().Digest(ctx, "d")
	if d.LastRunAt != nil {
		t.Error("預覽不改變 last_run_at")
	}
	// 執行時 digest_entries 記錄全部 53 部：下一次的預覽不再列出它們。
	g.clk.Set(time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
	g.runJob(t, "d")
	if p, err = g.svc.Preview(ctx, "d"); err != nil || len(p.Items) != 0 {
		t.Errorf("已送出的影音不再列出：%d %v", len(p.Items), err)
	}
	if _, err := g.svc.Preview(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("預覽不存在：%v", err)
	}
}

func TestRegister(t *testing.T) {
	g := newRig(t, "")
	sch := scheduler.New(g.st, g.clk, nil, 0)
	if err := g.svc.Register(sch); err != nil {
		t.Fatal(err)
	}
	if err := g.svc.Register(sch); err == nil {
		t.Error("重複登記要錯誤")
	}
}

func TestTextHelpers(t *testing.T) {
	if got := truncate("一二三四五", 3); got != "一二三…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("短", 3); got != "短" {
		t.Errorf("truncate 短 = %q", got)
	}
	if got := cleanBlock(" a\x00b​c\n\x1b[31md\xff "); got != "abc\n[31md" {
		t.Errorf("cleanBlock = %q", got)
	}
	if got := clip("一二三", 2); got != "一二…" {
		t.Errorf("clip = %q", got)
	}
	if New(Deps{}).d.Log == nil {
		t.Error("New 要補上 logger")
	}
}

// 失敗退避後的重試不在 at 上：範圍的終點是排定的時間（next_run_at），不早於它（DIG-R9 以排定的時間計算）。
func TestRunEndNotBeforeScheduledTime(t *testing.T) {
	ctx := context.Background()
	g := newRig(t, "")
	g.notifier(t, "phone", notify.EventDigest)
	if _, err := g.svc.Create(ctx, Input{ID: "d", Period: ptr(PeriodDaily), Scope: ptr(ScopeAll), Notifiers: &[]string{"phone"}}); err != nil {
		t.Fatal(err)
	}
	retry := time.Date(2026, 11, 24, 8, 5, 0, 0, time.UTC)
	g.clk.Set(time.Date(2026, 11, 24, 8, 3, 0, 0, time.UTC))
	g.item(t, "digretry001", "重試前完成", 0, store.StatusDone, false)
	g.clk.Set(retry)
	if _, err := g.svc.run(ctx, scheduler.Job{Kind: Kind, Key: "d", NextRunAt: retry}, retry); err != nil {
		t.Fatal(err)
	}
	d, _ := g.st.Read().Digest(ctx, "d")
	if d.LastRunAt == nil || !d.LastRunAt.Equal(retry) {
		t.Errorf("last_run_at = %v，預期 %v", d.LastRunAt, retry)
	}
	if n := len(g.outbox(t)); n != 1 {
		t.Errorf("08:03 完成的影音要在這次送出：%d", n)
	}
}
