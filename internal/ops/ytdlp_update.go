package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// yt-dlp 的每日排程更新（OPS-R6、R7、ADR-0009 第 2 點）：排程工作 tools.yt-dlp.update 每天在設定的時刻
// 呼叫和手動更新相同的流程（安裝 → 自我檢查 → 通過才切換，否則回退）。固定版本時略過。
// 回退或安裝失敗時以告警 ytdlp_update_failed 通知（NOTIFY 的 alert 事件），下一次成功時解除。

// 排程工作的 kind、key 與告警種類。
const (
	YtDlpUpdateKind = "tools.yt-dlp.update"
	YtDlpUpdateKey  = "daily"
	// KindYtdlpUpdateFailed 由這個排程工作自己管理（同 backup_failed），不在 alerts 的 managedKinds 中。
	KindYtdlpUpdateFailed = "ytdlp_update_failed"
)

// YtDlpBusyRetry 是遇到手動更新正在進行（ErrBusy）時延後的時間；不算失敗。
const YtDlpBusyRetry = 30 * time.Minute

// msgYtDlpInstallFailed 是安裝沒有完成時的告警文字（不含 pip 的輸出與路徑，NOTIFY-R9；完整錯誤只寫 log）。
const msgYtDlpInstallFailed = "yt-dlp 排程更新失敗：安裝沒有完成（詳見 server log）。"

// Updater 是 yt-dlp 的更新者（*ytdlp.Manager 實作它）；必須和 API 的手動更新共用同一個，互斥鎖才涵蓋兩者。
type Updater interface {
	Update(ctx context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error)
}

// YtDlpUpdate 是 yt-dlp 的每日排程更新。
type YtDlpUpdate struct {
	Store   *store.Store
	Clock   clock.Clock
	Outbox  Emitter
	Log     *slog.Logger
	Cfg     config.YtDlpUpdate
	Updater Updater
}

// Register 登記排程工作（worker 開始之前呼叫）。
func (u *YtDlpUpdate) Register(sch *scheduler.Scheduler) error {
	return sch.Register(scheduler.Kind{Name: YtDlpUpdateKind, Run: u.run})
}

// Schedule 在啟動時排定下一次，規則同 backup.daily（O15）：停用時刪除工作，並安靜地解除它的告警；
// 已經過了排定的時間（停機錯過）或正在失敗重試時保留；其他情況依目前的時間與時區重排。
func (u *YtDlpUpdate) Schedule(ctx context.Context) error {
	w := u.Store.Write()
	if !u.Cfg.Enabled {
		if _, err := w.DeleteScheduledJob(ctx, YtDlpUpdateKind, YtDlpUpdateKey); err != nil {
			return err
		}
		return u.Store.Tx(ctx, func(r *store.Repo) error {
			return u.resolve(ctx, r, "", false, u.Clock.Now())
		})
	}
	now := u.Clock.Now()
	next, err := NextDaily(now, u.Cfg.At, u.Cfg.Timezone)
	if err != nil {
		return err
	}
	job, err := u.Store.Read().ScheduledJob(ctx, YtDlpUpdateKind, YtDlpUpdateKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return w.ScheduleJob(ctx, YtDlpUpdateKind, YtDlpUpdateKey, next)
	case err != nil:
		return err
	case !job.NextRunAt.After(now) || job.Failures > 0 || job.NextRunAt.Equal(next):
		return nil
	}
	return w.ScheduleJob(ctx, YtDlpUpdateKind, YtDlpUpdateKey, next)
}

// run 執行一次排程更新。結果的對應：
//   - 已固定版本（Skipped）、已經是最新版、更新成功：成功；成功切換時解除告警並通知恢復。
//   - 自我檢查失敗而回退（RolledBack）：告警（OPS-R6 的固定文字），排程本身算成功，明天再試。
//   - 路徑是自行指定的、平台不支援：不是失敗，什麼都不做（否則 job_stale 會一直告警）。
//   - 手動更新正在進行（ErrBusy）：延後 30 分鐘，不算失敗。
//   - 安裝失敗或其他錯誤：告警並回傳錯誤，由排程器退避重試。
func (u *YtDlpUpdate) run(ctx context.Context, _ scheduler.Job, now time.Time) (scheduler.Result, error) {
	next, err := NextDaily(now, u.Cfg.At, u.Cfg.Timezone)
	if err != nil {
		return scheduler.Result{}, err
	}
	res, err := u.Updater.Update(ctx, ytdlp.UpdateRequest{})
	switch {
	case ctx.Err() != nil:
		return scheduler.Result{}, ctx.Err()
	case errors.Is(err, ytdlp.ErrExternallyManaged), errors.Is(err, ytdlp.ErrUnsupportedPlatform):
		u.log().InfoContext(ctx, "yt-dlp 不是受管理的版本，略過排程更新", "reason", err)
		return scheduler.Result{Next: next}, nil
	case errors.Is(err, ytdlp.ErrBusy):
		return scheduler.Result{Next: now.Add(YtDlpBusyRetry), Deferred: true}, nil
	case err != nil:
		u.log().ErrorContext(ctx, "yt-dlp 排程更新失敗", "error", err)
		if aerr := u.Store.Tx(ctx, func(r *store.Repo) error { return u.raise(ctx, r, msgYtDlpInstallFailed, now) }); aerr != nil {
			return scheduler.Result{}, errors.Join(err, aerr)
		}
		return scheduler.Result{}, fmt.Errorf("yt-dlp 排程更新失敗：%w", err)
	}
	switch {
	case res.RolledBack:
		u.log().WarnContext(ctx, "yt-dlp 排程更新後自我檢查失敗，已回退", "version", res.Version, "reason", res.Reason)
		err = u.Store.Tx(ctx, func(r *store.Repo) error { return u.raise(ctx, r, res.Message, now) })
	case res.Skipped:
		u.log().InfoContext(ctx, "yt-dlp 已固定版本，略過排程更新", "pinned", res.Pinned)
	default:
		u.log().InfoContext(ctx, "yt-dlp 排程更新完成", "version", res.Version, "message", res.Message)
		err = u.Store.Tx(ctx, func(r *store.Repo) error { return u.resolve(ctx, r, res.Message, true, now) })
	}
	if err != nil {
		return scheduler.Result{}, err
	}
	return scheduler.Result{Next: next}, nil
}

// raise 讓 ytdlp_update_failed 成立（已成立時只更新文字，不重複通知）。
func (u *YtDlpUpdate) raise(ctx context.Context, r *store.Repo, message string, now time.Time) error {
	states, err := r.AlertStates(ctx)
	if err != nil {
		return err
	}
	return raise(ctx, r, u.Outbox, stateOf(states, KindYtdlpUpdateFailed, YtDlpUpdateKey), KindYtdlpUpdateFailed, YtDlpUpdateKey, message, now)
}

// resolve 解除成立中的 ytdlp_update_failed；notifyUser 為 true 時送「已恢復：<summary>」。
func (u *YtDlpUpdate) resolve(ctx context.Context, r *store.Repo, summary string, notifyUser bool, now time.Time) error {
	states, err := r.AlertStates(ctx)
	if err != nil {
		return err
	}
	st := stateOf(states, KindYtdlpUpdateFailed, YtDlpUpdateKey)
	if st == nil || !st.Active() {
		return nil
	}
	return resolve(ctx, r, u.Outbox, *st, summary, notifyUser, now)
}

func (u *YtDlpUpdate) log() *slog.Logger {
	if u.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return u.Log
}
