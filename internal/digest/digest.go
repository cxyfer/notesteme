// Package digest 是訂閱彙整（DIG-R8～R11）：彙整設定、排程工作 digest、範圍與內容，經 NOTIFY 的 outbox 送出。
//
//   - 每個彙整是一個排程工作（scheduled_jobs.kind = digest，key 是 id），在該時區的 at 執行，不加隨機延遲；
//   - 範圍是 (上次涵蓋的終點, 這次排定的時間]：以排定的時間計算，停機後補跑一次時範圍仍然連續；
//   - 寫入 digest_entries、last_run_at 與 outbox 在同一個寫入交易（NOTIFY-R3）；沒有任何影音時不送出。
package digest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// Kind 是排程工作的 kind（scheduled_jobs.kind），key 是彙整的 id。
const Kind = "digest"

// 範圍（DIG-R8）。
const (
	ScopeSubscriptions = "subscriptions"
	ScopeAll           = "all"
	defaultAt          = "08:00"
	defaultTimezone    = "UTC"
	maxNotifiers       = 5
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// 錯誤。
var (
	ErrNotFound = errors.New("找不到這個彙整。")
	ErrExists   = errors.New("已經有同名的彙整。")
)

// InvalidError 是 422 的逐欄位錯誤。
type InvalidError struct{ Fields []validate.FieldError }

func (e *InvalidError) Error() string { return "彙整的設定不正確。" }

// Summaries 讀取影音現行的摘要（*pipeline.Pipeline 實作它）。
type Summaries interface {
	ItemSummary(ctx context.Context, itemID string) (pipeline.Summary, bool, error)
}

// Deps 是 Service 的依賴。
type Deps struct {
	Store     *store.Store
	Clock     clock.Clock
	Outbox    *notify.Outbox
	Summaries Summaries
	// PublicURL 是 server.public_url（結尾不含 /）；空字串代表沒有設定（DIG-R10）。
	PublicURL string
	Log       *slog.Logger
}

// Service 管理彙整設定並執行排程工作。
type Service struct{ d Deps }

// New 建立 Service。
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{d: d}
}

