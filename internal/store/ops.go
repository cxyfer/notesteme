package store

import (
	"context"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// AlertState 是 alert_states 的一列（OPS-R13）：ResolvedAt 為 nil 時告警仍然成立。
type AlertState struct {
	Kind        string
	Key         string
	Message     string
	ActiveSince time.Time
	NotifiedAt  *time.Time
	ResolvedAt  *time.Time
}

// Active 回報告警是否仍然成立。
func (a AlertState) Active() bool { return a.ResolvedAt == nil }

// AlertStates 列出所有告警的狀態：成立中的在前，再依開始的時間排序。
func (r *Repo) AlertStates(ctx context.Context) ([]AlertState, error) {
	rows, err := r.q.ListAlertStates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AlertState, 0, len(rows))
	for _, row := range rows {
		out = append(out, AlertState{
			Kind: row.Kind, Key: row.Key, Message: row.Message, ActiveSince: fromMs(row.ActiveSince),
			NotifiedAt: msToTime(row.NotifiedAt), ResolvedAt: msToTime(row.ResolvedAt),
		})
	}
	return out, nil
}

// ActivateAlert 讓 (kind, key) 的告警從 since 開始成立；已解除的列重新開始（清掉通知與解除的時間）。
func (r *Repo) ActivateAlert(ctx context.Context, kind, key, message string, since time.Time) error {
	return r.q.ActivateAlert(ctx, sqlcdb.ActivateAlertParams{Kind: kind, Key: key, Message: message, Since: since.UnixMilli()})
}

// SetAlertMessage 只更新告警的文字（條件仍然成立，例如磁碟用量的百分比改變）。
func (r *Repo) SetAlertMessage(ctx context.Context, kind, key, message string) error {
	return r.q.SetAlertMessage(ctx, sqlcdb.SetAlertMessageParams{Kind: kind, Key: key, Message: message})
}

// MarkAlertNotified 記錄告警送出通知的時間。
func (r *Repo) MarkAlertNotified(ctx context.Context, kind, key string, at time.Time) error {
	return r.q.MarkAlertNotified(ctx, sqlcdb.MarkAlertNotifiedParams{Kind: kind, Key: key, At: msPtr(&at)})
}

// ResolveAlert 解除成立中的告警；回報是否真的解除了一列。
func (r *Repo) ResolveAlert(ctx context.Context, kind, key string, at time.Time) (bool, error) {
	n, err := r.q.ResolveAlert(ctx, sqlcdb.ResolveAlertParams{Kind: kind, Key: key, At: msPtr(&at)})
	return n > 0, err
}

// ToolCallBucket 是工具呼叫統計的時間格（OPS-R18：5 分鐘）。
const ToolCallBucket = 5 * time.Minute

// bucketStart 回傳 at 所在時間格的開頭（Unix 毫秒）。
func bucketStart(at time.Time) int64 {
	ms := ToolCallBucket.Milliseconds()
	return at.UnixMilli() / ms * ms
}

// AddToolCall 在 at 所在的時間格記一次呼叫；failed 時同時記一次失敗。
func (r *Repo) AddToolCall(ctx context.Context, tool string, at time.Time, failed bool) error {
	var f int64
	if failed {
		f = 1
	}
	return r.q.AddToolCall(ctx, sqlcdb.AddToolCallParams{Tool: tool, BucketStart: bucketStart(at), Failures: f})
}

// SetToolCalls 直接寫入 at 所在時間格的呼叫數與失敗數（測試 hook）。
func (r *Repo) SetToolCalls(ctx context.Context, tool string, at time.Time, calls, failures int) error {
	return r.q.SetToolCalls(ctx, sqlcdb.SetToolCallsParams{Tool: tool, BucketStart: bucketStart(at), Calls: int64(calls), Failures: int64(failures)})
}

