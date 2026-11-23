package subscription

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/scheduler"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

func (r *ytRig) check(t *testing.T, id string) (scheduler.Result, error) {
	t.Helper()
	return r.svc.runCheck(t.Context(), scheduler.Job{Kind: JobKind, Key: id}, r.clk.Now())
}

// 頻道：第一次檢查以 @handle 換成頻道 ID、讀 RSS，回補時再列 /videos；新內容以 subscription 優先級建立，
// 舊內容以 backfill 優先級建立；之後的檢查只讀 RSS，記錄過的條目不再評估。
func TestCheckChannelHandleAndBackfill(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	now := r.clk.Now()
	old := []ytVideo{
		{ID: "old0000001x", Title: "Old 1", Published: now.Add(-time.Hour)},
		{ID: "old0000002x", Title: "Old 2", Published: now.Add(-2 * time.Hour)},
		{ID: "old0000003x", Title: "Old 3", Published: now.Add(-3 * time.Hour)},
	}
	r.feed.channels[testChannel] = old[:2]
	r.tool.set("https://www.youtube.com/@fixture", flatJSON(t, "Fixture Channel", testChannel))
	r.tool.set(ChannelVideosURL(testChannel), flatJSON(t, "Fixture", testChannel, old[0], old[1], old[2]))
	sub, err := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/@fixture", Backfill: &BackfillInput{Mode: BackfillRecent, Count: intp(2)}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.check(t, sub.ID)
	if err != nil || res.Done || res.Next.IsZero() {
		t.Fatalf("第一次檢查：%+v %v", res, err)
	}
	got, _ := r.svc.Get(ctx, sub.ID)
	if got.ExternalID != testChannel || got.Title != "Fixture & Co" || got.LastCheckedAt == nil {
		t.Errorf("訂閱：%+v", got)
	}
	es := r.entries(t, sub.ID)
	if es["old0000001x"] != "created/" || es["old0000002x"] != "created/" || es["old0000003x"] != "skipped/backfill_limit" {
		t.Errorf("條目：%v", es)
	}
	for _, q := range r.ing.reqs {
		if q.Priority != queue.PriorityBackfill || q.SubscriptionID != sub.ID {
			t.Errorf("回補的優先級：%+v", q)
		}
	}
	// 新影片只出現在 RSS：subscription 優先級；第二次檢查不再列 /videos。
	r.clk.Advance(6 * time.Hour)
	r.feed.channels[testChannel] = append([]ytVideo{{ID: "new0000001x", Title: "New", Published: r.clk.Now().Add(-time.Minute)}}, old[:2]...)
	calls := len(r.tool.calls)
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.tool.calls) != calls {
		t.Errorf("第二次檢查不該再呼叫 yt-dlp：%d → %d", calls, len(r.tool.calls))
	}
	last := r.ing.reqs[len(r.ing.reqs)-1]
	if last.Priority != queue.PrioritySubscription || last.URL != "https://www.youtube.com/watch?v=new0000001x" {
		t.Errorf("新內容：%+v", last)
	}
	subs, err := r.svc.List(ctx)
	if err != nil || len(subs) != 1 {
		t.Fatal(subs, err)
	}
}

// @handle 換成頻道 ID 之後已經有同一個頻道的訂閱：停用，permanent 的「已經訂閱了這個來源。」。
func TestCheckHandleDuplicateDisables(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	r.feed.channels[testChannel] = nil
	r.tool.set("https://www.youtube.com/@fixture", flatJSON(t, "Fixture Channel", testChannel))
	if _, err := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/channel/" + testChannel}); err != nil {
		t.Fatal(err)
	}
	h, err := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/@fixture"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.check(t, h.ID)
	if err != nil || !res.Done {
		t.Fatalf("重複：%+v %v", res, err)
	}
	got, _ := r.svc.Get(ctx, h.ID)
	if got.Enabled || got.LastErrorClass == nil || *got.LastErrorClass != string(errclass.ClassPermanent) || *got.LastError != msgDuplicate {
		t.Errorf("要停用：%+v", got)
	}
	// 停用的訂閱：檢查直接結束；CheckNow 回 422 的欄位錯誤。
	if res, err := r.check(t, h.ID); err != nil || !res.Done {
		t.Errorf("停用：%+v %v", res, err)
	}
	var fe *FieldsError
	if _, err := r.svc.CheckNow(ctx, h.ID); !errors.As(err, &fe) || fe.Error() == "" {
		t.Errorf("停用的 CheckNow：%v", err)
	}
	if res, err := r.check(t, "gone"); err != nil || !res.Done {
		t.Errorf("不存在：%+v %v", res, err)
	}
	// handle 解析出不合法的頻道 ID：permanent。
	r.tool.set("https://www.youtube.com/@weird", flatJSON(t, "W", "not-a-channel"))
	w, _ := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/@weird"})
	if _, err := r.check(t, w.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = r.svc.Get(ctx, w.ID)
	if got.LastError == nil || *got.LastError != msgListGone {
		t.Errorf("不合法的頻道 ID：%+v", got)
	}
}