// DigestInfo 是 API 回傳的彙整設定（DIG-R8）。
type DigestInfo struct {
	ID        string     `json:"id"`
	Period    string     `json:"period"`
	At        string     `json:"at"`
	Weekday   *string    `json:"weekday"`
	Timezone  string     `json:"timezone"`
	Notifiers []string   `json:"notifiers"`
	Scope     string     `json:"scope"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
	LastRunAt *time.Time `json:"last_run_at"`
	NextRunAt *time.Time `json:"next_run_at"`
}

// Input 是 POST /api/digests 與 PATCH /api/digests/:id 的欄位；nil 代表沒有給（建立時用預設值，修改時不變）。
type Input struct {
	ID        string
	Period    *string
	At        *string
	Weekday   *string
	Timezone  *string
	Notifiers *[]string
	Scope     *string
	Enabled   *bool
}

func info(d store.Digest, next *time.Time) DigestInfo {
	out := DigestInfo{
		ID: d.ID, Period: d.Period, At: d.At, Timezone: d.Timezone, Notifiers: d.Notifiers, Scope: d.Scope,
		Enabled: d.Enabled, CreatedAt: d.CreatedAt, LastRunAt: d.LastRunAt, NextRunAt: next,
	}
	if d.Weekday != "" {
		w := d.Weekday
		out.Weekday = &w
	}
	return out
}

// apply 把 in 套用到 d（建立時 d 是預設值）。
func apply(d *store.Digest, in Input) {
	if in.Period != nil {
		d.Period = *in.Period
	}
	if in.At != nil {
		d.At = *in.At
	}
	if in.Weekday != nil {
		d.Weekday = *in.Weekday
	} else if in.Period != nil && *in.Period == PeriodDaily {
		d.Weekday = "" // 改成每日時不再需要 weekday
	}
	if in.Timezone != nil {
		d.Timezone = *in.Timezone
	}
	if in.Notifiers != nil {
		d.Notifiers = slices.Clone(*in.Notifiers)
	}
	if in.Scope != nil {
		d.Scope = *in.Scope
	}
	if in.Enabled != nil {
		d.Enabled = *in.Enabled
	}
}

// check 驗證彙整設定（DIG-R8），notifier 在交易 r 中查詢。
func check(ctx context.Context, r *store.Repo, d store.Digest) ([]validate.FieldError, error) {
	var fs []validate.FieldError
	add := func(path, msg string) { fs = append(fs, validate.FieldError{Path: path, Message: msg}) }
	if !idPattern.MatchString(d.ID) {
		add("id", "id 只能用小寫英文字母、數字與 -，以英文字母或數字開頭，最多 40 個字元。")
	}
	if d.Period != PeriodDaily && d.Period != PeriodWeekly {
		add("period", "period 必須是 daily 或 weekly。")
	}
	if _, _, ok := parseClock(d.At); !ok {
		add("at", "at 必須是 HH:MM（00:00～23:59）。")
	}
	switch _, known := weekdays[d.Weekday]; {
	case d.Period == PeriodWeekly && !known:
		add("weekday", "每週彙整必須指定 weekday（mon～sun）。")
	case d.Period == PeriodDaily && d.Weekday != "":
		add("weekday", "weekday 只能用在每週彙整。")
	}
	if _, err := loadZone(d.Timezone); err != nil {
		add("timezone", fmt.Sprintf("不認得的時區：%s。", clip(d.Timezone, 64)))
	}
	if d.Scope != ScopeSubscriptions && d.Scope != ScopeAll {
		add("scope", "scope 必須是 subscriptions 或 all。")
	}
	switch n := len(d.Notifiers); {
	case n < 1 || n > maxNotifiers:
		add("notifiers", fmt.Sprintf("notifiers 必須是 1～%d 個 notifier 的 id。", maxNotifiers))
	default:
		seen := map[string]bool{}
		for _, id := range d.Notifiers {
			if seen[id] {
				add("notifiers", "notifiers 不能重複。")
				break
			}
			seen[id] = true
			n, err := r.Notifier(ctx, id)
			if errors.Is(err, store.ErrNotFound) {
				add("notifiers", fmt.Sprintf("找不到 notifier %s。", clip(id, 40)))
				break
			}
			if err != nil {
				return nil, err
			}
			if !slices.Contains(n.Events, notify.EventDigest) {
				add("notifiers", fmt.Sprintf("notifier %s 沒有勾選「訂閱彙整」。", id))
				break
			}
		}
	}
	return fs, nil
}

// clip 截斷放進錯誤訊息的不可信字串。
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func scheduleOf(d store.Digest) (schedule, error) {
	return newSchedule(d.Period, d.At, d.Weekday, d.Timezone)
}

// Create 建立彙整設定並排定第一次執行（DIG-R8）。
func (s *Service) Create(ctx context.Context, in Input) (DigestInfo, error) {
	d := store.Digest{ID: in.ID, At: defaultAt, Timezone: defaultTimezone, Scope: ScopeSubscriptions, Enabled: true}
	apply(&d, in)
	var out DigestInfo
	err := s.d.Store.Tx(ctx, func(r *store.Repo) error {
		fs, err := check(ctx, r, d)
		if err != nil {
			return err
		}
		if len(fs) > 0 {
			return &InvalidError{Fields: fs}
		}
		switch _, err := r.Digest(ctx, d.ID); {
		case err == nil:
			return ErrExists
		case !errors.Is(err, store.ErrNotFound):
			return err
		}
		if err := r.InsertDigest(ctx, d); err != nil {
			return err
		}
		next, err := s.reschedule(ctx, r, d)
		if err != nil {
			return err
		}
		created, err := r.Digest(ctx, d.ID)
		out = info(created, next)
		return err
	})
	return out, err
}

// reschedule 依設定排定下一次執行；停用時刪除排程工作。回傳下一次執行的時間（停用時是 nil）。
func (s *Service) reschedule(ctx context.Context, r *store.Repo, d store.Digest) (*time.Time, error) {
	if !d.Enabled {
		_, err := r.DeleteScheduledJob(ctx, Kind, d.ID)
		return nil, err
	}
	sch, err := scheduleOf(d)
	if err != nil {
		return nil, err
	}
	next := sch.next(s.d.Clock.Now())
	return &next, r.ScheduleJob(ctx, Kind, d.ID, next)
}

// nextRun 讀出排定的下一次執行。
func nextRun(ctx context.Context, r *store.Repo, id string) (*time.Time, error) {
	j, err := r.ScheduledJob(ctx, Kind, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t := j.NextRunAt
	return &t, nil
}

// Get 回傳一個彙整設定。
func (s *Service) Get(ctx context.Context, id string) (DigestInfo, error) {
	r := s.d.Store.Read()
	d, err := r.Digest(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return DigestInfo{}, ErrNotFound
	}
	if err != nil {
		return DigestInfo{}, err
	}
	next, err := nextRun(ctx, r, id)
	return info(d, next), err
}

// List 依建立順序列出彙整設定。
func (s *Service) List(ctx context.Context) ([]DigestInfo, error) {
	r := s.d.Store.Read()
	list, err := r.ListDigests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DigestInfo, 0, len(list))
	for _, d := range list {
		next, err := nextRun(ctx, r, d.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, info(d, next))
	}
	return out, nil
}

// Patch 修改彙整設定：只改給定的欄位，整份重新驗證。排程相關的欄位或 enabled 改變時重新排定下一次執行；
// 從停用改回啟用時範圍重新起算（不會把停用期間的影音一次送出）。
func (s *Service) Patch(ctx context.Context, id string, in Input) (DigestInfo, error) {
	var out DigestInfo
	err := s.d.Store.Tx(ctx, func(r *store.Repo) error {
		cur, err := r.Digest(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		d := cur
		d.Notifiers = slices.Clone(cur.Notifiers)
		apply(&d, in)
		fs, err := check(ctx, r, d)
		if err != nil {
			return err
		}
		if len(fs) > 0 {
			return &InvalidError{Fields: fs}
		}
		if d.Enabled && !cur.Enabled {
			d.LastRunAt = nil
		}
		if err := r.UpdateDigest(ctx, d); err != nil {
			return err
		}
		next, err := nextRun(ctx, r, id)
		if err != nil {
			return err
		}
		changed := d.Period != cur.Period || d.At != cur.At || d.Weekday != cur.Weekday || d.Timezone != cur.Timezone || d.Enabled != cur.Enabled
		if changed || (d.Enabled && next == nil) {
			if next, err = s.reschedule(ctx, r, d); err != nil {
				return err
			}
		}
		out = info(d, next)
		return nil
	})
	return out, err
}

// Delete 刪除彙整設定、它的 digest_entries 與排程工作。
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.d.Store.Tx(ctx, func(r *store.Repo) error {
		ok, err := r.DeleteDigest(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		_, err = r.DeleteScheduledJob(ctx, Kind, id)
		return err
	})
}

// Register 登記排程工作 digest（worker 開始之前呼叫）。
func (s *Service) Register(sch *scheduler.Scheduler) error {
	return sch.Register(scheduler.Kind{Name: Kind, Run: s.run})
}
