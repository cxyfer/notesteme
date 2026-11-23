package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/validate"
)

// MaxPreviewRows 是試跑預覽每個清單的上限（SUB-R6）。
const MaxPreviewRows = 200

// msgRateLimited 是預覽沒有額度時的訊息（SUB-R6）。
const msgRateLimited = "這個平台的請求太頻繁，請稍後再試。"

// PreviewRow 是預覽的一列。
type PreviewRow struct {
	Ref         string     `json:"ref" doc:"影音的短代號，例如 yt:abc123xxxxx"`
	Title       string     `json:"title"`
	PublishedAt *time.Time `json:"published_at" nullable:"true"`
	DurationS   *int       `json:"duration_s" nullable:"true"`
}

// PreviewSkip 是預覽中會略過的一列。
type PreviewSkip struct {
	PreviewRow
	Reason string `json:"reason" doc:"略過的原因（同 GET /entries 的 reason）"`
}

// Preview 是試跑預覽的結果（SUB-R6）。兩個清單都依發布時間由新到舊，各最多 MaxPreviewRows 列。
type Preview struct {
	WouldProcess []PreviewRow  `json:"would_process"`
	WouldSkip    []PreviewSkip `json:"would_skip"`
	Truncated    bool          `json:"truncated" doc:"任一個清單超過 200 列而被截斷"`
}

// RateLimitedError 是預覽時沒有額度，或平台回 429／412（SUB-R6、R10）。
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return msgRateLimited }

// PreviewError 是預覽時探索失敗（SUB-R12 的分類與已遮蔽的訊息）。
type PreviewError struct {
	Class   errclass.Class
	Message string
}

func (e *PreviewError) Error() string { return e.Message }

// PreviewInput 是預覽的設定；nil 的欄位用已存的設定（既有訂閱）或預設值（新訂閱）。
type PreviewInput struct {
	Backfill *BackfillInput
	Filters  *FiltersInput
}

// previewRows 收集預覽的決定。
type previewRows struct {
	process []candidate
	skip    []skipped
}

type skipped struct {
	cand   candidate
	reason string
}

// add 記下一個決定：建立的、因 backfill_per_check 延後（之後的檢查會建立）的算「會處理」，其他算「會略過」。
func (p *previewRows) add(cand candidate, decision, reason string) {
	if decision == store.EntryCreated || (decision == store.EntryDeferred && reason == ReasonBackfillPerCheck) {
		p.process = append(p.process, cand)
		return
	}
	p.skip = append(p.skip, skipped{cand: cand, reason: reason})
}

// PreviewNew 試跑還沒建立的訂閱（POST /api/subscriptions/preview）。
func (s *Service) PreviewNew(ctx context.Context, rawURL string, in PreviewInput) (Preview, error) {
	if rawURL == "" {
		return Preview{}, &FieldsError{Fields: []validate.FieldError{{Path: "url", Message: msgURLRequired}}}
	}
	src, err := Recognize(rawURL)
	if err != nil {
		return Preview{}, err
	}
	sub := store.Subscription{Kind: src.Kind, Platform: src.Platform, ExternalID: src.ExternalID, URL: src.URL, Enabled: true}
	return s.preview(ctx, sub, in)
}

// PreviewExisting 試跑既有的訂閱（POST /api/subscriptions/:id/preview）；body 的設定覆蓋已存的設定。
func (s *Service) PreviewExisting(ctx context.Context, id string, in PreviewInput) (Preview, error) {
	sub, err := s.d.Store.Read().Subscription(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	return s.preview(ctx, sub, in)
}

// preview 以和檢查相同的探索與過濾試跑（SUB-R6）：視同「現在」建立的訂閱（沒有基準清單、沒有記錄過的條目），
// 不建立影音、不寫入 subscription_entries、不改變訂閱與排程。對外請求一樣經過 rate limit。
func (s *Service) preview(ctx context.Context, base store.Subscription, in PreviewInput) (Preview, error) {
	_, bf, flt, errs := settings(nil, in.Backfill, in.Filters, base, base.ID != "")
	if len(errs) > 0 {
		return Preview{}, &FieldsError{Fields: errs}
	}
	now := s.d.Clock.Now()
	sub := base
	sub.CreatedAt, sub.BaselineAt, sub.Reevaluate = now, nil, false
	sub.Backfill, _ = json.Marshal(bf)
	sub.Filters, _ = json.Marshal(flt)
	c := &check{s: s, sub: sub, now: now, dry: true}
	if _, err := c.run(ctx); err != nil {
		var (
			de *deferredError
			ce *checkError
		)
		switch {
		case errors.As(err, &de):
			return Preview{}, &RateLimitedError{RetryAfter: de.At.Sub(now)}
		case errors.As(err, &ce) && ce.throttled:
			// run 已經清空並暫停 bucket（與檢查相同）。
			return Preview{}, &RateLimitedError{RetryAfter: c.throttledUntil.Sub(now)}
		case errors.As(err, &ce):
			return Preview{}, &PreviewError{Class: ce.Class, Message: ce.Message}
		case ctx.Err() != nil:
			return Preview{}, ctx.Err()
		}
		return Preview{}, err
	}
	return c.preview.result(), nil
}

// result 依發布時間由新到舊排序（沒有發布時間的排在最後，保持探索的順序），各取前 MaxPreviewRows 列。
func (p *previewRows) result() Preview {
	out := Preview{WouldProcess: []PreviewRow{}, WouldSkip: []PreviewSkip{}}
	sort.SliceStable(p.process, func(i, j int) bool { return newer(p.process[i], p.process[j]) })
	sort.SliceStable(p.skip, func(i, j int) bool { return newer(p.skip[i].cand, p.skip[j].cand) })
	for _, c := range p.process {
		if len(out.WouldProcess) == MaxPreviewRows {
			out.Truncated = true
			break
		}
		out.WouldProcess = append(out.WouldProcess, row(c))
	}
	for _, sk := range p.skip {
		if len(out.WouldSkip) == MaxPreviewRows {
			out.Truncated = true
			break
		}
		out.WouldSkip = append(out.WouldSkip, PreviewSkip{PreviewRow: row(sk.cand), Reason: sk.reason})
	}
	return out
}

func newer(a, b candidate) bool {
	switch {
	case a.PublishedAt == nil:
		return false
	case b.PublishedAt == nil:
		return true
	}
	return a.PublishedAt.After(*b.PublishedAt)
}

func row(c candidate) PreviewRow {
	return PreviewRow{Ref: entryRef(c.Platform, c.ExternalID, c.Part), Title: c.Title, PublishedAt: c.PublishedAt, DurationS: c.DurationS}
}
