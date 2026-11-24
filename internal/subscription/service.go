package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/validate"
)

// JobKind 是訂閱檢查的排程工作（ADR-0021 第 2 點），key 是訂閱 id。
const JobKind = "subscription.check"

// maxEntriesList 是 GET /entries 的上限，也是一次檢查讀回既有條目的上限。
const maxEntriesList = 5000

// Ingester 是新增影音的介面（*pipeline.Pipeline 實作它）。
type Ingester interface {
	Ingest(ctx context.Context, req pipeline.IngestRequest) ([]pipeline.IngestResult, error)
	CheckIngestPlan(ctx context.Context, profile string, overrides map[string]any) error
}

// Details 是單一影片的補齊資訊（SUB-R2：過濾需要列表沒有提供的片長、直播狀態時才查詢）。
type Details struct {
	DurationS  *int
	LiveStatus string
}

// Prober 以 yt-dlp -J 取得單一影片的 Details；platform 是 youtube 或 bilibili。
type Prober func(ctx context.Context, platform, id string) (Details, error)

// Deps 是 Service 的依賴。
type Deps struct {
	Store  *store.Store
	Clock  clock.Clock
	Log    *slog.Logger
	HTTP   *http.Client // 一律是 netguard 的 client（SUB-R11）
	Ingest Ingester
	Runner toolexec.Runner
	Tools  toolexec.Resolver
	Probe  Prober
	Config config.Subscriptions
	// JitterMax 是 scheduler.jitter_max（ADR-0021 第 1 點）。
	JitterMax time.Duration
}

// Service 是訂閱的建立、查詢與檢查。
type Service struct {
	d     Deps
	list  *lister
	rate  *limiter
	log   *slog.Logger
	limit config.Subscriptions
}

// New 建立 Service；設定檔沒有寫的數值用預設值。
func New(d Deps) *Service {
	log := d.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := d.Config
	if c.BackfillMax <= 0 {
		c.BackfillMax = config.DefaultBackfillMax
	}
	if c.BackfillPerCheck <= 0 {
		c.BackfillPerCheck = config.DefaultBackfillPerCheck
	}
	if c.RateLimits.YouTube <= 0 {
		c.RateLimits.YouTube = config.DefaultRateYouTube
	}
	if c.RateLimits.Bilibili <= 0 {
		c.RateLimits.Bilibili = config.DefaultRateBilibili
	}
	if c.RateLimits.FeedHost <= 0 {
		c.RateLimits.FeedHost = config.DefaultRateFeedHost
	}
	return &Service{d: d, list: &lister{Runner: d.Runner, Tools: d.Tools}, rate: newLimiter(d.Clock), log: log, limit: c}
}

// Register 在排程器登記 subscription.check（必須在 worker 開始之前）。
func (s *Service) Register(sc *scheduler.Scheduler) error {
	return sc.Register(scheduler.Kind{Name: JobKind, Run: s.runCheck})
}

// ---- 錯誤 ----

// ErrNotFound 是訂閱不存在。
var ErrNotFound = store.ErrNotFound

// FieldsError 是 422 的逐欄位錯誤。
type FieldsError struct{ Fields []validate.FieldError }

func (e *FieldsError) Error() string {
	if len(e.Fields) == 0 {
		return "請求內容不正確。"
	}
	return e.Fields[0].Path + "：" + e.Fields[0].Message
}

// DuplicateError 是同一個來源已經有訂閱（409，SUB-R1）。
type DuplicateError struct{ SubscriptionID string }

func (e *DuplicateError) Error() string { return msgDuplicate }

// ---- 檢視 ----

