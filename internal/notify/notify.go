// Package notify 是通知（NOTIFY，ADR-0021 第 5 點）：notifier 的設定、outbox 與送出。
//
//   - 其他領域在觸發事件的那個交易中呼叫 Outbox.Emit（同交易寫入 notification_outbox，NOTIFY-R3）；
//   - 排程工作 notify.deliver（Service.Deliver）取出到期的項目，在交易外送出，再寫回結果；
//   - 所有請求經過 netguard 的 client（NewClient），不跟隨轉址；
//   - notifier 的設定整份以資料金鑰加密（SEC-R4），API 只回傳非機密欄位與末四碼（NOTIFY-R6）。
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// MaxNotifiers 是 notifier 的數量上限（NOTIFY-R1）。
const MaxNotifiers = 20

// ListLimit 是 GET /api/notifications 回傳的筆數（NOTIFY-R8）。
const ListLimit = 100

// retention 是 sent 與 dropped 項目的保留期間（NOTIFY-R8）。
const retention = 30 * 24 * time.Hour

// backoff 是第 n 次失敗之後的等待時間（NOTIFY-R8）；第 len(backoff)+1 次失敗後 dropped。
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// MaxAttempts 是 dropped 之前最多嘗試的次數。
var MaxAttempts = len(backoff) + 1

// deliverEvery 是 notify.deliver 最晚的下一次執行（規格的「每分鐘」）：並行的 Emit 被覆寫時最多慢一分鐘。
const deliverEvery = time.Minute

// maxDuePerRun 是一次 notify.deliver 最多處理的項目數。
const maxDuePerRun = 50

// secretKind 是 notifier 設定在資料金鑰加密時的 kind（AAD 為 notifier/<id>）。
const secretKind = "notifier"

// 錯誤。
var (
	ErrNotFound    = errors.New("找不到這個 notifier。")
	ErrExists      = errors.New("已經有同名的 notifier。")
	ErrTooMany     = fmt.Errorf("notifier 最多 %d 個。", MaxNotifiers)
	errNoKeys      = errors.New("notify: 沒有資料金鑰")
	errSecretsGone = errors.New("notify: 無法解開 notifier 的設定")
)

// InvalidError 是 422 的逐欄位錯誤。
type InvalidError struct{ Fields []validate.FieldError }

func (e *InvalidError) Error() string { return "notifier 的設定不正確。" }

// Sealer 是資料金鑰的加解密（*secrets.Keyring）。
type Sealer interface {
	Seal(kind, name string, plaintext []byte) (nonce, ciphertext []byte, err error)
	Open(kind, name string, nonce, ciphertext []byte) ([]byte, error)
}

// Deps 是 Service 的依賴。
type Deps struct {
	Store *store.Store
	Keys  Sealer
	Clock clock.Clock
	Log   *slog.Logger
	// HTTP 是 NewClient 建立的 client（netguard、不跟隨轉址、15 秒逾時）。
	HTTP    *http.Client
	Hosts   HostPolicy
	Version string
	// PublicURL 是 server.public_url（結尾不含 /）；空字串代表沒有設定。
	PublicURL string
	// RateCount、RatePer 是 notify.rate_limit（NOTIFY-R11）；0 代表預設 20 則／10 分鐘。
	RateCount int
	RatePer   time.Duration
}

// Service 管理 notifier 與送出 outbox。
type Service struct{ d Deps }

// New 建立 Service。
func New(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.RateCount <= 0 {
		d.RateCount = 20
	}
	if d.RatePer <= 0 {
		d.RatePer = 10 * time.Minute
	}
	return &Service{d: d}
}

