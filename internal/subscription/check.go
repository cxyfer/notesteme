package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/events"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// 略過的原因（SUB-R3、R4、R5、R8），依檢查的順序。
const (
	ReasonTombstoned         = "tombstoned"
	ReasonInTrash            = "in_trash"
	ReasonAlreadyInLibrary   = "already_in_library"
	ReasonLive               = "live"
	ReasonShorts             = "shorts"
	ReasonTooShort           = "too_short"
	ReasonTooLong            = "too_long"
	ReasonTitleExcluded      = "title_excluded"
	ReasonTitleNotIncluded   = "title_not_included"
	ReasonBeforeSubscription = "before_subscription"
	ReasonBackfillLimit      = "backfill_limit"
	ReasonBackfillPerCheck   = "backfill_per_check"
	// ReasonDetailsUnavailable、ReasonIngestFailed 是暫時的 deferred：下一次檢查再試。
	ReasonDetailsUnavailable = "details_unavailable"
	ReasonIngestFailed       = "ingest_failed"
)

// liveStatuses 是 live: skip 時略過的直播狀態（SUB-R8）。
var liveStatuses = map[string]bool{"is_live": true, "is_upcoming": true, "post_live": true, "was_live": true}

// runCheck 是 subscription.check 的 Handler（ADR-0021 第 2 點）。
//
// 回傳：
//   - 成功、permanent 與 needs_action 的失敗：下一次是「這次執行的時間＋schedule＋jitter」（SUB-R9、R12：不退避）。
//   - retryable 的失敗：錯誤（scheduler 依 failures 退避）；平台回 429 時下一次不早於 bucket 恢復的時間（SUB-R10）。
//   - 額度用完：Deferred，下一次是額度恢復的時間，不算失敗。
//   - 訂閱不存在、停用，或 @handle 撞到既有的訂閱：Done。
func (s *Service) runCheck(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.Result, error) {
	sub, err := s.d.Store.Read().Subscription(ctx, job.Key)
	if errors.Is(err, store.ErrNotFound) {
		return scheduler.Result{Done: true}, nil
	}
	if err != nil {
		return scheduler.Result{}, err
	}
	if !sub.Enabled {
		return scheduler.Result{Done: true}, nil
	}
	next := scheduler.NextWithJitter(JobKind, sub.ID, now, sub.Schedule, s.d.JitterMax)
	c := &check{s: s, sub: sub, now: now}
	res, err := c.run(ctx)
	var de *deferredError
	switch {
	case errors.As(err, &de):
		if err := c.persist(ctx, false); err != nil {
			return scheduler.Result{}, err
		}
		return scheduler.Result{Deferred: true, Next: de.At}, nil
	case errors.Is(err, errDuplicateHandle):
		return scheduler.Result{Done: true}, nil
	case err != nil && ctx.Err() != nil:
		return scheduler.Result{}, ctx.Err()
	case err != nil:
		ce := asCheckError(err)
		if ce.cause != nil {
			s.log.DebugContext(ctx, "訂閱檢查失敗", "subscription", sub.ID, "class", string(ce.Class), "message", ce.Message)
		}
		if err := c.fail(ctx, ce); err != nil {
			return scheduler.Result{}, err
		}
		if ce.Class != errclass.ClassRetryable {
			return scheduler.Result{Next: next}, nil
		}
		if ce.throttled {
			return scheduler.Result{}, &scheduler.RetryAfter{Err: errors.New(ce.Message), NotBefore: c.throttledUntil}
		}
		return scheduler.Result{}, errors.New(ce.Message)
	}
	_ = res
	return scheduler.Result{Next: next}, nil
}

// errDuplicateHandle 是 @handle 解析後撞到既有的訂閱（已停用，SUB-R12）。
var errDuplicateHandle = errors.New("duplicate handle")

// check 是一次檢查的狀態。
type check struct {
	s   *Service
	sub store.Subscription
	now time.Time

	title, externalID string
	// decided 是這次決定（或重新決定）的條目，最後一起寫入。
	decided        []store.SubscriptionEntry
	toBackfill     []candidate
	created        int
	skipped        int
	complete       bool
	throttledUntil time.Time
}