// View 是 API 回應的訂閱（SUB 的介面）。
type View struct {
	ID             string         `json:"id"`
	Kind           string         `json:"kind" enum:"channel,playlist,collection,feed"`
	Platform       string         `json:"platform" enum:"youtube,bilibili,podcast"`
	ExternalID     string         `json:"external_id"`
	URL            string         `json:"url"`
	Title          string         `json:"title" doc:"第一次檢查時從來源取得；還沒檢查時是空字串"`
	Profile        *string        `json:"profile" nullable:"true" doc:"處理方案；null 代表預設處理方案"`
	Overrides      map[string]any `json:"overrides"`
	Schedule       string         `json:"schedule" doc:"檢查頻率（Go duration）"`
	Backfill       Backfill       `json:"backfill"`
	Filters        Filters        `json:"filters"`
	Enabled        bool           `json:"enabled"`
	CreatedAt      time.Time      `json:"created_at"`
	LastCheckedAt  *time.Time     `json:"last_checked_at" nullable:"true"`
	NextCheckAt    *time.Time     `json:"next_check_at" nullable:"true" doc:"下一次檢查；停用時是 null"`
	LastError      *string        `json:"last_error" nullable:"true"`
	LastErrorClass *string        `json:"last_error_class" nullable:"true" enum:"retryable,permanent,needs_action"`
	NewItems       int            `json:"new_items" doc:"新內容：這個訂閱建立、還沒有標為已讀、不在垃圾桶中的影音數（SUB-R14）"`
}