// Info 是 API 回傳的 notifier（不含任何密鑰）。
type Info struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Config    map[string]any `json:"config"`
	Events    []string       `json:"events"`
	Enabled   bool           `json:"enabled"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func toInfo(n store.Notifier) Info {
	cfg := map[string]any{}
	_ = json.Unmarshal([]byte(n.PublicJSON), &cfg)
	return Info{ID: n.ID, Kind: n.Kind, Config: cfg, Events: n.Events, Enabled: n.Enabled, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt}
}

// CreateInput 是 POST /api/notifiers。
type CreateInput struct {
	ID      string
	Kind    string
	Config  map[string]any
	Events  []string
	Enabled *bool
}

// PatchInput 是 PATCH /api/notifiers/:id：nil 的欄位不變；Config 只覆寫給定的鍵，沒給的密鑰保留原值（NOTIFY-R6）。
type PatchInput struct {
	Config  map[string]any
	Events  []string
	Enabled *bool
}

func (s *Service) seal(id string, cfg Config) (nonce, ct []byte, err error) {
	if s.d.Keys == nil {
		return nil, nil, errNoKeys
	}
	plain, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, err
	}
	return s.d.Keys.Seal(secretKind, id, plain)
}

// open 解開 notifier 的設定；資料金鑰被重設（SEC-R9）時回傳 errSecretsGone。
func (s *Service) open(n store.Notifier) (Config, error) {
	if s.d.Keys == nil {
		return nil, errNoKeys
	}
	plain, err := s.d.Keys.Open(secretKind, n.ID, n.Nonce, n.Ciphertext)
	if err != nil {
		return nil, errSecretsGone
	}
	var cfg Config
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return nil, errSecretsGone
	}
	return cfg, nil
}

// Create 新增 notifier（NOTIFY-R1～R3、R6）。
func (s *Service) Create(ctx context.Context, in CreateInput) (Info, error) {
	var fields []validate.FieldError
	if !ValidID(in.ID) {
		fields = append(fields, validate.FieldError{Path: "id", Message: "id 只能使用小寫英文字母、數字與 -，以字母或數字開頭，最多 40 字元。"})
	}
	var cfg Config
	switch {
	case !contains(Kinds, in.Kind):
		fields = append(fields, validate.FieldError{Path: "kind", Message: fmt.Sprintf("不支援的通知種類：%s。", in.Kind)})
	default:
		var errs []validate.FieldError
		cfg, errs = ValidateConfig(in.Kind, in.Config, s.d.Hosts)
		fields = append(fields, errs...)
	}
	fields = append(fields, ValidateEvents(in.Events)...)
	if len(fields) > 0 {
		return Info{}, &InvalidError{Fields: fields}
	}
	nonce, ct, err := s.seal(in.ID, cfg)
	if err != nil {
		return Info{}, err
	}
	pub, err := json.Marshal(cfg.Public(in.Kind))
	if err != nil {
		return Info{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	var out store.Notifier
	err = s.d.Store.Tx(ctx, func(r *store.Repo) error {
		if _, err := r.Notifier(ctx, in.ID); err == nil {
			return ErrExists
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if n, err := r.CountNotifiers(ctx); err != nil {
			return err
		} else if n >= MaxNotifiers {
			return ErrTooMany
		}
		n := store.Notifier{ID: in.ID, Kind: in.Kind, Nonce: nonce, Ciphertext: ct, PublicJSON: string(pub), Events: in.Events, Enabled: enabled}
		if err := r.InsertNotifier(ctx, n); err != nil {
			if errors.Is(err, store.ErrNotifierExists) {
				return ErrExists
			}
			return err
		}
		var err error
		out, err = r.Notifier(ctx, in.ID)
		return err
	})
	if err != nil {
		return Info{}, err
	}
	s.d.Log.InfoContext(ctx, "已新增 notifier", "notifier", in.ID, "kind", in.Kind, "host", endpointHost(in.Kind, cfg))
	return toInfo(out), nil
}

// Get 取出一個 notifier。
func (s *Service) Get(ctx context.Context, id string) (Info, error) {
	n, err := s.d.Store.Read().Notifier(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return toInfo(n), nil
}

// List 列出所有 notifier（依建立順序）。
func (s *Service) List(ctx context.Context) ([]Info, error) {
	ns, err := s.d.Store.Read().ListNotifiers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(ns))
	for _, n := range ns {
		out = append(out, toInfo(n))
	}
	return out, nil
}

// Patch 修改 notifier；只更新給定的欄位，沒給的密鑰保留原值（NOTIFY-R6）。
func (s *Service) Patch(ctx context.Context, id string, in PatchInput) (Info, error) {
	var out store.Notifier
	err := s.d.Store.Tx(ctx, func(r *store.Repo) error {
		n, err := r.Notifier(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var fields []validate.FieldError
		if in.Events != nil {
			fields = append(fields, ValidateEvents(in.Events)...)
			n.Events = in.Events
		}
		if in.Enabled != nil {
			n.Enabled = *in.Enabled
		}
		if in.Config != nil {
			// 以現有的設定為基礎（密鑰被重設時只剩非機密欄位，缺少的密鑰會要求重新輸入）。
			base, err := s.open(n)
			if err != nil {
				pub := map[string]any{}
				_ = json.Unmarshal([]byte(n.PublicJSON), &pub)
				base = nonSecret(n.Kind, pub)
			}
			raw := map[string]any{}
			for k, v := range base {
				raw[k] = v
			}
			for k, v := range in.Config {
				if v == nil {
					delete(raw, k)
					continue
				}
				raw[k] = v
			}
			cfg, errs := ValidateConfig(n.Kind, raw, s.d.Hosts)
			fields = append(fields, errs...)
			if len(errs) == 0 {
				nonce, ct, err := s.seal(id, cfg)
				if err != nil {
					return err
				}
				pub, err := json.Marshal(cfg.Public(n.Kind))
				if err != nil {
					return err
				}
				n.Nonce, n.Ciphertext, n.PublicJSON = nonce, ct, string(pub)
			}
		}
		if len(fields) > 0 {
			return &InvalidError{Fields: fields}
		}
		if err := r.UpdateNotifier(ctx, n); err != nil {
			return err
		}
		out, err = r.Notifier(ctx, id)
		return err
	})
	if err != nil {
		return Info{}, err
	}
	return toInfo(out), nil
}

// Delete 刪除 notifier 與它還沒送出的 outbox 項目（NOTIFY-R1）。
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.d.Store.Tx(ctx, func(r *store.Repo) error {
		ok, err := r.DeleteNotifier(ctx, id)
		if err == nil && !ok {
			return ErrNotFound
		}
		return err
	})
}

// TestResult 是 POST /api/notifiers/:id/test 的結果（NOTIFY-R12）。
type TestResult struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	Message    string `json:"message,omitempty"`
}

// Test 立即送出一則測試通知：不經 outbox、不重試、不受去重與速率上限限制，但經過 netguard（NOTIFY-R12）。
func (s *Service) Test(ctx context.Context, id string) (TestResult, error) {
	n, err := s.d.Store.Read().Notifier(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return TestResult{}, ErrNotFound
	}
	if err != nil {
		return TestResult{}, err
	}
	cfg, err := s.open(n)
	if err != nil {
		return TestResult{ErrorClass: string(errclass.ClassPermanent), Message: MsgSecretGone}, nil
	}
	now := s.d.Clock.Now()
	deliveryID := store.NewID()
	body, err := render(n.Kind, cfg, Event{Name: "test"}, deliveryID, now, s.d.PublicURL)
	if err != nil {
		return TestResult{}, err
	}
	o := send(ctx, s.d.HTTP, n.Kind, buildRequest(n.Kind, cfg, body, "test", deliveryID, s.d.Version, now), now)
	s.logOutcome(ctx, "測試通知", n, cfg, "test", "", o)
	if o.OK {
		return TestResult{OK: true, Status: o.Status}, nil
	}
	return TestResult{Status: o.Status, ErrorClass: string(o.Class), Message: o.Message}, nil
}

// logOutcome 只記錄 notifier 的 id、kind、主機、事件、outbox id、狀態碼與錯誤類別（NOTIFY-R13）。
func (s *Service) logOutcome(ctx context.Context, what string, n store.Notifier, cfg Config, event, outboxID string, o Outcome) {
	attrs := []any{"notifier", n.ID, "kind", n.Kind, "host", endpointHost(n.Kind, cfg), "event", event, "status", o.Status}
	if outboxID != "" {
		attrs = append(attrs, "outbox", outboxID)
	}
	if o.OK {
		s.d.Log.InfoContext(ctx, what+"已送出", attrs...)
		return
	}
	s.d.Log.WarnContext(ctx, what+"送出失敗", append(attrs, "error_class", string(o.Class))...)
}

// Notification 是 GET /api/notifications 的一筆（NOTIFY-R8）。
type Notification struct {
	ID        string     `json:"id"`
	Notifier  string     `json:"notifier"`
	Event     string     `json:"event"`
	State     string     `json:"state"`
	Attempts  int        `json:"attempts"`
	LastError *string    `json:"last_error"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at"`
}