// run 探索、決定並建立影音。回傳的錯誤是 *deferredError、*checkError、errDuplicateHandle 或資料庫錯誤。
func (c *check) run(ctx context.Context) (struct{}, error) {
	s, sub := c.s, c.sub
	c.title, c.externalID = sub.Title, sub.ExternalID
	existing, err := s.d.Store.Read().SubscriptionEntries(ctx, sub.ID, maxEntriesList)
	if err != nil {
		return struct{}{}, err
	}
	known := map[string]store.SubscriptionEntry{}
	for _, e := range existing {
		known[candidate{Platform: e.Platform, ExternalID: e.ExternalID, Part: e.Part}.key()] = e
	}
	bf := decodeBackfill(sub.Backfill)
	first := sub.BaselineAt == nil
	listBackfill := bf.Mode != BackfillNone && (first || sub.Reevaluate)
	lst, err := s.discover(ctx, sub, listBackfill, c.backfillCap(bf))
	if lst.ExternalID != "" {
		c.externalID = lst.ExternalID
	}
	if lst.Title != "" {
		c.title = lst.Title
	}
	if err != nil {
		var ce *checkError
		if errors.As(err, &ce) && ce.throttled {
			c.throttledUntil = s.throttled(sub.Platform, sub.URL)
		}
		return struct{}{}, err
	}
	if c.externalID != sub.ExternalID {
		if dup, err := c.handleDuplicate(ctx); err != nil || dup {
			if dup {
				return struct{}{}, errDuplicateHandle
			}
			return struct{}{}, err
		}
	}

	// 這次要評估的條目：清單中第一次出現的、先前 deferred 的、PATCH backfill 之後因回補範圍略過的。
	var fresh, old []candidate
	inList := map[string]bool{}
	for _, cand := range lst.Items {
		inList[cand.key()] = true
		e, seen := known[cand.key()]
		switch {
		case !seen:
			cand.fresh = true
			fresh = append(fresh, cand)
		case c.reconsider(e):
			old = append(old, c.mergeStored(cand, e))
		}
	}
	// 不在這次清單中的 deferred 條目（例如 RSS 只列最新 15 部）也要依序建立。
	for _, e := range existing {
		if !inList[candidate{Platform: e.Platform, ExternalID: e.ExternalID, Part: e.Part}.key()] && c.reconsider(e) {
			old = append(old, c.storedCandidate(e))
		}
	}

	filters := decodeFilters(sub.Filters)
	var newOnes, backfill []candidate
	for _, cand := range append(old, fresh...) {
		reason, err := c.filter(ctx, &cand, filters)
		if err != nil {
			return struct{}{}, err
		}
		if reason != "" {
			c.decide(cand, store.EntrySkipped, reason, "")
			continue
		}
		if c.isNew(cand, first) {
			newOnes = append(newOnes, cand)
		} else {
			backfill = append(backfill, cand)
		}
	}
	c.selectBackfill(backfill, bf, known)
	for _, cand := range newOnes {
		if err := c.create(ctx, cand, queue.PrioritySubscription); err != nil {
			return struct{}{}, err
		}
	}
	for _, cand := range c.toBackfill {
		if err := c.create(ctx, cand, queue.PriorityBackfill); err != nil {
			return struct{}{}, err
		}
	}
	c.complete = true
	return struct{}{}, c.persist(ctx, true)
}

// backfillCap 是回補最多的部數：recent 是 count，all 是 subscriptions.backfill_max（SUB-R3）。
func (c *check) backfillCap(bf Backfill) int {
	if bf.Mode == BackfillRecent && bf.Count != nil {
		return *bf.Count
	}
	return c.s.limit.BackfillMax
}

// isNew 判斷是不是新內容（SUB-R3）：頻道看發布時間是否不早於訂閱的建立時間；
// 播放清單、合集與 feed 看是不是在第一次成功檢查（基準清單）之後才出現。
func (c *check) isNew(cand candidate, first bool) bool {
	if c.sub.Kind == KindChannel {
		return cand.PublishedAt != nil && !cand.PublishedAt.Before(c.sub.CreatedAt)
	}
	return !first && cand.fresh
}

