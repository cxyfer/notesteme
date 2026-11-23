package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// ToolYtDlp 是 tool_call_stats 中 yt-dlp 的名稱。
const ToolYtDlp = "yt-dlp"

// CookieKind 是 cookie 在 secrets 表中的 kind（internal/cookies）。
const CookieKind = "cookie"

// diskHysteresis 是磁碟用量告警解除時要低於門檻的百分點（OPS-R16）。
const diskHysteresis = 5

// toolStatsRetention 是工具呼叫統計保留的時間（OPS-R18）。
const toolStatsRetention = 48 * time.Hour

// ytdlpWindow 是 yt-dlp 失敗率的觀察區間（OPS-R18）。
const ytdlpWindow = time.Hour

// Alerter 是無人值守告警（OPS-R13～R18）：排程工作 alerts 每 15 分鐘檢查一次。
type Alerter struct {
	Store  *store.Store
	Clock  clock.Clock
	Outbox Emitter
	Log    *slog.Logger
	Cfg    config.Alerts
	// DataDir 是資料目錄；DiskUsage 回傳它所在檔案系統的用量百分比（nil 時用 DiskUsagePercent）。
	DataDir   string
	DiskUsage func(dir string) (int, error)
	// Paused 回報整個佇列是否被 owner 暫停；ClassPaused 回報資源類別是否被暫停（OPS-R14 排除這些步驟）。
	Paused      func() bool
	ClassPaused func(class string) bool
}

// managedKinds 是 alerts 工作管理的告警種類（backup_failed 由 backup.daily 自己管理）。
var managedKinds = []string{KindStuckTask, KindJobStale, KindDiskUsage, KindCookieStale, KindYtdlpFailureRate}

// Register 登記排程工作 alerts（worker 開始之前呼叫）。間隔固定 15 分鐘，不加 jitter。
func (a *Alerter) Register(sch *scheduler.Scheduler) error {
	return sch.Register(scheduler.Kind{Name: AlertsKind, Interval: AlertsInterval, Run: a.run})
}

