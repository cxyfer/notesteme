package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/ops"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// registerOps 登記每日備份（backup.daily，OPS-R11、R12）與無人值守告警（alerts，OPS-R13～R18），並排定第一次執行。
// 只有 serve 呼叫：standalone 的 CLI 是短命的程序，不自動備份也不檢查告警。
func (s *Server) registerOps(ctx context.Context, env Env, cfg *config.Config, log *slog.Logger, outbox *notify.Outbox) error {
	bc := cfg.M4.DailyBackup
	if bc.Dir == "" {
		bc.Dir = filepath.Join(cfg.Data, "backups")
	}
	daily := &ops.Daily{
		Store: s.store, Clock: env.Clock, Outbox: outbox, Log: log, Cfg: bc,
		Begin: func(ctx context.Context, now time.Time) (ops.Archive, error) {
			pb, err := s.beginBackup(ctx, now)
			if err != nil {
				return nil, err
			}
			return pb, nil
		},
	}
	ctl := s.queue.Control()
	alerts := &ops.Alerter{
		Store: s.store, Clock: env.Clock, Outbox: outbox, Log: log, Cfg: cfg.M4.Alerts, DataDir: cfg.Data,
		Paused: ctl.Paused, ClassPaused: ctl.ClassPaused,
	}
	if env.Test != nil && env.Test.DiskUsage != nil {
		alerts.DiskUsage = env.Test.DiskUsage
	}
	// yt-dlp 的每日排程更新（OPS-R6、R7）：和手動更新共用同一個 Manager（互斥鎖涵蓋兩者）。
	uc := cfg.M4.YtDlpUpdate
	if env.Test != nil && env.Test.NoYtDlpAutoUpdate && !uc.EnabledSet {
		uc.Enabled = false
	}
	ytdlpUpdate := &ops.YtDlpUpdate{
		Store: s.store, Clock: env.Clock, Outbox: outbox, Log: log, Cfg: uc,
		Updater: rootedUpdater{m: s.ytdlpManager(env), withRoot: s.withRoot},
	}
	if err := daily.Register(s.scheduler); err != nil {
		return err
	}

	if err := alerts.Register(s.scheduler); err != nil {
		return err
	}
	if err := ytdlpUpdate.Register(s.scheduler); err != nil {
		return err
	}
	// 排定失敗（例如測試以較舊的 schema 啟動、沒有 scheduled_jobs）只記 log，不擋 serve 啟動：
	// 備份與告警是附加的維運功能，不該讓服務本身起不來。
	if err := daily.Schedule(ctx); err != nil {
		log.Error("無法排定每日備份", "err", err)
	}
	if err := alerts.Schedule(ctx); err != nil {
		log.Error("無法排定系統告警檢查", "err", err)
	}
	if err := ytdlpUpdate.Schedule(ctx); err != nil {
		log.Error("無法排定 yt-dlp 的排程更新", "err", err)
	}
	return nil
}

// rootedUpdater 讓排程更新在 server 關閉（root 取消）時也一起取消，新的 venv 才會被清掉（同 toolManager）。
type rootedUpdater struct {
	m        *ytdlp.Manager
	withRoot func(context.Context) (context.Context, func())
}

func (u rootedUpdater) Update(ctx context.Context, req ytdlp.UpdateRequest) (*ytdlp.Result, error) {
	ctx, stop := u.withRoot(ctx)
	defer stop()
	return u.m.Update(ctx, req)
}

// SetCookieUsedAt 把平台 cookie 的上傳時間與最近一次成功使用都設為 at（OPS-R17）。只供驗收測試的 harness 使用。
func (s *Server) SetCookieUsedAt(ctx context.Context, platform string, at time.Time) error {
	return s.store.Write().SetSecretTimes(ctx, ops.CookieKind, platform, at)
}

// SetToolCalls 寫入 at 所在時間格的工具呼叫統計（OPS-R18）。只供驗收測試的 harness 使用。
func (s *Server) SetToolCalls(ctx context.Context, tool string, at time.Time, calls, failures int) error {
	return s.store.Write().SetToolCalls(ctx, tool, at, calls, failures)
}

// opsCalls 是 yt-dlp 呼叫的記錄器（OPS-R17、R18）。
func (s *Server) opsCalls(env Env, log *slog.Logger) *ops.Calls {
	return &ops.Calls{Store: s.store, Clock: env.Clock, Log: log}
}
