package subscription

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
)

// fakeIngest 記下每次新增；fail 非 nil 時回傳錯誤。
type fakeIngest struct {
	mu   sync.Mutex
	reqs []pipeline.IngestRequest
	fail error
	plan error
}

func (f *fakeIngest) Ingest(_ context.Context, req pipeline.IngestRequest) ([]pipeline.IngestResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.reqs = append(f.reqs, req)
	return []pipeline.IngestResult{{Item: store.Item{ID: fmt.Sprintf("item-%d", len(f.reqs))}}}, nil
}

func (f *fakeIngest) CheckIngestPlan(context.Context, string, map[string]any) error { return f.plan }

// feedServer 是可變的假 Podcast feed。
type feedServer struct {
	mu       sync.Mutex
	episodes []string // guid，由新到舊
	status   int
	requests int
}

func (f *feedServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>Show &amp; Tell</title>`)
	for _, g := range f.episodes {
		fmt.Fprintf(&b, `<item><guid>%s</guid><title>Episode <b>%s</b></title><enclosure url="/audio/%s.mp3" type="audio/mpeg"/></item>`, g, g, g)
	}
	b.WriteString(`</channel></rss>`)
	_, _ = w.Write([]byte(b.String()))
}

func (f *feedServer) set(status int, eps ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
	if eps != nil {
		f.episodes = eps
	}
}

type svcRig struct {
	svc  *Service
	st   *store.Store
	clk  *clock.Fake
	ing  *fakeIngest
	feed *feedServer
	url  string
}

func newSvcRig(t *testing.T, cfg config.Subscriptions) *svcRig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	feed := &feedServer{episodes: []string{"e2", "e1"}}
	srv := httptest.NewServer(feed)
	t.Cleanup(srv.Close)
	ing := &fakeIngest{}
	svc := New(Deps{Store: st, Clock: clk, HTTP: srv.Client(), Ingest: ing, Config: cfg})
	return &svcRig{svc: svc, st: st, clk: clk, ing: ing, feed: feed, url: srv.URL + "/show.xml"}
}

// check 執行一次 subscription.check（如同排程器在 now 執行）。
func (r *svcRig) check(t *testing.T, id string) (scheduler.Result, error) {
	t.Helper()
	return r.svc.runCheck(t.Context(), scheduler.Job{Kind: JobKind, Key: id}, r.clk.Now())
}

func (r *svcRig) entries(t *testing.T, id string) map[string]string {
	t.Helper()
	es, err := r.svc.Entries(t.Context(), id, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range es {
		reason := ""
		if e.Reason != nil {
			reason = *e.Reason
		}
		out[e.ExternalID] = e.Decision + "/" + reason
	}
	return out
}

func TestServiceFeedLifecycle(t *testing.T) {
	r := newSvcRig(t, config.Subscriptions{})
	ctx := t.Context()
	v, err := r.svc.Create(ctx, CreateInput{URL: r.url})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindFeed || v.Schedule != "6h" || v.NextCheckAt == nil || !v.NextCheckAt.Equal(r.clk.Now()) || !v.Enabled {
		t.Fatalf("建立 = %+v", v)
	}
	// 同一個來源（大小寫、結尾不同但正規化相同）再訂閱一次是 409，帶既有的 id。
	_, err = r.svc.Create(ctx, CreateInput{URL: r.url})
	var dup *DuplicateError
	if !errors.As(err, &dup) || dup.SubscriptionID != v.ID {
		t.Fatalf("重複：%v", err)
	}

	// 第一次檢查：既有的集數是基準，不新增（SUB-R3 none）。
	res, err := r.check(t, v.ID)
	if err != nil || res.Done || res.Deferred || !res.Next.Equal(r.clk.Now().Add(6*time.Hour)) {
		t.Fatalf("第一次檢查：%+v %v", res, err)
	}
	if len(r.ing.reqs) != 0 {
		t.Fatalf("基準不應新增：%+v", r.ing.reqs)
	}
	got, _ := r.svc.Get(ctx, v.ID)
	if got.Title != "Show & Tell" || got.LastCheckedAt == nil || got.LastError != nil {
		t.Fatalf("檢查後 = %+v", got)
	}

	// 之後出現的集數才新增，帶訂閱的 id 與 guid。
	r.clk.Advance(6 * time.Hour)
	r.feed.set(0, "e3", "e2", "e1")
	if _, err := r.check(t, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.ing.reqs) != 1 || r.ing.reqs[0].SubscriptionID != v.ID || len(r.ing.reqs[0].Episodes) != 1 || r.ing.reqs[0].Episodes[0] != "e3" {
		t.Fatalf("新增 = %+v", r.ing.reqs)
	}
	// 同一集再出現不再新增（SUB-R5）。
	r.clk.Advance(6 * time.Hour)
	if _, err := r.check(t, v.ID); err != nil || len(r.ing.reqs) != 1 {
		t.Fatalf("重複出現：%v %d", err, len(r.ing.reqs))
	}
	var created int
	for _, d := range r.entries(t, v.ID) {
		if strings.HasPrefix(d, "created/") {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("條目 = %v", r.entries(t, v.ID))
	}

	// 404：permanent，不退避（Next 是一個間隔之後），訊息逐字。
	r.clk.Advance(6 * time.Hour)
	r.feed.set(http.StatusNotFound)
	res, err = r.check(t, v.ID)
	if err != nil || !res.Next.Equal(r.clk.Now().Add(6*time.Hour)) {
		t.Fatalf("404：%+v %v", res, err)
	}
	got, _ = r.svc.Get(ctx, v.ID)
	if got.LastErrorClass == nil || *got.LastErrorClass != "permanent" || *got.LastError != "訂閱來源已不存在（HTTP 404）。請確認網址，或刪除這個訂閱。" {
		t.Fatalf("404 之後 = %+v", got)
	}
	// 503：retryable，回傳錯誤讓排程器退避。
	r.feed.set(http.StatusServiceUnavailable)
	if _, err := r.check(t, v.ID); err == nil || err.Error() != "暫時無法檢查訂閱（HTTP 503），稍後會自動重試。" {
		t.Fatalf("503：%v", err)
	}
	// 429：RetryAfter，不早於 bucket 恢復（15 分鐘）。
	r.feed.set(http.StatusTooManyRequests)
	_, err = r.check(t, v.ID)
	var ra *scheduler.RetryAfter
	if !errors.As(err, &ra) || !ra.NotBefore.Equal(r.clk.Now().Add(throttlePause)) {
		t.Fatalf("429：%v", err)
	}
	// 暫停中再檢查：Deferred 到恢復時間，不連線。
	before := r.feed.requests
	res, err = r.check(t, v.ID)
	if err != nil || !res.Deferred || !res.Next.Equal(ra.NotBefore) || r.feed.requests != before {
		t.Fatalf("暫停中：%+v %v，請求 %d→%d", res, err, before, r.feed.requests)
	}
	// 恢復後成功，錯誤清空（SUB-R12）。
	r.clk.Set(ra.NotBefore)
	r.feed.set(0)
	if _, err := r.check(t, v.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.svc.Get(ctx, v.ID); got.LastError != nil || got.LastErrorClass != nil {
		t.Fatalf("恢復後 = %+v", got)
	}

	// 停用：不排程，檢查直接結束；刪除後檢查也直接結束。
	off := false
	if got, err = r.svc.Patch(ctx, v.ID, PatchInput{Enabled: &off}); err != nil || got.NextCheckAt != nil {
		t.Fatalf("停用：%+v %v", got, err)
	}
	if res, err := r.check(t, v.ID); err != nil || !res.Done {
		t.Fatalf("停用後檢查：%+v %v", res, err)
	}
	if err := r.svc.Delete(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if res, err := r.check(t, v.ID); err != nil || !res.Done {
		t.Fatalf("刪除後檢查：%+v %v", res, err)
	}
	if _, err := r.svc.Get(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("刪除後讀取：%v", err)
	}
}

func TestServiceIngestFailureIsDeferred(t *testing.T) {
	r := newSvcRig(t, config.Subscriptions{})
	v, err := r.svc.Create(t.Context(), CreateInput{URL: r.url, Backfill: &BackfillInput{Mode: BackfillAll}})
	if err != nil {
		t.Fatal(err)
	}
	r.ing.fail = errors.New("db busy")
	if _, err := r.check(t, v.ID); err != nil {
		t.Fatal(err)
	}
	if e := r.entries(t, v.ID); e[podcastIDFor(t, r, v.ID, "e1")] != "deferred/"+ReasonIngestFailed {
		t.Fatalf("新增失敗應是 deferred：%v", e)
	}
	// 下一次檢查再試，成功後變成 created。
	r.ing.fail = nil
	r.clk.Advance(6 * time.Hour)
	if _, err := r.check(t, v.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.ing.reqs) != 2 {
		t.Fatalf("重試新增 = %d", len(r.ing.reqs))
	}
}

// podcastIDFor 回傳 guid 對應的條目 external_id（條目的 external_id 是 feed key 與 guid 的組合）。
func podcastIDFor(t *testing.T, r *svcRig, id, guid string) string {
	t.Helper()
	es, err := r.svc.Entries(t.Context(), id, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if strings.HasSuffix(e.ExternalID, guid) {
			return e.ExternalID
		}
	}
	t.Fatalf("沒有 guid %s 的條目", guid)
	return ""
}

func TestServiceBackfillCap(t *testing.T) {
	r := newSvcRig(t, config.Subscriptions{BackfillMax: 3, BackfillPerCheck: 2})
	eps := []string{}
	for i := 9; i >= 1; i-- {
		eps = append(eps, fmt.Sprintf("e%d", i))
	}
	r.feed.set(0, eps...)
	v, err := r.svc.Create(t.Context(), CreateInput{URL: r.url, Backfill: &BackfillInput{Mode: BackfillAll}})
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, err := r.check(t, v.ID); err != nil {
			t.Fatal(err)
		}
		r.clk.Advance(6 * time.Hour)
	}
	// 上限 3：每次最多 2 部，分兩次完成，之後不再新增。
	if len(r.ing.reqs) != 3 {
		t.Fatalf("回補新增 %d 部；預期 3 部", len(r.ing.reqs))
	}
	var limited int
	for _, d := range r.entries(t, v.ID) {
		if d == "skipped/"+ReasonBackfillLimit {
			limited++
		}
	}
	if limited != 6 {
		t.Fatalf("超過上限的條目 = %d：%v", limited, r.entries(t, v.ID))
	}
}

func TestServiceValidation(t *testing.T) {
	r := newSvcRig(t, config.Subscriptions{})
	ctx := t.Context()
	bad := "30m"
	cases := []struct {
		in   CreateInput
		path string
	}{
		{CreateInput{}, "url"},
		{CreateInput{URL: r.url, Schedule: &bad}, "schedule"},
		{CreateInput{URL: r.url, Backfill: &BackfillInput{Mode: BackfillRecent}}, "backfill.count"},
		{CreateInput{URL: r.url, Filters: &FiltersInput{MinDurationS: intp(10), MaxDurationS: intp(5)}}, "filters.max_duration_s"},
	}
	for _, c := range cases {
		_, err := r.svc.Create(ctx, c.in)
		var fe *FieldsError
		if !errors.As(err, &fe) || fe.Fields[0].Path != c.path {
			t.Errorf("Create(%+v) = %v；預期 %s", c.in, err, c.path)
		}
	}
	var ue *URLError
	if _, err := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/watch?v=abcdefghijk"}); !errors.As(err, &ue) || ue.Code != CodeNotACollection {
		t.Errorf("單一影片：%v", err)
	}
	r.ing.plan = errors.New("處理方案不存在。")
	if _, err := r.svc.Create(ctx, CreateInput{URL: r.url, Profile: "nope"}); err == nil || err.Error() != "處理方案不存在。" {
		t.Errorf("處理方案：%v", err)
	}
	if n, _ := r.svc.Count(ctx); n != 0 {
		t.Errorf("驗證失敗不應建立訂閱：%d", n)
	}
	if _, err := r.svc.Patch(ctx, "missing", PatchInput{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("修改不存在的訂閱：%v", err)
	}
}

func TestServiceRateLimitDefersFeedHost(t *testing.T) {
	r := newSvcRig(t, config.Subscriptions{RateLimits: config.RateLimits{FeedHost: 1}})
	ctx := t.Context()
	a, err := r.svc.Create(ctx, CreateInput{URL: r.url})
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Create(ctx, CreateInput{URL: r.url + "?other=1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.check(t, a.ID); err != nil {
		t.Fatal(err)
	}
	// 同一個主機每分鐘 1 次：第二個訂閱延到下一顆額度，不連線、不算失敗。
	before := r.feed.requests
	res, err := r.check(t, b.ID)
	if err != nil || !res.Deferred || !res.Next.Equal(r.clk.Now().Add(time.Minute)) || r.feed.requests != before {
		t.Fatalf("額度用完：%+v %v", res, err)
	}
	got, _ := r.svc.Get(ctx, b.ID)
	if got.LastError != nil || got.LastCheckedAt != nil {
		t.Fatalf("延後不算檢查：%+v", got)
	}
}