// Schedule 在啟動時排定：啟用時立即檢查一次（OPS-R13）；停用（alerts.enabled: false）時刪除工作，
// 並安靜地解除由它管理、仍然成立的告警，總覽不會一直顯示停用前的狀態。
func (a *Alerter) Schedule(ctx context.Context) error {
	w := a.Store.Write()
	if a.Cfg.Enabled {
		return w.ScheduleJob(ctx, AlertsKind, AlertsKey, a.Clock.Now())
	}
	if _, err := w.DeleteScheduledJob(ctx, AlertsKind, AlertsKey); err != nil {
		return err
	}
	now := a.Clock.Now()
	return a.Store.Tx(ctx, func(r *store.Repo) error {
		states, err := r.AlertStates(ctx)
		if err != nil {
			return err
		}
		for _, st := range states {
			if st.Active() && isManaged(st.Kind) {
				if err := resolve(ctx, r, a.Outbox, st, "", false, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func isManaged(kind string) bool {
	for _, k := range managedKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (a *Alerter) run(ctx context.Context, _ scheduler.Job, now time.Time) (scheduler.Result, error) {
	return scheduler.Result{}, a.Check(ctx, now)
}

// condition 是一種告警這一次的檢查結果：firing 是成立的 key 與訊息；known 為 false 時這次無法判斷
// （例如讀不到磁碟用量），維持原來的狀態；disabled 為 true 時門檻是 0，成立中的告警安靜地解除。
type condition struct {
	firing   map[string]string
	known    bool
	disabled bool
}

// Check 檢查一次全部的條件並更新告警的狀態：新成立的送一次通知，持續成立的不重複，解除的送一次 resolved。
// 沒有任何 notifier 訂閱 alert 時仍然記錄狀態（OPS-R13）。
func (a *Alerter) Check(ctx context.Context, now time.Time) error {
	rd := a.Store.Read()
	states, err := rd.AlertStates(ctx)
	if err != nil {
		return err
	}
	conds := map[string]condition{}
	var errs []error
	add := func(kind string, c condition, err error) {
		if err != nil {
			a.log().WarnContext(ctx, "告警條件檢查失敗", "kind", kind, "error", err)
			errs = append(errs, fmt.Errorf("%s：%w", kind, err))
			c = condition{}
		}
		conds[kind] = c
	}
	c, err := a.stuckTasks(ctx, rd, now)
	add(KindStuckTask, c, err)
	c, err = a.staleJobs(ctx, rd, now)
	add(KindJobStale, c, err)
	add(KindDiskUsage, a.disk(states), nil)
	c, err = a.staleCookies(ctx, rd, now)
	add(KindCookieStale, c, err)
	c, err = a.ytdlp(ctx, rd, now)
	add(KindYtdlpFailureRate, c, err)

	err = a.Store.Tx(ctx, func(r *store.Repo) error {
		// 在交易中重讀，避免和每日備份同時更新 alert_states 時用到舊的狀態。
		states, err := r.AlertStates(ctx)
		if err != nil {
			return err
		}
		for _, kind := range managedKinds {
			c := conds[kind]
			if !c.known && !c.disabled {
				continue
			}
			for key, msg := range c.firing {
				if err := raise(ctx, r, a.Outbox, stateOf(states, kind, key), kind, key, msg, now); err != nil {
					return err
				}
			}
			for _, st := range states {
				if st.Kind != kind || !st.Active() {
					continue
				}
				if _, still := c.firing[st.Key]; still {
					continue
				}
				if err := resolve(ctx, r, a.Outbox, st, "", !c.disabled, now); err != nil {
					return err
				}
			}
		}
		_, err = r.PruneToolCallStats(ctx, now.Add(-toolStatsRetention))
		return err
	})
	if err != nil {
		return err
	}
	return errors.Join(errs...)
}

// stuckTasks 是 OPS-R14：pending（含等 Agent 領取的 awaiting_agent）或 running 的步驟超過門檻沒有改變狀態。
// 不含 waiting、blocked（查詢只列 pending、running、awaiting_agent），以及 owner 暫停的佇列或資源類別中的步驟。
// 每部影音一則（key 是影音 id），取最先建立而且卡住的步驟。
func (a *Alerter) stuckTasks(ctx context.Context, r *store.Repo, now time.Time) (condition, error) {
	hours := a.Cfg.StuckTaskHours
	if hours <= 0 {
		return condition{disabled: true}, nil
	}
	c := condition{firing: map[string]string{}, known: true}
	if a.Paused != nil && a.Paused() {
		return c, nil
	}
	runs, err := r.ActiveStageRuns(ctx)
	if err != nil {
		return condition{}, err
	}
	limit := time.Duration(hours) * time.Hour
	for _, run := range runs {
		if _, done := c.firing[run.ItemID]; done || a.anyClassPaused(run.Classes) {
			continue
		}
		if now.Sub(StageSince(run)) <= limit {
			continue
		}
		title, err := itemTitle(ctx, r, run.ItemID)
		if err != nil {
			return condition{}, err
		}
		c.firing[run.ItemID] = fmt.Sprintf("「%s」的 %s 超過 %d 小時沒有進展。", title, run.Stage, hours)
	}
	return c, nil
}

// StageSince 推算步驟最後一次改變狀態的時間：running 是最後一次領取（started_at）；排隊中是最新的任務建立的時間，
// 或最晚的重試時間（退避中的任務在那之前本來就不會動）。stage_runs 沒有「狀態改變的時間」欄位，見 OPS 待決問題。
func StageSince(run store.ActiveStageRun) time.Time {
	if run.Status == store.StatusRunning {
		if run.StartedAt != nil {
			return *run.StartedAt
		}
		return run.CreatedAt
	}
	since := run.TaskCreatedAt
	if run.TaskNotBefore != nil && run.TaskNotBefore.After(since) {
		since = *run.TaskNotBefore
	}
	return since
}

func (a *Alerter) anyClassPaused(classes []string) bool {
	if a.ClassPaused == nil {
		return false
	}
	for _, c := range classes {
		if a.ClassPaused(c) {
			return true
		}
	}
	return false
}

// itemTitle 是告警訊息中的影音名稱：標題，沒有標題時是 ref。
func itemTitle(ctx context.Context, r *store.Repo, id string) (string, error) {
	it, err := notify.ItemOf(ctx, r, id)
	if err != nil {
		return "", err
	}
	if t := notify.CleanTitle(it.Title); t != "" {
		return t, nil
	}
	return it.Ref, nil
}

// staleJobs 是 OPS-R15：最近一次失敗、而且最後一次成功（沒有成功過時用建立的時間）早於門檻的排程工作。
// alerts 與 notify.deliver 自己不檢查：通知壞掉時告警也送不出去，由總覽顯示。
func (a *Alerter) staleJobs(ctx context.Context, r *store.Repo, now time.Time) (condition, error) {
	hours := a.Cfg.JobStaleHours
	if hours <= 0 {
		return condition{disabled: true}, nil
	}
	jobs, err := r.FailingScheduledJobs(ctx)
	if err != nil {
		return condition{}, err
	}
	c := condition{firing: map[string]string{}, known: true}
	limit := time.Duration(hours) * time.Hour
	for _, j := range jobs {
		if j.Kind == AlertsKind || j.Kind == notify.DeliverKind {
			continue
		}
		ref := j.CreatedAt
		if j.LastOKAt != nil {
			ref = *j.LastOKAt
		}
		if now.Sub(ref) <= limit {
			continue
		}
		c.firing[j.Kind+":"+j.Key] = fmt.Sprintf("排程 %s 超過 %d 小時沒有成功執行：%s", j.Kind, hours, clip(j.LastError, 200))
	}
	return c, nil
}

// clip 把文字壓成一行、最多 n 個字。
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// disk 是 OPS-R16：資料目錄所在檔案系統的用量大於或等於門檻；成立後要低於門檻減 5 個百分點才解除。
// 讀不到用量時這次不判斷（維持原來的狀態）。
func (a *Alerter) disk(states []store.AlertState) condition {
	limit := a.Cfg.DiskUsagePercent
	if limit <= 0 {
		return condition{disabled: true}
	}
	usage := a.DiskUsage
	if usage == nil {
		usage = DiskUsagePercent
	}
	pct, err := usage(a.DataDir)
	if err != nil {
		a.log().Warn("讀取磁碟用量失敗，這次不判斷磁碟用量告警", "error", err)
		return condition{}
	}
	c := condition{firing: map[string]string{}, known: true}
	st := stateOf(states, KindDiskUsage, DiskKey)
	active := st != nil && st.Active()
	switch {
	case pct >= limit:
		c.firing[DiskKey] = fmt.Sprintf("%s 的磁碟用量是 %d%%，超過 %d%%。", DiskKey, pct, limit)
	case active && pct >= limit-diskHysteresis:
		// 還沒低到解除的程度：沿用成立時的文字。
		c.firing[DiskKey] = st.Message
	}
	return c
}

// platformNames 是 cookie 告警中的平台名稱。
var platformNames = map[string]string{"youtube": "YouTube", "bilibili": "Bilibili"}

// staleCookies 是 OPS-R17：cookie 最後一次成功使用（沒有成功過時用上傳的時間；重新上傳也算）早於門檻。
func (a *Alerter) staleCookies(ctx context.Context, r *store.Repo, now time.Time) (condition, error) {
	days := a.Cfg.CookieStaleDays
	if days <= 0 {
		return condition{disabled: true}, nil
	}
	list, err := r.SecretsFreshness(ctx, CookieKind)
	if err != nil {
		return condition{}, err
	}
	c := condition{firing: map[string]string{}, known: true}
	limit := time.Duration(days) * 24 * time.Hour
	for _, s := range list {
		ref := s.UpdatedAt
		if s.LastOKAt != nil && s.LastOKAt.After(ref) {
			ref = *s.LastOKAt
		}
		if now.Sub(ref) <= limit {
			continue
		}
		name := platformNames[s.Name]
		if name == "" {
			name = s.Name
		}
		c.firing[s.Name] = fmt.Sprintf("%s cookie 超過 %d 天沒有成功使用，可能已經失效。請重新上傳。", name, days)
	}
	return c, nil
}

// ytdlp 是 OPS-R18：最近 1 小時至少 ytdlp_min_calls 次呼叫，而且失敗的比例大於或等於 ytdlp_failure_ratio。
func (a *Alerter) ytdlp(ctx context.Context, r *store.Repo, now time.Time) (condition, error) {
	minCalls, ratio := a.Cfg.YtdlpMinCalls, a.Cfg.YtdlpFailureRatio
	if minCalls <= 0 || ratio <= 0 {
		return condition{disabled: true}, nil
	}
	calls, fails, err := r.ToolCallTotals(ctx, ToolYtDlp, now.Add(-ytdlpWindow))
	if err != nil {
		return condition{}, err
	}
	c := condition{firing: map[string]string{}, known: true}
	if calls >= minCalls && float64(fails) >= ratio*float64(calls) {
		c.firing[ToolYtDlp] = fmt.Sprintf("最近 1 小時 yt-dlp 失敗 %d/%d 次。先更新 yt-dlp；如果仍然失敗，上傳 cookie 或確認 VPS 的 IP 是否被封鎖。", fails, calls)
	}
	return c, nil
}

func (a *Alerter) log() *slog.Logger {
	if a.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return a.Log
}