// ValidState 回報 state 是否是 ?state= 接受的值。
func ValidState(state string) bool {
	return state == store.OutboxPending || state == store.OutboxSent || state == store.OutboxDropped
}

// Notifications 回傳最近 100 筆 outbox 項目；state 空白代表全部。
func (s *Service) Notifications(ctx context.Context, state string) ([]Notification, error) {
	rows, err := s.d.Store.Read().ListOutbox(ctx, state, ListLimit)
	if err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(rows))
	for _, e := range rows {
		n := Notification{ID: e.ID, Notifier: e.NotifierID, Event: e.Event, State: e.State, Attempts: e.Attempts, CreatedAt: e.CreatedAt, SentAt: e.SentAt}
		if e.LastError != "" {
			msg := e.LastError
			n.LastError = &msg
		}
		out = append(out, n)
	}
	return out, nil
}

// Register 在排程器登記 notify.deliver（必須在 worker 開始之前）。
func (s *Service) Register(sch *scheduler.Scheduler) error {
	return sch.Register(scheduler.Kind{Name: DeliverKind, Interval: deliverEvery, Run: s.Deliver})
}

// Deliver 是 notify.deliver 的 Handler（NOTIFY-R8、R11）：送出到期的 pending 項目。
// 端點的失敗記在 outbox（last_error、退避），不回傳給排程器；只有資料庫錯誤會回傳。
// 下一次是最早的 pending 項目與一分鐘之後取較早的；沒有 pending 的項目時一分鐘之後再看。
func (s *Service) Deliver(ctx context.Context, _ scheduler.Job, now time.Time) (scheduler.Result, error) {
	if _, err := s.d.Store.Write().DeleteFinishedOutboxBefore(ctx, now.Add(-retention)); err != nil {
		return scheduler.Result{}, err
	}
	due, err := s.d.Store.Read().DueOutbox(ctx, now, maxDuePerRun)
	if err != nil {
		return scheduler.Result{}, err
	}
	notifiers := map[string]*loaded{}
	for _, e := range due {
		if err := ctx.Err(); err != nil {
			return scheduler.Result{}, err
		}
		l, err := s.load(ctx, notifiers, e.NotifierID)
		if err != nil {
			return scheduler.Result{}, err
		}
		if err := s.deliverOne(ctx, l, e, now); err != nil {
			return scheduler.Result{}, err
		}
	}
	next := now.Add(deliverEvery)
	if at, ok, err := s.d.Store.Read().NextOutboxAttempt(ctx); err != nil {
		return scheduler.Result{}, err
	} else if ok && at.Before(next) {
		next = at
		if !next.After(now) {
			// 這一輪沒處理完（超過 maxDuePerRun）：下一輪馬上繼續。
			next = now.Add(time.Millisecond)
		}
	}
	return scheduler.Result{Next: next}, nil
}