// EntryView 是 API 回應的條目。
type EntryView struct {
	Ref         string     `json:"ref" doc:"影音的短代號，例如 yt:abc123xxxxx"`
	Platform    string     `json:"platform"`
	ExternalID  string     `json:"external_id"`
	Part        int        `json:"part"`
	Title       string     `json:"title"`
	PublishedAt *time.Time `json:"published_at" nullable:"true"`
	DurationS   *int       `json:"duration_s" nullable:"true"`
	LiveStatus  *string    `json:"live_status" nullable:"true"`
	Decision    string     `json:"decision" enum:"created,skipped,deferred"`
	Reason      *string    `json:"reason" nullable:"true"`
	ItemID      *string    `json:"item_id" nullable:"true"`
	SeenAt      time.Time  `json:"seen_at"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// newItems 是 SubscriptionNewItems 的結果；nil 時在這裡查詢（單一訂閱的回應）。
func (s *Service) view(ctx context.Context, r *store.Repo, sub store.Subscription, newItems map[string]int) (View, error) {
	if newItems == nil {
		var err error
		if newItems, err = r.SubscriptionNewItems(ctx); err != nil {
			return View{}, err
		}
	}
	v := View{
		ID: sub.ID, Kind: sub.Kind, Platform: sub.Platform, ExternalID: sub.ExternalID, URL: sub.URL, Title: sub.Title,
		Profile: nilIfEmpty(sub.ProfileID), Overrides: map[string]any{}, Schedule: FormatSchedule(sub.Schedule),
		Backfill: decodeBackfill(sub.Backfill), Filters: decodeFilters(sub.Filters), Enabled: sub.Enabled,
		CreatedAt: sub.CreatedAt, LastCheckedAt: sub.LastCheckedAt,
		LastError: nilIfEmpty(sub.LastError), LastErrorClass: nilIfEmpty(sub.LastErrorClass),
		NewItems: newItems[sub.ID],
	}
	if len(sub.Overrides) > 0 {
		_ = json.Unmarshal(sub.Overrides, &v.Overrides)
	}
	switch j, err := r.ScheduledJob(ctx, JobKind, sub.ID); {
	case err == nil:
		at := j.NextRunAt
		v.NextCheckAt = &at
	case !errors.Is(err, store.ErrNotFound):
		return View{}, err
	}
	return v, nil
}

// ---- 建立、修改 ----

// BackfillInput 是請求中的回補範圍。
type BackfillInput struct {
	Mode  string `json:"mode"`
	Count *int   `json:"count,omitempty"`
}

// CreateInput 是 POST /api/subscriptions 的內容（SUB-R1）。
type CreateInput struct {
	URL       string
	Profile   string
	Overrides map[string]any
	Schedule  *string
	Backfill  *BackfillInput
	Filters   *FiltersInput
	Enabled   *bool
	// title 是暫定的標題，只有匯入使用（SUB-R7）；POST /api/subscriptions 沒有 title（SUB-R1）。
	title string
}

// MaxURLLen 是訂閱網址的長度上限。
const MaxURLLen = 2048

const msgURLTooLong = "網址最長 2048 個字元。"

// settings 驗證排程、回補與過濾，錯誤依欄位的固定順序（backfill、filters、schedule）。
func settings(schedule *string, bf *BackfillInput, fi *FiltersInput, base store.Subscription, hasBase bool) (time.Duration, Backfill, Filters, []validate.FieldError) {
	var errs []validate.FieldError
	b := Backfill{Mode: BackfillNone}
	f := DefaultFilters()
	sched := DefaultSched
	if hasBase {
		b, f, sched = decodeBackfill(base.Backfill), decodeFilters(base.Filters), base.Schedule
	}
	if bf != nil {
		b = Backfill{Mode: bf.Mode, Count: bf.Count}
		errs = append(errs, validateBackfill(b)...)
		b = normalizeBackfill(b)
	}
	f = fi.apply(f)
	errs = append(errs, validateFilters(f)...)
	if schedule != nil || !hasBase {
		d, e := parseSchedule(schedule)
		errs = append(errs, e...)
		if len(e) == 0 {
			sched = d
		}
	}
	return sched, b, f, errs
}

// Create 建立訂閱並排定第一次檢查（下一輪排程器就執行，SUB-R9）。
func (s *Service) Create(ctx context.Context, in CreateInput) (View, error) {
	if in.URL == "" {
		return View{}, &FieldsError{Fields: []validate.FieldError{{Path: "url", Message: msgURLRequired}}}
	}
	if len(in.URL) > MaxURLLen {
		return View{}, &FieldsError{Fields: []validate.FieldError{{Path: "url", Message: msgURLTooLong}}}
	}
	src, err := Recognize(in.URL)
	if err != nil {
		return View{}, err
	}
	sched, bf, flt, errs := settings(in.Schedule, in.Backfill, in.Filters, store.Subscription{}, false)
	if len(errs) > 0 {
		return View{}, &FieldsError{Fields: errs}
	}
	if err := s.d.Ingest.CheckIngestPlan(ctx, in.Profile, in.Overrides); err != nil {
		return View{}, err
	}
	enabled := in.Enabled == nil || *in.Enabled
	sub := store.Subscription{
		Kind: src.Kind, Platform: src.Platform, ExternalID: src.ExternalID, URL: src.URL, Title: in.title, ProfileID: in.Profile,
		Schedule: sched, Enabled: enabled,
	}
	if sub.Overrides, err = json.Marshal(orEmpty(in.Overrides)); err != nil {
		return View{}, err
	}
	sub.Backfill, _ = json.Marshal(bf)
	sub.Filters, _ = json.Marshal(flt)
	var out View
	err = s.d.Store.Tx(ctx, func(r *store.Repo) error {
		dup, err := r.SubscriptionsBySource(ctx, src.Kind, src.Platform, src.ExternalID)
		if err != nil {
			return err
		}
		if len(dup) > 0 {
			return &DuplicateError{SubscriptionID: dup[0].ID}
		}
		created, err := r.InsertSubscription(ctx, sub)
		if err != nil {
			return err
		}
		if created.Enabled {
			if err := r.ScheduleJob(ctx, JobKind, created.ID, created.CreatedAt); err != nil {
				return err
			}
		}
		if err := r.EmitSubscriptionEvent(ctx, events.Event{Type: events.TypeSubscriptionCreated, Subscription: created.ID}); err != nil {
			return err
		}
		out, err = s.view(ctx, r, created, map[string]int{})
		return err
	})
	return out, err
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// PatchInput 是 PATCH /api/subscriptions/:id 的內容；nil 的欄位不變。
type PatchInput struct {
	Profile   *string
	Overrides map[string]any
	Schedule  *string
	Backfill  *BackfillInput
	Filters   *FiltersInput
	Enabled   *bool
}

// Patch 修改訂閱的設定。修改 backfill 時，下一次檢查重新評估因回補範圍略過的條目（SUB-R3）；
// 修改 filters 不重新評估（SUB-R4）。enabled 改成 false 時取消排程，改回 true 時排在現在。
func (s *Service) Patch(ctx context.Context, id string, in PatchInput) (View, error) {
	cur, err := s.d.Store.Read().Subscription(ctx, id)
	if err != nil {
		return View{}, err
	}
	sched, bf, flt, errs := settings(in.Schedule, in.Backfill, in.Filters, cur, true)
	if len(errs) > 0 {
		return View{}, &FieldsError{Fields: errs}
	}
	next := cur
	if in.Profile != nil {
		next.ProfileID = *in.Profile
	}
	ov := map[string]any{}
	_ = json.Unmarshal(cur.Overrides, &ov)
	if in.Overrides != nil {
		ov = in.Overrides
	}
	if in.Profile != nil || in.Overrides != nil {
		if err := s.d.Ingest.CheckIngestPlan(ctx, next.ProfileID, ov); err != nil {
			return View{}, err
		}
	}
	next.Overrides, _ = json.Marshal(ov)
	next.Schedule = sched
	newBF, _ := json.Marshal(bf)
	if in.Backfill != nil && string(newBF) != string(cur.Backfill) {
		next.Reevaluate = true
	}
	next.Backfill = newBF
	next.Filters, _ = json.Marshal(flt)
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	var out View
	err = s.d.Store.Tx(ctx, func(r *store.Repo) error {
		if err := r.UpdateSubscriptionSettings(ctx, next); err != nil {
			return err
		}
		switch {
		case !next.Enabled:
			if _, err := r.DeleteScheduledJob(ctx, JobKind, id); err != nil {
				return err
			}
		case !cur.Enabled:
			if err := r.ScheduleJob(ctx, JobKind, id, s.d.Clock.Now()); err != nil {
				return err
			}
		}
		got, err := r.Subscription(ctx, id)
		if err != nil {
			return err
		}
		out, err = s.view(ctx, r, got, nil)
		return err
	})
	return out, err
}

// ---- 查詢、刪除、立即檢查 ----

// List 依建立順序列出全部訂閱。
func (s *Service) List(ctx context.Context) ([]View, error) {
	r := s.d.Store.Read()
	subs, err := r.Subscriptions(ctx)
	if err != nil {
		return nil, err
	}
	newItems, err := r.SubscriptionNewItems(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(subs))
	for _, sub := range subs {
		v, err := s.view(ctx, r, sub, newItems)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get 取得一個訂閱。
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	r := s.d.Store.Read()
	sub, err := r.Subscription(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, r, sub, nil)
}

// Entries 列出訂閱的條目，decision 不是空字串時只列那一種，最多 limit 筆（0 代表上限）。
func (s *Service) Entries(ctx context.Context, id, decision string, limit int) ([]EntryView, error) {
	r := s.d.Store.Read()
	if _, err := r.Subscription(ctx, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxEntriesList {
		limit = maxEntriesList
	}
	es, err := r.SubscriptionEntries(ctx, id, maxEntriesList)
	if err != nil {
		return nil, err
	}
	out := make([]EntryView, 0, len(es))
	for _, e := range es {
		if decision != "" && e.Decision != decision {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, EntryView{
			Ref: entryRef(e.Platform, e.ExternalID, e.Part), Platform: e.Platform, ExternalID: e.ExternalID, Part: e.Part,
			Title: e.Title, PublishedAt: e.PublishedAt, DurationS: e.DurationS, LiveStatus: nilIfEmpty(e.LiveStatus),
			Decision: e.Decision, Reason: nilIfEmpty(e.Reason), ItemID: nilIfEmpty(e.ItemID), SeenAt: e.SeenAt,
		})
	}
	return out, nil
}

// Delete 刪除訂閱、它的條目與排程；已建立的影音保留，subscription_id 改為空（SUB-R13）。
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.d.Store.Tx(ctx, func(r *store.Repo) error {
		ok, err := r.DeleteSubscription(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return store.ErrNotFound
		}
		if _, err := r.DeleteScheduledJob(ctx, JobKind, id); err != nil {
			return err
		}
		return r.EmitSubscriptionEvent(ctx, events.Event{Type: events.TypeSubscriptionDeleted, Subscription: id})
	})
}

// CheckNow 把下一次檢查改成現在（下一輪排程器執行，SUB-R9），回傳排定的時間。停用的訂閱回 *FieldsError。
func (s *Service) CheckNow(ctx context.Context, id string) (time.Time, error) {
	now := s.d.Clock.Now()
	err := s.d.Store.Tx(ctx, func(r *store.Repo) error {
		sub, err := r.Subscription(ctx, id)
		if err != nil {
			return err
		}
		if !sub.Enabled {
			return &FieldsError{Fields: []validate.FieldError{{Path: "enabled", Message: "這個訂閱已停用，啟用之後才會檢查。"}}}
		}
		return r.ScheduleJob(ctx, JobKind, id, now)
	})
	return now, err
}

// Count 回傳訂閱數。
func (s *Service) Count(ctx context.Context) (int64, error) {
	return s.d.Store.Read().CountSubscriptions(ctx)
}
