package scheduler

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

var t0 = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func openStore(t *testing.T) (*store.Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	s, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clk
}

// recorder 是測試用的 Handler：記錄執行的 key，依 results 回傳結果。
type recorder struct {
	ran     []string
	results map[string]func(Job, time.Time) (Result, error)
}

func (r *recorder) run(_ context.Context, j Job, now time.Time) (Result, error) {
	r.ran = append(r.ran, j.Key)
	if f := r.results[j.Key]; f != nil {
		return f(j, now)
	}
	return Result{}, nil
}

func job(t *testing.T, s *store.Store, kind, key string) store.ScheduledJob {
	t.Helper()
	j, err := s.Read().ScheduledJob(context.Background(), kind, key)
	if err != nil {
		t.Fatalf("%s/%s：%v", kind, key, err)
	}
	return j
}

// 註冊表是空的：Run、NextAt 不查資料庫（關掉資料庫也不會失敗），worker 的時間不變（M4-S0）。
func TestEmptyRegistryTouchesNothing(t *testing.T) {
	s, clk := openStore(t)
	if err := s.Write().ScheduleJob(context.Background(), "test.kind", "a", t0); err != nil {
		t.Fatal(err)
	}
	sc := New(s, clk, nil, 0)
	_ = s.Close()
	if err := sc.Run(context.Background()); err != nil {
		t.Errorf("Run = %v", err)
	}
	if _, ok, err := sc.NextAt(context.Background()); ok || err != nil {
		t.Errorf("NextAt = %v %v", ok, err)
	}
	if err := sc.Register(Kind{Name: "late", Interval: time.Hour, Run: (&recorder{}).run}); !errors.Is(err, ErrFrozen) {
		t.Errorf("開始執行之後登記：%v", err)
	}
}