// loaded 是這一輪用到的 notifier（設定只解開一次）。
type loaded struct {
	n       store.Notifier
	missing bool
	cfg     Config
	openErr error
	// attempts 是速率視窗內的嘗試時間（含這一輪已經送出的）。
	attempts []time.Time
	fetched  bool
}

func (s *Service) load(ctx context.Context, cache map[string]*loaded, id string) (*loaded, error) {
	if l, ok := cache[id]; ok {
		return l, nil
	}
	l := &loaded{}
	n, err := s.d.Store.Read().Notifier(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		l.missing = true
	case err != nil:
		return nil, err
	default:
		l.n = n
		l.cfg, l.openErr = s.open(n)
	}
	cache[id] = l
	return l, nil
}

// deliverOne 處理一個到期的項目：速率上限、送出、寫回結果。
func (s *Service) deliverOne(ctx context.Context, l *loaded, e store.OutboxEntry, now time.Time) error {
	w := s.d.Store
	switch {
	case l.missing:
		return s.finish(ctx, e, store.OutboxAttempt{State: store.OutboxDropped, Attempts: e.Attempts, NextAttemptAt: now, LastAttemptAt: now, LastError: "notifier 已刪除。"}, "")
	case !l.n.Enabled:
		return s.finish(ctx, e, store.OutboxAttempt{State: store.OutboxDropped, Attempts: e.Attempts, NextAttemptAt: now, LastAttemptAt: now, LastError: "notifier 已停用。"}, l.n.ID)
	case l.openErr != nil:
		return s.finish(ctx, e, store.OutboxAttempt{State: store.OutboxDropped, Attempts: e.Attempts + 1, NextAttemptAt: now, LastAttemptAt: now, LastError: MsgSecretGone}, l.n.ID)
	}
	// 速率上限（NOTIFY-R11）：視窗是 (now-per, now]，超過時延到最早的一次嘗試離開視窗的時間，不計入嘗試次數。
	if !l.fetched {
		ts, err := w.Read().OutboxAttemptsSince(ctx, l.n.ID, now.Add(-s.d.RatePer))
		if err != nil {
			return err
		}
		l.attempts, l.fetched = ts, true
	}
	if len(l.attempts) >= s.d.RateCount {
		slot := l.attempts[len(l.attempts)-s.d.RateCount].Add(s.d.RatePer)
		return w.Write().DeferOutbox(ctx, e.ID, slot)
	}
	var ev Event
	if err := json.Unmarshal([]byte(e.PayloadJSON), &ev); err != nil {
		return s.finish(ctx, e, store.OutboxAttempt{State: store.OutboxDropped, Attempts: e.Attempts + 1, NextAttemptAt: now, LastAttemptAt: now, LastError: "通知內容無法解析。"}, l.n.ID)
	}
	body, err := render(l.n.Kind, l.cfg, ev, e.ID, e.CreatedAt, s.d.PublicURL)
	if err != nil {
		return err
	}
	sentAt := s.d.Clock.Now()
	o := send(ctx, s.d.HTTP, l.n.Kind, buildRequest(l.n.Kind, l.cfg, body, e.Event, e.ID, s.d.Version, sentAt), sentAt)
	if ctx.Err() != nil {
		return ctx.Err() // 關閉中：不記錄這次嘗試，下次啟動再送
	}
	l.attempts = append(l.attempts, now)
	s.logOutcome(ctx, "通知", l.n, l.cfg, e.Event, e.ID, o)
	return s.finish(ctx, e, nextAttempt(e.Attempts, o, now), l.n.ID)
}