// filter 依序檢查略過的原因（SUB-R4），回傳第一個成立的原因；空字串代表通過。
func (c *check) filter(ctx context.Context, cand *candidate, f Filters) (string, error) {
	r := c.s.d.Store.Read()
	if has, err := r.HasTombstone(ctx, cand.Platform, cand.ExternalID, cand.Part); err != nil {
		return "", err
	} else if has {
		return ReasonTombstoned, nil
	}
	switch it, err := r.ItemByKey(ctx, cand.Platform, cand.ExternalID, cand.Part); {
	case err == nil && it.DeletedAt != nil:
		return ReasonInTrash, nil
	case err == nil:
		return ReasonAlreadyInLibrary, nil
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	needLive := f.Live == LiveSkip && cand.LiveStatus == ""
	needDur := (f.MinDurationS != nil || f.MaxDurationS != nil) && cand.DurationS == nil
	if (needLive || needDur) && cand.Platform != PlatformPodcast && c.s.d.Probe != nil && !cand.probed {
		if err := c.s.take(cand.Platform, ""); err != nil {
			return "", err
		}
		d, err := c.s.d.Probe(ctx, cand.Platform, cand.ExternalID)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			c.decide(*cand, store.EntryDeferred, ReasonDetailsUnavailable, "")
			return "-", nil
		}
		cand.probed = true
		if cand.DurationS == nil {
			cand.DurationS = d.DurationS
		}
		if cand.LiveStatus == "" {
			cand.LiveStatus = d.LiveStatus
		}
	}
	switch {
	case f.Live == LiveSkip && liveStatuses[cand.LiveStatus]:
		return ReasonLive, nil
	case f.ExcludeShorts && cand.Shorts:
		return ReasonShorts, nil
	case f.MinDurationS != nil && cand.DurationS != nil && *cand.DurationS < *f.MinDurationS:
		return ReasonTooShort, nil
	case f.MaxDurationS != nil && cand.DurationS != nil && *cand.DurationS > *f.MaxDurationS:
		return ReasonTooLong, nil
	case f.TitleExclude != "" && matches(f.TitleExclude, cand.Title):
		return ReasonTitleExcluded, nil
	case f.TitleInclude != "" && !matches(f.TitleInclude, cand.Title):
		return ReasonTitleNotIncluded, nil
	}
	return "", nil
}

// matches 以 RE2 比對清理後的標題；儲存前已驗證過，編譯失敗時視為不符合。
func matches(expr, title string) bool {
	re, err := regexp.Compile(expr)
	return err == nil && re.MatchString(title)
}

// selectBackfill 決定舊內容（SUB-R3）：none 全部 before_subscription；recent 與 all 依排序取上限內的，
// 其餘 backfill_limit；上限內的每次檢查最多建立 backfill_per_check 部，其餘 deferred。
func (c *check) selectBackfill(cands []candidate, bf Backfill, known map[string]store.SubscriptionEntry) {
	if bf.Mode == BackfillNone {
		for _, cand := range cands {
			c.decide(cand, store.EntrySkipped, ReasonBeforeSubscription, "")
		}
		return
	}
	c.sortOld(cands)
	// 上限扣掉先前已經建立或排定（deferred）的回補；先前 deferred 的已經在上限內，不再佔用。
	limit := c.backfillCap(bf)
	used := 0
	for _, e := range known {
		if e.Decision == store.EntryCreated && !c.wasNew(e) {
			used++
		}
	}
	budget := c.s.limit.BackfillPerCheck
	for _, cand := range cands {
		if !cand.wasDeferred {
			if used >= limit {
				c.decide(cand, store.EntrySkipped, ReasonBackfillLimit, "")
				continue
			}
		}
		used++
		if budget <= 0 {
			c.decide(cand, store.EntryDeferred, ReasonBackfillPerCheck, "")
			continue
		}
		budget--
		c.toBackfill = append(c.toBackfill, cand)
	}
}

// wasNew 回報先前建立的條目是不是新內容（不佔用回補上限）。
func (c *check) wasNew(e store.SubscriptionEntry) bool {
	if c.sub.Kind == KindChannel {
		return e.PublishedAt != nil && !e.PublishedAt.Before(c.sub.CreatedAt)
	}
	return c.sub.BaselineAt != nil && e.SeenAt.After(*c.sub.BaselineAt)
}

// sortOld 依回補的優先順序排序（SUB-R3）：有發布時間的新的在前；沒有發布時間時依清單順序：
// 頻道與 feed 的前面（新的在前），播放清單與合集的後面（最後加入的在前）。
func (c *check) sortOld(cands []candidate) {
	reverse := c.sub.Kind == KindPlaylist || c.sub.Kind == KindCollection
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.PublishedAt != nil && b.PublishedAt != nil && !a.PublishedAt.Equal(*b.PublishedAt) {
			return a.PublishedAt.After(*b.PublishedAt)
		}
		if reverse {
			return a.Position > b.Position
		}
		return a.Position < b.Position
	})
}

