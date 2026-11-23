package subscription

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

func refs(rows []PreviewRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Ref)
	}
	return out
}

func skips(rows []PreviewSkip) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Ref+"/"+r.Reason)
	}
	return out
}

func eq(a, b []string) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

// 預覽視同「現在」建立的頻道：發布時間早於現在的都是舊內容，依回補範圍與過濾決定，原因的順序同檢查。
func TestPreviewNewChannelWithBackfill(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	now := r.clk.Now()
	vids := []ytVideo{
		{ID: "keep001xxxx", Title: "How B-trees work", Published: now.Add(-22 * time.Hour)},
		{ID: "short01xxxx", Title: "Quick tip", Published: now.Add(-23 * time.Hour), Shorts: true},
		{ID: "ads0001xxxx", Title: "抽獎 giveaway", Published: now.Add(-24 * time.Hour)},
		{ID: "old0001xxxx", Title: "Old talk", Published: now.Add(-48 * time.Hour)},
	}
	r.feed.channels[testChannel] = vids
	r.tool.set(ChannelVideosURL(testChannel), flatJSON(t, "Fixture", testChannel, vids[0], vids[2], vids[3]))
	p, err := r.svc.PreviewNew(t.Context(), "https://www.youtube.com/channel/"+testChannel, PreviewInput{
		Backfill: &BackfillInput{Mode: BackfillRecent, Count: intp(1)},
		Filters:  &FiltersInput{TitleExclude: strp("抽獎")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(refs(p.WouldProcess), []string{"yt:keep001xxxx"}) {
		t.Errorf("would_process = %v", refs(p.WouldProcess))
	}
	want := []string{"yt:short01xxxx/shorts", "yt:ads0001xxxx/title_excluded", "yt:old0001xxxx/backfill_limit"}
	if !eq(skips(p.WouldSkip), want) {
		t.Errorf("would_skip = %v，預期 %v", skips(p.WouldSkip), want)
	}
	if p.Truncated {
		t.Error("沒有超過上限，不應截斷")
	}
	// 沒有建立任何東西。
	if n, _ := r.svc.Count(t.Context()); n != 0 || len(r.ing.reqs) != 0 {
		t.Fatalf("預覽建立了訂閱或影音：%d %d", n, len(r.ing.reqs))
	}
	// none（預設）：列表中的都是 before_subscription。
	p, err = r.svc.PreviewNew(t.Context(), "https://www.youtube.com/channel/"+testChannel, PreviewInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.WouldProcess) != 0 || len(p.WouldSkip) != 4 || p.WouldSkip[0].Reason != ReasonShorts && p.WouldSkip[0].Reason != ReasonBeforeSubscription {
		t.Fatalf("none = %+v", p)
	}
	for _, s := range p.WouldSkip {
		if s.Ref != "yt:short01xxxx" && s.Reason != ReasonBeforeSubscription {
			t.Errorf("%s 的原因 %s，預期 before_subscription", s.Ref, s.Reason)
		}
	}
}

// 既有的訂閱：body 的設定覆蓋已存的設定；預覽不寫入條目、不建立影音、不改變排程與訂閱（含 @handle 不寫回）。
func TestPreviewExistingWritesNothing(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	now := r.clk.Now()
	vids := []ytVideo{
		{ID: "keep001xxxx", Title: "Keep", Published: now.Add(-1 * time.Hour), Duration: floatp(900)},
		{ID: "long001xxxx", Title: "Long", Published: now.Add(-2 * time.Hour), Duration: floatp(6000)},
	}
	r.feed.channels[testChannel] = vids
	r.tool.set("https://www.youtube.com/@fixture", flatJSON(t, "Fixture Channel", testChannel))
	r.tool.set(ChannelVideosURL(testChannel), flatJSON(t, "Fixture", testChannel, vids...))
	sub, err := r.svc.Create(ctx, CreateInput{URL: "https://www.youtube.com/@fixture", Backfill: &BackfillInput{Mode: BackfillAll},
		Filters: &FiltersInput{MaxDurationS: intp(5400)}})
	if err != nil {
		t.Fatal(err)
	}
	jobBefore, err := r.st.Read().ScheduledJob(ctx, JobKind, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Minute)
	p, err := r.svc.PreviewExisting(ctx, sub.ID, PreviewInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(refs(p.WouldProcess), []string{"yt:keep001xxxx"}) || !eq(skips(p.WouldSkip), []string{"yt:long001xxxx/too_long"}) {
		t.Fatalf("預覽 = %v %v", refs(p.WouldProcess), skips(p.WouldSkip))
	}
	if p.WouldProcess[0].DurationS == nil || *p.WouldProcess[0].DurationS != 900 || p.WouldProcess[0].Title != "Keep" {
		t.Errorf("列 = %+v", p.WouldProcess[0])
	}
	// 尚未儲存的設定覆蓋已存的。none 不列 /videos，RSS 沒有片長：過濾要片長時逐部補齊（同檢查）。
	r.probe.info["keep001xxxx"] = Details{DurationS: intp(900)}
	r.probe.info["long001xxxx"] = Details{DurationS: intp(6000)}
	p, err = r.svc.PreviewExisting(ctx, sub.ID, PreviewInput{Filters: &FiltersInput{MaxDurationS: intp(7200)}, Backfill: &BackfillInput{Mode: BackfillNone}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.WouldProcess) != 0 || !eq(skips(p.WouldSkip), []string{"yt:keep001xxxx/before_subscription", "yt:long001xxxx/before_subscription"}) {
		t.Fatalf("覆蓋後 = %v %v", refs(p.WouldProcess), skips(p.WouldSkip))
	}

	got, err := r.st.Read().Subscription(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalID != "@fixture" || got.Title != "" || got.BaselineAt != nil || got.LastCheckedAt != nil || got.Reevaluate {
		t.Errorf("預覽改變了訂閱：%+v", got)
	}
	if es := r.entries(t, sub.ID); len(es) != 0 {
		t.Errorf("預覽寫入了條目：%v", es)
	}
	if len(r.ing.reqs) != 0 {
		t.Errorf("預覽建立了影音：%d", len(r.ing.reqs))
	}
	jobAfter, err := r.st.Read().ScheduledJob(ctx, JobKind, sub.ID)
	if err != nil || !jobAfter.NextRunAt.Equal(jobBefore.NextRunAt) || jobAfter.Failures != jobBefore.Failures {
		t.Errorf("預覽改變了排程：%+v → %+v（%v）", jobBefore, jobAfter, err)
	}
	if _, err := r.svc.PreviewExisting(ctx, "nope", PreviewInput{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("不存在的訂閱：%v", err)
	}
}

func TestPreviewErrors(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{RateLimits: config.RateLimits{YouTube: 1}})
	ctx := t.Context()
	var fe *FieldsError
	if _, err := r.svc.PreviewNew(ctx, "", PreviewInput{}); !errors.As(err, &fe) || fe.Fields[0].Path != "url" {
		t.Errorf("缺網址：%v", err)
	}
	var ue *URLError
	if _, err := r.svc.PreviewNew(ctx, "https://www.youtube.com/watch?v=abc123xxxxx", PreviewInput{}); !errors.As(err, &ue) || ue.Code != CodeNotACollection {
		t.Errorf("單一影片：%v", err)
	}
	if _, err := r.svc.PreviewNew(ctx, "https://www.youtube.com/channel/"+testChannel, PreviewInput{Backfill: &BackfillInput{Mode: "some"}}); !errors.As(err, &fe) || fe.Fields[0].Path != "backfill.mode" {
		t.Errorf("回補模式：%v", err)
	}
	if _, err := r.svc.PreviewNew(ctx, "https://www.youtube.com/channel/"+testChannel, PreviewInput{Filters: &FiltersInput{TitleExclude: strp("(")}}); !errors.As(err, &fe) || fe.Fields[0].Path != "filters.title_exclude" {
		t.Errorf("regex：%v", err)
	}

	// 頻道不存在（RSS 404）：permanent，訊息已遮蔽。
	var pe *PreviewError
	if _, err := r.svc.PreviewNew(ctx, "https://www.youtube.com/channel/"+testChannel2, PreviewInput{}); !errors.As(err, &pe) || pe.Class != errclass.ClassPermanent {
		t.Fatalf("404：%v", err)
	}
	// 每分鐘 1 次的額度用完：429 rate_limited，retry_after 是下一顆額度。
	var rl *RateLimitedError
	_, err := r.svc.PreviewNew(ctx, "https://www.youtube.com/channel/"+testChannel2, PreviewInput{})
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 || rl.RetryAfter > time.Minute || rl.Error() != msgRateLimited {
		t.Fatalf("額度用完：%v", err)
	}

	// 平台回 429：bucket 清空並暫停 15 分鐘，預覽也回 rate_limited。
	r.clk.Advance(time.Hour)
	r.tool.set("https://www.youtube.com/playlist?list=PL1", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] PL1: HTTP Error 429: Too Many Requests")})
	_, err = r.svc.PreviewNew(ctx, "https://www.youtube.com/playlist?list=PL1", PreviewInput{})
	if !errors.As(err, &rl) || rl.RetryAfter != throttlePause {
		t.Fatalf("429：%v", err)
	}
	r.clk.Advance(time.Minute)
	if ok, _ := r.svc.rate.Take("youtube", 1); ok {
		t.Error("429 之後 bucket 應該暫停")
	}
	// 取消。
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	r.clk.Advance(time.Hour)
	r.tool.failRun = errors.New("killed")
	if _, err := r.svc.PreviewNew(cctx, "https://www.youtube.com/playlist?list=PL1", PreviewInput{}); !errors.Is(err, cctx.Err()) {
		t.Errorf("取消：%v", err)
	}
}

// 兩個清單都依發布時間由新到舊（沒有發布時間的在最後、保持探索順序），各最多 200 列，超過時 truncated。
func TestPreviewOrderAndTruncation(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	now := r.clk.Now()
	var vids []ytVideo
	for i := range 250 {
		v := ytVideo{ID: fmt.Sprintf("v%010d", i), Title: fmt.Sprint(i)}
		if i%2 == 0 {
			v.Published = now.Add(-time.Duration(i+1) * time.Minute)
		}
		vids = append(vids, v)
	}
	// 播放清單：flat-playlist 的順序是清單順序；mode all 的回補全部「會處理」（含 backfill_per_check 延後的）。
	r.tool.set("https://www.youtube.com/playlist?list=PL2", flatJSON(t, "PL", "", vids...))
	p, err := r.svc.PreviewNew(t.Context(), "https://www.youtube.com/playlist?list=PL2", PreviewInput{Backfill: &BackfillInput{Mode: BackfillAll}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.WouldProcess) != MaxPreviewRows || !p.Truncated || len(p.WouldSkip) != 0 {
		t.Fatalf("截斷：%d %d %v", len(p.WouldProcess), len(p.WouldSkip), p.Truncated)
	}
	// 有發布時間的先（新到舊），之後是沒有發布時間的（依清單順序反過來：最後加入的在前）。
	if p.WouldProcess[0].Ref != "yt:v0000000000" || p.WouldProcess[124].Ref != "yt:v0000000248" || p.WouldProcess[125].PublishedAt != nil {
		t.Errorf("順序：%v %v %v", p.WouldProcess[0].Ref, p.WouldProcess[124].Ref, p.WouldProcess[125])
	}
	// none：全部 before_subscription，略過的清單也截斷。
	p, err = r.svc.PreviewNew(t.Context(), "https://www.youtube.com/playlist?list=PL2", PreviewInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.WouldSkip) != MaxPreviewRows || !p.Truncated || len(p.WouldProcess) != 0 {
		t.Fatalf("none 截斷：%d %d %v", len(p.WouldProcess), len(p.WouldSkip), p.Truncated)
	}
}

// 補齊查不到時（details_unavailable）列在會略過；之後的檢查會再試，但預覽不寫入。
func TestPreviewDetailsUnavailable(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	now := r.clk.Now()
	vids := []ytVideo{{ID: "a0000000001", Title: "A", Published: now.Add(-time.Hour)}, {ID: "b0000000002", Title: "B", Published: now.Add(-2 * time.Hour)}}
	r.tool.set("https://www.youtube.com/playlist?list=PL3", flatJSON(t, "PL", "", vids...))
	r.probe.info["a0000000001"] = Details{DurationS: intp(100)}
	p, err := r.svc.PreviewNew(t.Context(), "https://www.youtube.com/playlist?list=PL3", PreviewInput{
		Backfill: &BackfillInput{Mode: BackfillAll}, Filters: &FiltersInput{MinDurationS: intp(60)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(refs(p.WouldProcess), []string{"yt:a0000000001"}) || !eq(skips(p.WouldSkip), []string{"yt:b0000000002/details_unavailable"}) {
		t.Fatalf("預覽 = %v %v", refs(p.WouldProcess), skips(p.WouldSkip))
	}
	if p.WouldProcess[0].DurationS == nil || *p.WouldProcess[0].DurationS != 100 {
		t.Errorf("補齊的片長：%+v", p.WouldProcess[0])
	}
}

// 已經檢查過的訂閱：預覽仍以目前的設定評估整份清單（視同現在建立），不因為條目記錄過就省略；
// 已建立的影音在媒體庫中，列為 already_in_library。
func TestPreviewIgnoresRecordedEntries(t *testing.T) {
	r := newYtRig(t, config.Subscriptions{})
	ctx := t.Context()
	target := "https://www.youtube.com/playlist?list=PLrec"
	r.tool.set(target, flatJSON(t, "P", "", ytVideo{ID: "a0000000001"}, ytVideo{ID: "b0000000002"}))
	sub, err := r.svc.Create(ctx, CreateInput{URL: target})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.check(t, sub.ID); err != nil {
		t.Fatal(err)
	}
	if es := r.entries(t, sub.ID); len(es) != 2 {
		t.Fatalf("條目：%v", es)
	}
	if _, _, err := r.st.Write().InsertItemIfAbsent(ctx, store.NewItem{Platform: PlatformYouTube, ExternalID: "a0000000001", URL: "https://www.youtube.com/watch?v=a0000000001"}); err != nil {
		t.Fatal(err)
	}
	p, err := r.svc.PreviewExisting(ctx, sub.ID, PreviewInput{Backfill: &BackfillInput{Mode: BackfillAll}})
	if err != nil {
		t.Fatal(err)
	}
	if !eq(refs(p.WouldProcess), []string{"yt:b0000000002"}) || !eq(skips(p.WouldSkip), []string{"yt:a0000000001/already_in_library"}) {
		t.Fatalf("預覽 = %v %v", refs(p.WouldProcess), skips(p.WouldSkip))
	}
}