// ToolCallTotals 回傳 tool 在 since 之後開始的時間格中的呼叫數與失敗數。
func (r *Repo) ToolCallTotals(ctx context.Context, tool string, since time.Time) (calls, failures int, err error) {
	row, err := r.q.ToolCallTotals(ctx, sqlcdb.ToolCallTotalsParams{Tool: tool, Since: since.UnixMilli()})
	return int(row.Calls), int(row.Failures), err
}

// PruneToolCallStats 刪除 before 之前開始的時間格，回傳刪除的筆數。
func (r *Repo) PruneToolCallStats(ctx context.Context, before time.Time) (int64, error) {
	return r.q.PruneToolCallStats(ctx, before.UnixMilli())
}

// MarkSecretOK 記錄 (kind, name) 的 secret 最近一次成功使用的時間（OPS-R17）。
func (r *Repo) MarkSecretOK(ctx context.Context, kind, name string, at time.Time) error {
	return r.q.MarkSecretOK(ctx, sqlcdb.MarkSecretOKParams{Kind: kind, Name: name, At: msPtr(&at)})
}

// SetSecretTimes 把 secret 的上傳時間與最近一次成功使用都設為 at（測試 hook）；不存在時回傳 ErrNotFound。
func (r *Repo) SetSecretTimes(ctx context.Context, kind, name string, at time.Time) error {
	n, err := r.q.SetSecretTimes(ctx, sqlcdb.SetSecretTimesParams{Kind: kind, Name: name, At: at.UnixMilli()})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// SecretFreshness 是 secret 的上傳時間與最近一次成功使用的時間（沒有成功過時為 nil）。
type SecretFreshness struct {
	Name      string
	UpdatedAt time.Time
	LastOKAt  *time.Time
}

// SecretsFreshness 列出 kind 的 secret 的上傳時間與最近一次成功使用的時間，不含密文。
func (r *Repo) SecretsFreshness(ctx context.Context, kind string) ([]SecretFreshness, error) {
	rows, err := r.q.SecretFreshness(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := make([]SecretFreshness, 0, len(rows))
	for _, row := range rows {
		out = append(out, SecretFreshness{Name: row.Name, UpdatedAt: fromMs(row.UpdatedAt), LastOKAt: msToTime(row.LastOkAt)})
	}
	return out, nil
}

// FailingScheduledJobs 列出最近一次執行失敗的排程工作（OPS-R15）。
func (r *Repo) FailingScheduledJobs(ctx context.Context) ([]ScheduledJob, error) {
	rows, err := r.q.FailingScheduledJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, scheduledJobFromRow(row))
	}
	return out, nil
}

// ActiveStageRun 是排隊中或執行中、還有沒完成任務的步驟（OPS-R14）。
type ActiveStageRun struct {
	ID        string
	ItemID    string
	Stage     string
	Status    string
	StartedAt *time.Time
	CreatedAt time.Time
	// TaskCreatedAt 是最新的未完成任務建立的時間；TaskNotBefore 是最晚的重試時間（沒有時為 nil）。
	TaskCreatedAt time.Time
	TaskNotBefore *time.Time
	// Classes 是任務的資源類別。
	Classes []string
}

// ActiveStageRuns 列出排隊中或執行中、還有沒完成任務的步驟（不含垃圾桶中的影音），依影音與建立順序。
func (r *Repo) ActiveStageRuns(ctx context.Context) ([]ActiveStageRun, error) {
	rows, err := r.q.ActiveStageRuns(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ActiveStageRun, 0, len(rows))
	for _, row := range rows {
		a := ActiveStageRun{
			ID: row.ID, ItemID: row.ItemID, Stage: row.Stage, Status: row.Status,
			StartedAt: msToTime(row.StartedAt), CreatedAt: fromMs(row.CreatedAt), TaskCreatedAt: fromMs(row.TaskCreatedAt),
		}
		if row.TaskNotBefore > 0 {
			t := fromMs(row.TaskNotBefore)
			a.TaskNotBefore = &t
		}
		if row.Classes != "" {
			a.Classes = strings.Split(row.Classes, ",")
		}
		out = append(out, a)
	}
	return out, nil
}
