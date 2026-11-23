package app

import (
	"context"
	"errors"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/ops"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// systemTools 是 GET /api/system 列出的外部工具。
var systemTools = []toolexec.Spec{toolexec.YtDlp, toolexec.FFmpeg, toolexec.FFprobe, toolexec.Deno}

// systemInfo 回傳 GET /api/system 的提供者。env 在這裡傳入，讓實作不必再改 core.go 的 apiDeps。
func (s *Server) systemInfo(env Env) api.SystemInfo {
	return systemInfo{cfg: s.cfg, res: ToolResolver(env.Vars, s.cfg), ytdlp: s.ytdlpManager(env), store: s.store}
}

type systemInfo struct {
	cfg   *config.Config
	res   toolexec.Resolver
	ytdlp *ytdlp.Manager
	store *store.Store
}

// opsStatus 填入成立中的告警與每日備份的狀態（OPS-R11、R13）。
func (i systemInfo) opsStatus(ctx context.Context, v *api.SystemView) error {
	v.Alerts = []api.AlertView{}
	if i.store == nil {
		return nil
	}
	states, err := i.store.Read().AlertStates(ctx)
	if err != nil {
		return err
	}
	for _, st := range states {
		if st.Active() {
			v.Alerts = append(v.Alerts, api.AlertView{Kind: st.Kind, Key: st.Key, Message: st.Message, Since: st.ActiveSince})
		}
	}
	job, err := i.store.Read().ScheduledJob(ctx, ops.BackupKind, ops.BackupKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	next := job.NextRunAt
	v.Backup = api.BackupView{LastOKAt: job.LastOKAt, NextRunAt: &next}
	if job.LastError != "" {
		v.Backup.LastError = api.Str(job.LastError)
	}
	return nil
}

// Info 組出生效的設定與其來源、每個外部工具實際會用到的路徑與來源（OPS-R8）。
// 設定檔或環境變數指定的路徑不能執行時，仍然回報該路徑與來源，available 為 false。
func (i systemInfo) Info(ctx context.Context) (*api.SystemView, error) {
	v := &api.SystemView{
		Listen: i.cfg.Listen, ListenSource: string(i.cfg.SourceOf(config.FieldListen)),
		Data: i.cfg.Data, DataSource: string(i.cfg.SourceOf(config.FieldData)),
		Tools: make(map[string]api.ToolView, len(systemTools)),
	}
	for _, sp := range systemTools {
		loc := i.res.Locate(sp)
		tv := api.ToolView{Path: loc.Path, Source: string(loc.Source), Available: loc.Available()}
		if loc.Err != nil {
			tv.Error = loc.Err.Error()
		}
		v.Tools[sp.Name] = tv
	}
	if err := i.opsStatus(ctx, v); err != nil {
		return nil, err
	}
	// state.json 壞掉不能讓整個 /api/system 失敗（它正是用來診斷的）：yt_dlp 改回 null 加上說明，其他欄位照常。
	st, err := i.ytdlp.State()
	if err != nil {
		v.YtDlp = api.YtDlpView{Error: err.Error()}
		return v, nil
	}
	v.YtDlp = api.YtDlpView{Pinned: api.Str(st.Pinned), Previous: api.Str(st.Previous)}
	return v, nil
}
