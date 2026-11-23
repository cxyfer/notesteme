package digest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/cxyfer/notesteme/internal/notify"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// 內容的上限（DIG-R9、R10）。
const (
	maxListed         = 50
	maxSummaryInText  = 280
	noSummary         = "（沒有摘要）"
	awaitingLabel     = "等你決定"
	noPublicURLFooter = "設定 server.public_url 後，彙整會附上連結。"
)

// DigestEntry 是彙整中的一部影音（webhook 的 items[]，DIG-R10）。
type DigestEntry struct {
	ID               string  `json:"id"`
	Ref              string  `json:"ref"`
	Title            string  `json:"title"`
	TitleTranslation *string `json:"title_translation"`
	Channel          string  `json:"channel"`
	DurationS        *int    `json:"duration_s"`
	Summary          *string `json:"summary"`
	AwaitingUser     bool    `json:"awaiting_user"`
	URL              *string `json:"url"`
}

// DigestRange 是 webhook 的 digest 欄位（DIG-R10）。
type DigestRange struct {
	ID     string    `json:"id"`
	Period string    `json:"period"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

// content 是一次彙整的內容。
type content struct {
	title   string
	text    string
	entries []DigestEntry // 最多 maxListed 部
	all     []string      // 範圍內全部的影音（寫入 digest_entries）
}

// DigestPreviewItem 是預覽回傳的一部影音（DIG-R11）。
type DigestPreviewItem struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	TitleTranslation *string `json:"title_translation"`
	Summary          *string `json:"summary"`
	AwaitingUser     bool    `json:"awaiting_user"`
}

// DigestPreview 是 POST /api/digests/:id/preview 的結果（DIG-R11）。
type DigestPreview struct {
	Items []DigestPreviewItem `json:"items"`
	Text  string              `json:"text"`
}

// run 是排程工作 digest（DIG-R8、R9）：範圍是 (上次涵蓋的終點, 不晚於現在的最近一個排定時間]，
// 沒有任何影音時只記錄範圍終點、不送出；下一次排在現在之後的下一個 at。
func (s *Service) run(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.Result, error) {
	d, err := s.d.Store.Read().Digest(ctx, job.Key)
	if errors.Is(err, store.ErrNotFound) {
		return scheduler.Result{Done: true}, nil
	}
	if err != nil {
		return scheduler.Result{}, err
	}
	if !d.Enabled {
		return scheduler.Result{Done: true}, nil
	}
	sch, err := scheduleOf(d)
	if err != nil {
		return scheduler.Result{}, fmt.Errorf("彙整 %s 的排程不正確：%w", d.ID, err)
	}
	end := sch.prev(now)
	if end.Before(job.NextRunAt) {
		end = job.NextRunAt
	}
	start := job.NextRunAt.Add(-sch.period())
	if d.LastRunAt != nil {
		start = *d.LastRunAt
	}
	c, err := s.collect(ctx, d, sch, start, end)
	if err != nil {
		return scheduler.Result{}, err
	}
	err = s.d.Store.Tx(ctx, func(r *store.Repo) error {
		if err := r.SetDigestLastRun(ctx, d.ID, end); err != nil {
			return err
		}
		if len(c.all) == 0 {
			return nil // DIG-R9：沒有任何影音時不送出、不寫入 outbox
		}
		for _, id := range c.all {
			if err := r.InsertDigestEntry(ctx, d.ID, id, end); err != nil {
				return err
			}
		}
		rng, err := json.Marshal(DigestRange{ID: d.ID, Period: d.Period, Start: start.UTC(), End: end.UTC()})
		if err != nil {
			return err
		}
		items, err := json.Marshal(c.entries)
		if err != nil {
			return err
		}
		_, err = s.d.Outbox.EmitTo(ctx, r, notify.Event{
			Name: notify.EventDigest, DedupeKey: notify.DedupeDigest(d.ID, end),
			Summary: c.title, Message: c.text, Digest: rng, Items: items,
		}, d.Notifiers)
		return err
	})
	if err != nil {
		return scheduler.Result{}, err
	}
	s.d.Log.InfoContext(ctx, "彙整已執行", "digest", d.ID, "items", len(c.all), "end", end)
	return scheduler.Result{Next: sch.next(now)}, nil
}

// DigestPreview 回傳「現在執行」會送出的內容（DIG-R11）：範圍的終點是現在，不送出、不寫入 digest_entries、不改變下次執行時間。
func (s *Service) Preview(ctx context.Context, id string) (DigestPreview, error) {
	r := s.d.Store.Read()
	d, err := r.Digest(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return DigestPreview{}, ErrNotFound
	}
	if err != nil {
		return DigestPreview{}, err
	}
	sch, err := scheduleOf(d)
	if err != nil {
		return DigestPreview{}, err
	}
	now := s.d.Clock.Now()
	start := sch.next(now).Add(-sch.period())
	if next, err := nextRun(ctx, r, id); err != nil {
		return DigestPreview{}, err
	} else if next != nil {
		start = next.Add(-sch.period())
	}
	if d.LastRunAt != nil {
		start = *d.LastRunAt
	}
	c, err := s.collect(ctx, d, sch, start, now)
	if err != nil {
		return DigestPreview{}, err
	}
	out := DigestPreview{Items: make([]DigestPreviewItem, 0, len(c.entries)), Text: c.title + "\n" + c.text}
	for _, e := range c.entries {
		out.Items = append(out.Items, DigestPreviewItem{ID: e.ID, Title: e.Title, TitleTranslation: e.TitleTranslation, Summary: e.Summary, AwaitingUser: e.AwaitingUser})
	}
	return out, nil
}

// collect 讀出範圍內的影音並組出內容（DIG-R9、R10）。
func (s *Service) collect(ctx context.Context, d store.Digest, sch schedule, start, end time.Time) (content, error) {
	r := s.d.Store.Read()
	cands, err := r.DigestCandidates(ctx, d.ID, start, end, d.Scope == ScopeAll)
	if err != nil {
		return content{}, err
	}
	c := content{all: make([]string, 0, len(cands))}
	for _, cand := range cands {
		c.all = append(c.all, cand.ItemID)
	}
	for _, id := range c.all[:min(len(c.all), maxListed)] {
		e, err := s.entry(ctx, r, id)
		if err != nil {
			return content{}, err
		}
		c.entries = append(c.entries, e)
	}
	c.title = sch.title(start, end, len(c.all))
	c.text = s.text(c)
	return c, nil
}

// entry 讀出一部影音在彙整中的資料。摘要讀不到時當成沒有摘要，不讓整則彙整失敗。
func (s *Service) entry(ctx context.Context, r *store.Repo, id string) (DigestEntry, error) {
	it, err := r.ItemByID(ctx, id)
	if err != nil {
		return DigestEntry{}, err
	}
	ni, err := notify.ItemOf(ctx, r, id)
	if err != nil {
		return DigestEntry{}, err
	}
	awaiting, err := pipeline.AwaitingUser(ctx, r, id)
	if err != nil {
		return DigestEntry{}, err
	}
	e := DigestEntry{ID: it.ID, Ref: ni.Ref, Title: notify.CleanTitle(ni.Title), Channel: notify.CleanTitle(ni.Channel), DurationS: it.DurationS, AwaitingUser: awaiting}
	if e.Title == "" {
		e.Title = notify.CleanTitle(ni.Ref)
	}
	if s.d.Summaries != nil {
		sm, ok, err := s.d.Summaries.ItemSummary(ctx, id)
		switch {
		case err != nil:
			s.d.Log.WarnContext(ctx, "讀取摘要失敗，彙整中顯示沒有摘要", "item", id, "error", err)
		case ok:
			text := cleanBlock(sm.Text)
			e.Summary = &text
			if sm.TitleTranslation != nil {
				if t := notify.CleanTitle(*sm.TitleTranslation); t != "" {
					e.TitleTranslation = &t
				}
			}
		}
	}
	if s.d.PublicURL != "" {
		u := s.d.PublicURL + "/items/" + url.PathEscape(it.ID)
		e.URL = &u
	}
	return e, nil
}

// text 是文字管道的內文（DIG-R10）：每部影音一段，連結只指向 UI（開啟時要登入），不含任何憑證。
func (s *Service) text(c content) string {
	var b strings.Builder
	awaiting := false
	for k, e := range c.entries {
		if k > 0 {
			b.WriteString("\n\n")
		}
		head := e.Title
		if e.TitleTranslation != nil {
			head += "（" + *e.TitleTranslation + "）"
		}
		b.WriteString(head)
		var meta []string
		if e.Channel != "" {
			meta = append(meta, e.Channel)
		}
		if e.DurationS != nil && *e.DurationS > 0 {
			meta = append(meta, pipeline.HMS(int64(*e.DurationS)))
		}
		if len(meta) > 0 {
			b.WriteString("\n" + strings.Join(meta, "・"))
		}
		summary := noSummary
		if e.Summary != nil {
			summary = truncate(strings.Join(strings.Fields(*e.Summary), " "), maxSummaryInText)
		}
		b.WriteString("\n" + summary)
		if e.AwaitingUser {
			awaiting = true
			b.WriteString("\n" + awaitingLabel)
		}
		if e.URL != nil {
			b.WriteString("\n" + *e.URL)
		}
	}
	if more := len(c.all) - len(c.entries); more > 0 {
		fmt.Fprintf(&b, "\n\n還有 %d 部，請到 Notesteme 查看。", more)
	}
	switch {
	case s.d.PublicURL == "":
		b.WriteString("\n\n" + noPublicURLFooter)
	case awaiting:
		b.WriteString("\n\n" + awaitingLabel + "：" + s.d.PublicURL + "/library?blocked=awaiting_user")
	}
	return strings.TrimSpace(b.String())
}

// truncate 超過 n 個字元時截斷並加「…」。
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// cleanBlock 移除換行以外的控制字元與格式字元（摘要是模型的輸出，不可信）。
func cleanBlock(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "")))
}