// CheckNow 把下一次檢查改成現在；Register 登記 subscription.check。
func TestCheckNowAndRegister(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	sub, err := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/channel/" + testChannel, Schedule: strp("2h")})
	if err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Hour)
	at, err := r.svc.CheckNow(ctx, sub.ID)
	if err != nil || !at.Equal(r.clk.Now()) {
		t.Fatal(at, err)
	}
	j, _ := r.st.Read().ScheduledJob(ctx, JobKind, sub.ID)
	if !j.NextRunAt.Equal(r.clk.Now()) {
		t.Errorf("next_run_at = %v", j.NextRunAt)
	}
	if _, err := r.svc.CheckNow(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在：%v", err)
	}
	sc := scheduler.New(r.st, r.clk, nil, 0)
	if err := r.svc.Register(sc); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Register(sc); err == nil {
		t.Error("重複登記要失敗")
	}
}

// Bilibili 合集：以舊格式網址與 BilibiliCollectionList 列出，條目是 BV 號的第 1 個分P；
// 過濾需要片長而列表沒有時以 probe 補齊，查不到的 deferred（下一次再試），之後不再查詢。
func TestCheckCollectionProbes(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	target := LegacyCollectionURL("123", "456")
	r.tool.set(target, flatJSON(t, "合集", "", ytVideo{ID: "BV1aaaaaaaaa"}, ytVideo{ID: "BV1bbbbbbbbb"}, ytVideo{ID: "not-bv"}))
	r.probe.info["BV1aaaaaaaaa"] = Details{DurationS: intp(30)}
	sub, err := r.svc.Create(ctx, CreateInput{URL: "https://space.bilibili.com/123/lists/456?type=season",
		Backfill: &BackfillInput{Mode: BackfillAll}, Filters: &FiltersInput{MinDurationS: intp(60)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.tool.calls[len(r.tool.calls)-1].Args; got[2] != extractorsBilibili {
		t.Errorf("extractor：%q", got)
	}
	es := r.entries(t, sub.ID)
	if es["BV1aaaaaaaaa"] != "skipped/too_short" || es["BV1bbbbbbbbb"] != "deferred/details_unavailable" || len(es) != 2 {
		t.Fatalf("條目：%v", es)
	}
	// 下一次檢查：deferred 的再試一次，查到了就建立；已略過的不再查詢。
	r.probe.info["BV1bbbbbbbbb"] = Details{DurationS: intp(600)}
	calls := r.probe.calls
	r.clk.Advance(7 * time.Hour)
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	es = r.entries(t, sub.ID)
	if es["BV1bbbbbbbbb"] != "created/" || r.probe.calls != calls+1 {
		t.Fatalf("第二次：%v（probe %d → %d）", es, calls, r.probe.calls)
	}
	if last := r.ing.reqs[len(r.ing.reqs)-1]; last.URL != "https://www.bilibili.com/video/BV1bbbbbbbbb" {
		t.Errorf("新增的網址：%s", last.URL)
	}
}

// live: skip：直播中、即將開始、直播剛結束與回放都略過；列表沒有直播狀態時補齊。
// 播放清單的發布時間不可靠：第一次檢查是基準清單（舊內容），之後才出現的是新內容。
func TestCheckPlaylistLiveSkip(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	target := "https://www.youtube.com/playlist?list=PLlive"
	r.tool.set(target, flatJSON(t, "Live", "",
		ytVideo{ID: "live0000001", Live: "is_live"}, ytVideo{ID: "vod00000001", Live: "not_live"}, ytVideo{ID: "unk00000001"}))
	r.probe.info["unk00000001"] = Details{LiveStatus: "was_live"}
	sub, err := r.svc.Create(ctx, CreateInput{URL: target, Backfill: &BackfillInput{Mode: BackfillAll}, Filters: &FiltersInput{Live: strp(LiveSkip)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	es := r.entries(t, sub.ID)
	if es["live0000001"] != "skipped/live" || es["unk00000001"] != "skipped/live" || es["vod00000001"] != "created/" {
		t.Fatalf("條目：%v", es)
	}
	if r.ing.reqs[0].Priority != queue.PriorityBackfill {
		t.Errorf("基準清單是回補：%+v", r.ing.reqs[0])
	}
	r.clk.Advance(7 * time.Hour)
	r.tool.set(target, flatJSON(t, "Live", "", ytVideo{ID: "vod00000001", Live: "not_live"}, ytVideo{ID: "new00000001", Live: "not_live"}))
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	if last := r.ing.reqs[len(r.ing.reqs)-1]; last.Priority != queue.PrioritySubscription || last.URL != "https://www.youtube.com/watch?v=new00000001" {
		t.Errorf("新內容：%+v", last)
	}
}

// 每次檢查最多 backfill_per_check 部回補，其餘 deferred；不在這次清單中的 deferred 條目一樣在之後依序建立。
func TestCheckDeferredOutsideList(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{BackfillPerCheck: 1})
	ctx := t.Context()
	target := "https://www.youtube.com/playlist?list=PLdef"
	r.tool.set(target, flatJSON(t, "P", "", ytVideo{ID: "a0000000001"}, ytVideo{ID: "b0000000002"}))
	sub, err := r.svc.Create(ctx, CreateInput{URL: target, Backfill: &BackfillInput{Mode: BackfillAll}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	es := r.entries(t, sub.ID)
	if es["b0000000002"] != "created/" || es["a0000000001"] != "deferred/backfill_per_check" {
		t.Fatalf("第一次：%v", es)
	}
	// 清單變空：先前 deferred 的仍然建立（由記錄的條目重建）。
	r.tool.set(target, flatJSON(t, "P", ""))
	r.clk.Advance(7 * time.Hour)
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	if es := r.entries(t, sub.ID); es["a0000000001"] != "created/" {
		t.Fatalf("第二次：%v", es)
	}
	if last := r.ing.reqs[len(r.ing.reqs)-1]; last.URL != "https://www.youtube.com/watch?v=a0000000001" {
		t.Errorf("重建的網址：%s", last.URL)
	}
}

// 頻道 RSS 的失敗：404 是 permanent、不退避；連線失敗與 5xx 是 retryable（回錯誤讓排程器退避）；
// yt-dlp 的 403 是 needs_action；檢查期間取消時回 context 的錯誤。
func TestCheckChannelFailures(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	sub, _ := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/channel/" + testChannel})
	res, err := r.check(t, sub.ID)
	if err != nil || res.Next.IsZero() {
		t.Fatalf("404：%+v %v", res, err)
	}
	got, _ := r.svc.Get(ctx, sub.ID)
	if got.LastErrorClass == nil || *got.LastErrorClass != string(errclass.ClassPermanent) {
		t.Errorf("404 要是 permanent：%+v", got)
	}
	r.feed.status = 503
	r.clk.Advance(time.Hour)
	if _, err := r.check(t, sub.ID); err == nil {
		t.Error("503 要回錯誤（退避）")
	}
	got, _ = r.svc.Get(ctx, sub.ID)
	if *got.LastErrorClass != string(errclass.ClassRetryable) {
		t.Errorf("503：%+v", got)
	}
	r.feed.status = 0
	pl := "https://www.youtube.com/playlist?list=PL403"
	r.tool.set(pl, toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] PL403: HTTP Error 403: Forbidden")})
	p, _ := r.svc.Create(ctx, CreateInput{URL: pl})
	if _, err := r.check(t, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = r.svc.Get(ctx, p.ID)
	if *got.LastErrorClass != string(errclass.ClassNeedsAction) || *got.LastError != msgForbidden {
		t.Errorf("403：%+v", got)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	r.tool.failRun = errors.New("killed")
	if _, err := r.svc.runCheck(cctx, scheduler.Job{Kind: JobKind, Key: p.ID}, r.clk.Now()); !errors.Is(err, context.Canceled) {
		t.Errorf("取消：%v", err)
	}
}