// create 以 ingest 建立影音並套用訂閱的處理方案（快照在新增當下凍結）。已在媒體庫或垃圾桶中的照實記錄；
// 新增失敗時記為 deferred（ingest_failed），下一次檢查再試，不影響其他條目。
func (c *check) create(ctx context.Context, cand candidate, priority int) error {
	var ov map[string]any
	if len(c.sub.Overrides) > 0 {
		_ = json.Unmarshal(c.sub.Overrides, &ov)
	}
	if len(ov) == 0 {
		ov = nil
	}
	req := pipeline.IngestRequest{URL: cand.IngestURL, Profile: c.sub.ProfileID, Overrides: ov, Priority: priority, SubscriptionID: c.sub.ID}
	if cand.Episode != "" {
		req.Episodes = []string{cand.Episode}
	}
	res, err := c.s.d.Ingest.Ingest(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.s.log.WarnContext(ctx, "訂閱新增影音失敗，下一次檢查再試", "subscription", c.sub.ID, "ref", entryRef(cand.Platform, cand.ExternalID, cand.Part))
		c.decide(cand, store.EntryDeferred, ReasonIngestFailed, "")
		return nil
	}
	if len(res) == 0 {
		c.decide(cand, store.EntryDeferred, ReasonIngestFailed, "")
		return nil
	}
	switch r := res[0]; {
	case r.InTrash:
		c.decide(cand, store.EntrySkipped, ReasonInTrash, "")
	case r.AlreadyInLibrary:
		c.decide(cand, store.EntrySkipped, ReasonAlreadyInLibrary, "")
	default:
		c.decide(cand, store.EntryCreated, "", r.Item.ID)
	}
	return nil
}

// decide 記下一個條目的決定。
func (c *check) decide(cand candidate, decision, reason, itemID string) {
	if reason == "-" {
		return
	}
	switch decision {
	case store.EntryCreated:
		c.created++
	case store.EntrySkipped:
		c.skipped++
	}
	c.decided = append(c.decided, store.SubscriptionEntry{
		SubscriptionID: c.sub.ID, Platform: cand.Platform, ExternalID: cand.ExternalID, Part: cand.Part,
		Title: cand.Title, PublishedAt: cand.PublishedAt, DurationS: cand.DurationS, LiveStatus: cand.LiveStatus,
		Position: cand.Position, Decision: decision, Reason: reason, ItemID: itemID, SeenAt: c.seenAt(cand),
	})
}

// seenAt 是條目第一次出現的時間；重新決定的條目保留原本的時間。
func (c *check) seenAt(cand candidate) time.Time {
	if cand.seenAt != nil {
		return *cand.seenAt
	}
	return c.now
}

// persist 寫入這次決定的條目與檢查結果，成功時清除錯誤並發出 subscription.checked。
// complete 為 false（額度用完）時只寫條目，不建立基準清單、不改變錯誤狀態。
func (c *check) persist(ctx context.Context, complete bool) error {
	return c.s.d.Store.Tx(ctx, func(r *store.Repo) error {
		if _, err := r.Subscription(ctx, c.sub.ID); errors.Is(err, store.ErrNotFound) {
			return nil // 檢查期間被刪除
		} else if err != nil {
			return err
		}
		for _, e := range c.decided {
			if err := r.UpsertSubscriptionEntry(ctx, e); err != nil {
				return err
			}
		}
		if !complete {
			return nil
		}
		rec := store.SubscriptionCheck{Title: c.title, ExternalID: c.externalID, Enabled: true, CheckedAt: c.now}
		if c.sub.BaselineAt == nil {
			at := c.now
			rec.BaselineAt = &at
		}
		if err := r.RecordSubscriptionCheck(ctx, c.sub.ID, rec); err != nil {
			return err
		}
		created, skipped := c.created, c.skipped
		return r.EmitSubscriptionEvent(ctx, events.Event{
			Type: events.TypeSubscriptionChecked, Subscription: c.sub.ID, Created: &created, Skipped: &skipped,
		})
	})
}