// nextAttempt 依結果計算寫回的欄位（NOTIFY-R8）。
func nextAttempt(prev int, o Outcome, now time.Time) store.OutboxAttempt {
	a := store.OutboxAttempt{Attempts: prev + 1, LastAttemptAt: now, NextAttemptAt: now}
	switch {
	case o.OK:
		t := now
		a.State, a.SentAt = store.OutboxSent, &t
	case o.Class != errclass.ClassRetryable || a.Attempts >= MaxAttempts:
		a.State, a.LastError = store.OutboxDropped, o.Message
	default:
		wait := backoff[a.Attempts-1]
		if o.RetryAfter > wait {
			wait = o.RetryAfter
		}
		a.State, a.LastError, a.NextAttemptAt = store.OutboxPending, o.Message, now.Add(wait)
	}
	return a
}

// finish 寫回一次嘗試；標記 dropped 時在同一個交易寫入事件 notify.dropped（總覽顯示，不再以通知送出）。
func (s *Service) finish(ctx context.Context, e store.OutboxEntry, a store.OutboxAttempt, notifierID string) error {
	return s.d.Store.Tx(ctx, func(r *store.Repo) error {
		err := r.RecordOutboxAttempt(ctx, e.ID, a)
		if errors.Is(err, store.ErrNotFound) {
			return nil // 期間被刪除（notifier 刪除）：不用記錄
		}
		if err != nil || a.State != store.OutboxDropped {
			return err
		}
		payload, err := json.Marshal(map[string]any{"outbox": e.ID, "notifier": notifierID, "event": e.Event, "message": a.LastError})
		if err != nil {
			return err
		}
		return r.InsertEvent(ctx, store.EventRecord{Type: EventDropped, PayloadJSON: string(payload)})
	})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
