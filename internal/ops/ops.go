// Package ops 是 M4 的無人值守運作（OPS-R11～R21）：每日備份（backup.daily）、無人值守告警（alerts）、
// 外部工具的呼叫統計與 VPS 部署檢查（deploy-check）。
//
// 備份與告警都是排程工作（internal/scheduler），不進佇列（ADR-0021 第 6 點）；告警的狀態存在 alert_states，
// 經 NOTIFY 的 outbox 送出 alert 事件，與觸發的狀態變更寫在同一個交易中。
package ops

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/store"
)

// 排程工作（scheduled_jobs 的 kind 與固定的 key）。
const (
	BackupKind = "backup.daily"
	BackupKey  = "daily"
	AlertsKind = "alerts"
	AlertsKey  = "check"
)

// AlertsInterval 是 alerts 的檢查間隔（OPS-R13）。
const AlertsInterval = 15 * time.Minute

// 告警的種類（alert 事件的 alert.kind）。
const (
	KindBackupFailed     = "backup_failed"
	KindStuckTask        = "stuck_task"
	KindJobStale         = "job_stale"
	KindDiskUsage        = "disk_usage"
	KindCookieStale      = "cookie_stale"
	KindYtdlpFailureRate = "ytdlp_failure_rate"
)

// DiskKey 是磁碟用量告警的對象：資料目錄所在的檔案系統，一律以容器中的名稱 /data 表示（OPS-R16）。
const DiskKey = "/data"

// Emitter 寫入通知的 outbox（*notify.Outbox 實作它；nil 的 *notify.Outbox 不寫入任何東西）。
type Emitter interface {
	Emit(ctx context.Context, r *store.Repo, ev notify.Event) (int, error)
}

// raise 在 r 的交易中讓 (kind, key) 的告警成立並送出一次通知；已經成立時只更新文字，不重複通知（OPS-R13）。
// active 是目前的狀態（沒有時為 nil）。
func raise(ctx context.Context, r *store.Repo, out Emitter, active *store.AlertState, kind, key, message string, now time.Time) error {
	if active != nil && active.Active() {
		if active.Message == message {
			return nil
		}
		return r.SetAlertMessage(ctx, kind, key, message)
	}
	if err := r.ActivateAlert(ctx, kind, key, message, now); err != nil {
		return err
	}
	ev := notify.Event{
		Name: notify.EventAlert, DedupeKey: notify.DedupeAlert(kind, key, false), Message: message,
		Alert: &notify.Alert{Kind: kind, Key: key, Message: message, Since: now},
	}
	return emit(ctx, r, out, ev, kind, key, now)
}

// resolve 在 r 的交易中解除成立中的告警；notifyUser 為 true 時送出一次 resolved（文字管道的標題「Notesteme 已恢復：<摘要>」）。
// summary 是恢復的摘要；空白時用告警原本的文字。
func resolve(ctx context.Context, r *store.Repo, out Emitter, st store.AlertState, summary string, notifyUser bool, now time.Time) error {
	ok, err := r.ResolveAlert(ctx, st.Kind, st.Key, now)
	if err != nil || !ok || !notifyUser {
		return err
	}
	if summary == "" {
		summary = st.Message
	}
	msg := "已恢復：" + summary
	ev := notify.Event{
		Name: notify.EventAlert, DedupeKey: notify.DedupeAlert(st.Kind, st.Key, true), Message: msg,
		Alert: &notify.Alert{Kind: st.Kind, Key: st.Key, Message: msg, Since: st.ActiveSince, Resolved: true, Summary: summary},
	}
	return emit(ctx, r, out, ev, st.Kind, st.Key, now)
}

func emit(ctx context.Context, r *store.Repo, out Emitter, ev notify.Event, kind, key string, now time.Time) error {
	if out == nil {
		return nil
	}
	n, err := out.Emit(ctx, r, ev)
	if err != nil || n == 0 {
		return err
	}
	return r.MarkAlertNotified(ctx, kind, key, now)
}

// stateOf 回傳 states 中 (kind, key) 的狀態；沒有時為 nil。
func stateOf(states []store.AlertState, kind, key string) *store.AlertState {
	for i := range states {
		if states[i].Kind == kind && states[i].Key == key {
			return &states[i]
		}
	}
	return nil
}