// fail 記下失敗（last_error、last_error_class）並發出 subscription.check_failed（SUB-R12）。
func (c *check) fail(ctx context.Context, ce *checkError) error {
	return c.s.d.Store.Tx(ctx, func(r *store.Repo) error {
		cur, err := r.Subscription(ctx, c.sub.ID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		rec := store.SubscriptionCheck{
			Title: c.title, ExternalID: c.externalID, Enabled: cur.Enabled, CheckedAt: c.now,
			LastError: ce.Message, LastErrorClass: string(ce.Class), Reevaluate: cur.Reevaluate,
		}
		if err := r.RecordSubscriptionCheck(ctx, c.sub.ID, rec); err != nil {
			return err
		}
		return r.EmitSubscriptionEvent(ctx, events.Event{
			Type: events.TypeSubscriptionCheckFailed, Subscription: c.sub.ID, Class: string(ce.Class), Message: ce.Message,
		})
	})
}

// handleDuplicate 在 @handle 換成頻道 ID 之後檢查重複（SUB-R12）：已有同一個頻道的訂閱時，這個訂閱改成停用，
// 記下 permanent 的「已經訂閱了這個來源。」。沒有重複時寫回解析後的頻道 ID。
func (c *check) handleDuplicate(ctx context.Context) (bool, error) {
	dup := false
	err := c.s.d.Store.Tx(ctx, func(r *store.Repo) error {
		others, err := r.SubscriptionsBySource(ctx, c.sub.Kind, c.sub.Platform, c.externalID)
		if err != nil {
			return err
		}
		for _, o := range others {
			if o.ID != c.sub.ID {
				dup = true
			}
		}
		rec := store.SubscriptionCheck{Title: c.title, ExternalID: c.externalID, Enabled: !dup, CheckedAt: c.now, Reevaluate: c.sub.Reevaluate}
		if !dup {
			// 只寫回頻道 ID；檢查結果之後由 persist 或 fail 寫入。
			rec.CheckedAt = c.now
			rec.LastError, rec.LastErrorClass = c.sub.LastError, c.sub.LastErrorClass
			return r.RecordSubscriptionCheck(ctx, c.sub.ID, rec)
		}
		rec.LastError, rec.LastErrorClass = msgDuplicate, string(errclass.ClassPermanent)
		if err := r.RecordSubscriptionCheck(ctx, c.sub.ID, rec); err != nil {
			return err
		}
		return r.EmitSubscriptionEvent(ctx, events.Event{
			Type: events.TypeSubscriptionCheckFailed, Subscription: c.sub.ID, Class: string(errclass.ClassPermanent), Message: msgDuplicate,
		})
	})
	return dup, err
}

// reconsider 回報記錄過的條目這次要不要重新評估：deferred 的一律要；PATCH backfill 之後，
// 因回補範圍略過的（before_subscription、backfill_limit）也要（SUB-R3，只評估一次）。
func (c *check) reconsider(e store.SubscriptionEntry) bool {
	if e.Decision == store.EntryDeferred {
		return true
	}
	return c.sub.Reevaluate && e.Decision == store.EntrySkipped && (e.Reason == ReasonBeforeSubscription || e.Reason == ReasonBackfillLimit)
}

// mergeStored 把先前記錄的欄位補到這次的條目上（不再重新查詢，SUB-R2）。
// 因暫時的失敗而 deferred、而且是在基準清單之後才出現的條目，仍然是新內容。
func (c *check) mergeStored(cand candidate, e store.SubscriptionEntry) candidate {
	if cand.DurationS == nil {
		cand.DurationS = e.DurationS
	}
	if cand.LiveStatus == "" {
		cand.LiveStatus = e.LiveStatus
	}
	if cand.PublishedAt == nil {
		cand.PublishedAt = e.PublishedAt
	}
	at := e.SeenAt
	cand.seenAt = &at
	cand.wasDeferred = e.Decision == store.EntryDeferred && e.Reason == ReasonBackfillPerCheck
	cand.fresh = e.Decision == store.EntryDeferred && !cand.wasDeferred && c.wasNew(e)
	return cand
}

// storedCandidate 由記錄的條目重建候選（不在這次清單中的 deferred 條目）。
func (c *check) storedCandidate(e store.SubscriptionEntry) candidate {
	sub := c.sub
	cand := candidate{
		Platform: e.Platform, ExternalID: e.ExternalID, Part: e.Part, Title: e.Title, Position: e.Position,
	}
	switch e.Platform {
	case PlatformYouTube:
		cand.IngestURL = "https://www.youtube.com/watch?v=" + e.ExternalID
	case PlatformBilibili:
		cand.IngestURL = "https://www.bilibili.com/video/" + e.ExternalID
	case PlatformPodcast:
		cand.IngestURL = sub.URL
		if _, guid, ok := podcast.SplitExternalID(e.ExternalID); ok {
			cand.Episode = guid
		}
	}
	return c.mergeStored(cand, e)
}