func TestRegisterRejects(t *testing.T) {
	s, clk := openStore(t)
	sc := New(s, clk, nil, 0)
	run := (&recorder{}).run
	if err := sc.Register(Kind{Name: "a", Interval: time.Hour, Run: run}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []Kind{{Name: "a", Interval: time.Hour, Run: run}, {Name: "", Run: run}, {Name: "b"}, {Name: "c", Interval: -1, Run: run}} {
		if err := sc.Register(k); err == nil {
			t.Errorf("%+v 應被拒絕", k)
		}
	}
	if !slices.Equal(sc.Kinds(), []string{"a"}) {
		t.Errorf("Kinds = %v", sc.Kinds())
	}
}

// 到期的工作依 next_run_at、再依建立順序執行，一輪各一次；沒到期與未登記的 kind 不執行。
func TestRunOrderAndNextAt(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	rec := &recorder{}
	sc := New(s, clk, nil, 0)
	if err := sc.Register(Kind{Name: "test.kind", Interval: time.Hour, Run: rec.run}); err != nil {
		t.Fatal(err)
	}
	w := s.Write()
	for _, j := range []struct {
		key string
		at  time.Time
	}{{"second", t0}, {"first", t0.Add(-time.Minute)}, {"third", t0}, {"future", t0.Add(time.Minute)}} {
		if err := w.ScheduleJob(ctx, "test.kind", j.key, j.at); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.ScheduleJob(ctx, "other.kind", "x", t0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if at, ok, err := sc.NextAt(ctx); err != nil || !ok || !at.Equal(t0.Add(-time.Minute)) {
		t.Fatalf("NextAt = %v %v %v（未登記的 kind 不算）", at, ok, err)
	}
	if err := sc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rec.ran, []string{"first", "second", "third"}) {
		t.Errorf("執行順序 = %v", rec.ran)
	}
	// 成功：下一次是這次執行的時間＋間隔（不是排定的時間＋間隔），失敗次數歸零。
	if j := job(t, s, "test.kind", "first"); !j.NextRunAt.Equal(t0.Add(time.Hour)) || j.LastOKAt == nil || !j.LastOKAt.Equal(t0) || j.Failures != 0 {
		t.Errorf("first = %+v", j)
	}
	if at, _, _ := sc.NextAt(ctx); !at.Equal(t0.Add(time.Minute)) {
		t.Errorf("NextAt = %v", at)
	}
	// 同一輪不重複；時間沒有前進時再跑一輪也不會執行。
	rec.ran = nil
	if err := sc.Run(ctx); err != nil || len(rec.ran) != 0 {
		t.Errorf("沒有到期：%v %v", rec.ran, err)
	}
	clk.Advance(time.Minute)
	if err := sc.Run(ctx); err != nil || !slices.Equal(rec.ran, []string{"future"}) {
		t.Errorf("一分鐘後：%v %v", rec.ran, err)
	}
}

// 停機很久之後只補跑一次：下一次從這次執行的時間起算。
func TestCatchUpRunsOnce(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	rec := &recorder{}
	sc := New(s, clk, nil, 0)
	_ = sc.Register(Kind{Name: "test.kind", Interval: time.Hour, Run: rec.run})
	if err := s.Write().ScheduleJob(ctx, "test.kind", "a", t0); err != nil {
		t.Fatal(err)
	}
	clk.Advance(7 * 24 * time.Hour)
	for range 3 {
		if err := sc.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.ran) != 1 {
		t.Errorf("停機一週後執行了 %d 次", len(rec.ran))
	}
	if j := job(t, s, "test.kind", "a"); !j.NextRunAt.Equal(clk.Now().Add(time.Hour)) {
		t.Errorf("下一次 = %v", j.NextRunAt)
	}
}

// 失敗依連續次數退避：1 分鐘、5 分鐘、30 分鐘，之後固定 6 小時；成功後歸零。panic 也算失敗。
func TestFailureBackoff(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	fail := true
	rec := &recorder{results: map[string]func(Job, time.Time) (Result, error){
		"a": func(Job, time.Time) (Result, error) {
			if fail {
				return Result{}, errors.New(strings.Repeat("壞", 600))
			}
			return Result{}, nil
		},
		"p": func(Job, time.Time) (Result, error) { panic("boom") },
	}}
	sc := New(s, clk, nil, 0)
	_ = sc.Register(Kind{Name: "test.kind", Interval: time.Hour, Run: rec.run})
	for _, k := range []string{"a", "p"} {
		if err := s.Write().ScheduleJob(ctx, "test.kind", k, t0); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 6 * time.Hour, 6 * time.Hour} {
		now := clk.Now()
		if err := sc.Run(ctx); err != nil {
			t.Fatal(err)
		}
		j := job(t, s, "test.kind", "a")
		if j.Failures != i+1 || !j.NextRunAt.Equal(now.Add(want)) || j.LastOKAt != nil {
			t.Fatalf("第 %d 次失敗：%+v，預期 %v 後重試", i+1, j, want)
		}
		if n := len([]rune(j.LastError)); n != maxErrorLen+1 {
			t.Errorf("last_error 長度 = %d", n)
		}
		clk.Set(j.NextRunAt)
	}
	if j := job(t, s, "test.kind", "p"); j.Failures == 0 || !strings.Contains(j.LastError, "panic") {
		t.Errorf("panic：%+v", j)
	}
	fail = false
	if err := sc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if j := job(t, s, "test.kind", "a"); j.Failures != 0 || j.LastError != "" || j.LastOKAt == nil {
		t.Errorf("成功後：%+v", j)
	}
	if Backoff(0) != time.Minute || Backoff(100) != 6*time.Hour {
		t.Error("Backoff 的邊界")
	}
}

// Result 的 Next、Done、Deferred。
func TestResults(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	at := t0.Add(3 * time.Hour)
	rec := &recorder{results: map[string]func(Job, time.Time) (Result, error){
		"next":  func(Job, time.Time) (Result, error) { return Result{Next: at}, nil },
		"done":  func(Job, time.Time) (Result, error) { return Result{Done: true}, nil },
		"defer": func(Job, time.Time) (Result, error) { return Result{Deferred: true, Next: at}, nil },
		"bad":   func(Job, time.Time) (Result, error) { return Result{Deferred: true}, nil },
	}}
	sc := New(s, clk, nil, 0)
	_ = sc.Register(Kind{Name: "test.kind", Interval: time.Hour, Run: rec.run})
	_ = sc.Register(Kind{Name: "aligned", Run: func(context.Context, Job, time.Time) (Result, error) { return Result{}, nil }})
	w := s.Write()
	for _, k := range []string{"next", "done", "defer"} {
		_ = w.ScheduleJob(ctx, "test.kind", k, t0)
	}
	_ = w.ScheduleJob(ctx, "aligned", "x", t0)
	// defer 之前先失敗過一次：Deferred 不改 failures 與 last_error。
	_ = w.RecordScheduledJobRun(ctx, "test.kind", "defer", store.ScheduledJobRun{NextRunAt: t0, LastRunAt: t0, LastError: "old", Failures: 1})
	if err := sc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if j := job(t, s, "test.kind", "next"); !j.NextRunAt.Equal(at) {
		t.Errorf("next = %+v", j)
	}
	if _, err := s.Read().ScheduledJob(ctx, "test.kind", "done"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("done 應被刪除：%v", err)
	}
	if j := job(t, s, "test.kind", "defer"); !j.NextRunAt.Equal(at) || j.Failures != 1 || j.LastError != "old" || j.LastOKAt != nil {
		t.Errorf("defer = %+v", j)
	}
	// 沒有間隔的 kind 一定要回傳下一次的時間，否則算失敗（不會停在過去每一輪重試）。
	if j := job(t, s, "aligned", "x"); j.Failures != 1 || !j.NextRunAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("aligned = %+v", j)
	}
	// Deferred 沒有 Next 是程式錯誤，回傳給 worker。
	_ = w.ScheduleJob(ctx, "test.kind", "bad", t0)
	if err := sc.Run(ctx); err == nil {
		t.Error("Deferred 沒有 Next 應回傳錯誤")
	}
}

// 30 秒的單輪上限（以注入的 clock 判斷）：用完時其餘的工作留到下一輪。
func TestRoundBudget(t *testing.T) {
	ctx := context.Background()
	s, clk := openStore(t)
	rec := &recorder{results: map[string]func(Job, time.Time) (Result, error){
		"slow": func(Job, time.Time) (Result, error) { clk.Advance(RoundBudget); return Result{}, nil },
	}}
	sc := New(s, clk, nil, 0)
	_ = sc.Register(Kind{Name: "test.kind", Interval: time.Hour, Run: rec.run})
	_ = s.Write().ScheduleJob(ctx, "test.kind", "slow", t0)
	_ = s.Write().ScheduleJob(ctx, "test.kind", "next", t0)
	if err := sc.Run(ctx); err != nil || !slices.Equal(rec.ran, []string{"slow"}) {
		t.Fatalf("第一輪：%v %v", rec.ran, err)
	}
	if err := sc.Run(ctx); err != nil || !slices.Equal(rec.ran, []string{"slow", "next"}) {
		t.Errorf("第二輪：%v %v", rec.ran, err)
	}
}

// jitter 是確定性的：同一個 (kind, key, 時段) 一定得到同一個延遲，落在 [0, min(間隔 × 10%, max))；max 為 0 時沒有延遲。
func TestJitter(t *testing.T) {
	interval := 6 * time.Hour
	seen := map[time.Duration]bool{}
	for slot := range int64(50) {
		d := Jitter("subscription.check", "s1", slot, interval, 30*time.Minute)
		if d != Jitter("subscription.check", "s1", slot, interval, 30*time.Minute) {
			t.Fatal("jitter 不是確定性的")
		}
		if d < 0 || d >= 30*time.Minute {
			t.Errorf("jitter = %v 超出範圍", d)
		}
		seen[d] = true
	}
	if len(seen) < 40 {
		t.Errorf("jitter 幾乎沒有變化：%d 種", len(seen))
	}
	if Jitter("k", "a", 1, interval, 0) != 0 || Jitter("k", "a", 1, 0, time.Hour) != 0 {
		t.Error("max 或間隔為 0 時沒有延遲")
	}
	// 間隔的 10% 比 max 小時以 10% 為上限：取多個時段，最大值要接近 6 分鐘但不能到。
	var top time.Duration
	for slot := range int64(200) {
		d := Jitter("k", "a", slot, time.Hour, time.Hour)
		if d >= 6*time.Minute {
			t.Errorf("間隔 1 小時的上限是 6 分鐘：%v", d)
		}
		top = max(top, d)
	}
	if top < 5*time.Minute {
		t.Errorf("200 個時段的最大延遲只有 %v，上限算錯了", top)
	}
	if Jitter("k", "a", 1, interval, time.Hour) == Jitter("k", "b", 1, interval, time.Hour) &&
		Jitter("k", "a", 2, interval, time.Hour) == Jitter("k", "b", 2, interval, time.Hour) {
		t.Error("不同的 key 應該有不同的延遲")
	}

	// Kind.Jitter：預設的下一次加上延遲；jitter_max 為 0 時是精確的間隔。
	ctx := context.Background()
	for _, max := range []time.Duration{0, 30 * time.Minute} {
		s, clk := openStore(t)
		sc := New(s, clk, nil, max)
		_ = sc.Register(Kind{Name: "subscription.check", Interval: interval, Jitter: true, Run: (&recorder{}).run})
		_ = s.Write().ScheduleJob(ctx, "subscription.check", "s1", t0)
		if err := sc.Run(ctx); err != nil {
			t.Fatal(err)
		}
		base := t0.Add(interval)
		want := base.Add(Jitter("subscription.check", "s1", base.UnixMilli()/interval.Milliseconds(), interval, max))
		if j := job(t, s, "subscription.check", "s1"); !j.NextRunAt.Equal(want) || (max == 0 && !j.NextRunAt.Equal(base)) {
			t.Errorf("max=%v：下一次 = %v，預期 %v", max, j.NextRunAt, want)
		}
	}
}
