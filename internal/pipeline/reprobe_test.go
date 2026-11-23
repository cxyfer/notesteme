package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/transcript"
)

// reprobeRig 是只有自動字幕的 YouTube 影音（現行來源不是第一條規則），處理到 transcript。
func reprobeRig(t *testing.T, overrides map[string]any) (*rig, store.Item) {
	t.Helper()
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
	it := r.ingest(watch, overrides, StageTranscript)
	r.run()
	return r, it
}

func detailOf(t *testing.T, r *rig, id string) Detail {
	t.Helper()
	d, err := r.p.Item(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func addManual(r *rig) {
	r.yt.mu.Lock()
	r.yt.info.Tracks = append(r.yt.info.Tracks, youtube.Track{Lang: "en", Kind: "manual"})
	r.yt.mu.Unlock()
}

func reprobeAt(r *rig, id string, opts ReprobeOptions) ReprobeResult {
	r.t.Helper()
	res, err := r.p.Reprobe(context.Background(), id, r.clk.Now(), opts)
	if err != nil {
		r.t.Fatal(err)
	}
	return res
}

func TestReprobeSchedulesAfterTranscriptAndFindsBetter(t *testing.T) {
	r, it := reprobeRig(t, nil)
	start := r.clk.Now()
	d := detailOf(t, r, it.ID)
	if d.Reprobe == nil || !d.Reprobe.Sources || d.Reprobe.SponsorBlock || d.Reprobe.Runs != 0 ||
		!d.Reprobe.NextAt.Equal(start.Add(ReprobeInterval)) || !d.Reprobe.Until.Equal(start.Add(reprobeWindow)) || d.BetterSource != nil {
		t.Fatalf("reprobe = %+v better = %+v", d.Reprobe, d.BetterSource)
	}

	// 第一次：還沒有人工字幕，下一次在 12 小時後，次數加一。
	r.clk.Advance(ReprobeInterval)
	probesBefore := r.yt.probes
	res := reprobeAt(r, it.ID, ReprobeOptions{})
	if res.Done || res.Deferred || !res.Next.Equal(r.clk.Now().Add(ReprobeInterval)) || r.yt.probes != probesBefore+1 {
		t.Fatalf("res = %+v probes=%d", res, r.yt.probes)
	}

	// 人工字幕出現：只通知，不下載、不改字幕。
	addManual(r)
	fetched := len(r.yt.fetched)
	r.clk.Advance(ReprobeInterval)
	if res := reprobeAt(r, it.ID, ReprobeOptions{}); !res.Done {
		t.Fatalf("找到更好的來源後結束：%+v", res)
	}
	if len(r.yt.fetched) != fetched {
		t.Errorf("重新 Probe 不下載字幕：%v", r.yt.fetched)
	}
	d = detailOf(t, r, it.ID)
	b := d.BetterSource
	if b == nil || b.Source != transcript.SourceYouTubeManual || b.Lang != "en" || b.Kind != "manual" ||
		!b.FoundAt.Equal(r.clk.Now()) || b.EstimateUSD != nil {
		t.Fatalf("better = %+v", b)
	}
	tr := latestStage(t, r, it.ID, StageTranscript)
	want := "發現更好的字幕來源：YouTube 人工字幕（en）。重跑會再花一次 LLM 費用，目前無法估算金額。"
	if tr.Status != store.StatusDone || tr.Message != want || len(tr.Actions) != 1 || tr.Actions[0] != actionRerunBetter {
		t.Fatalf("transcript = %+v", tr)
	}
	// sources 仍然只列現行字幕的 Probe（SRC-R19）。
	if u, ok := usedProbe(d.Sources); !ok || u.SourceID != transcript.SourceYouTubeAuto {
		t.Fatalf("sources = %+v", d.Sources)
	}
	rp, err := r.st.Read().Reprobe(context.Background(), it.ID)
	if err != nil || rp.Runs != 2 || rp.Sources {
		t.Fatalf("rp = %+v err=%v", rp, err)
	}

	// 手動重跑 transcript：採用更好的來源，better_source 清除，第一條規則不再排程。
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranscript, Until: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	if d := detailOf(t, r, it.ID); d.BetterSource != nil || d.Reprobe != nil {
		t.Fatalf("重跑後：%+v %+v", d.BetterSource, d.Reprobe)
	}
	r.run()
	d = detailOf(t, r, it.ID)
	if u, _ := usedProbe(d.Sources); u.SourceID != transcript.SourceYouTubeManual || d.Reprobe != nil || d.BetterSource != nil {
		t.Fatalf("重跑完成：sources=%+v reprobe=%+v", d.Sources, d.Reprobe)
	}
}

func TestReprobeRateLimitDefers(t *testing.T) {
	r, it := reprobeRig(t, nil)
	r.clk.Advance(ReprobeInterval)
	at := r.clk.Now().Add(time.Minute)
	probes := r.yt.probes
	res := reprobeAt(r, it.ID, ReprobeOptions{Take: func(p string) (bool, time.Time) {
		if p != "youtube" {
			t.Errorf("platform = %q", p)
		}
		return false, at
	}})
	if !res.Deferred || !res.Next.Equal(at) || r.yt.probes != probes {
		t.Fatalf("res = %+v probes=%d", res, r.yt.probes)
	}
}

func TestReprobeEndsAfterUntilTrashOrMissing(t *testing.T) {
	r, it := reprobeRig(t, nil)
	addManual(r)
	r.clk.Advance(reprobeWindow + time.Minute)
	if res := reprobeAt(r, it.ID, ReprobeOptions{}); !res.Done {
		t.Fatalf("超過期限：%+v", res)
	}
	if d := detailOf(t, r, it.ID); d.BetterSource != nil {
		t.Fatalf("超過期限不 Probe：%+v", d.BetterSource)
	}
	if res, err := r.p.Reprobe(context.Background(), "nope", r.clk.Now(), ReprobeOptions{}); err != nil || !res.Done {
		t.Fatalf("不存在：%+v %v", res, err)
	}

	r2, it2 := reprobeRig(t, nil)
	if _, err := r2.p.Trash(context.Background(), it2.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if res, err := r2.p.Reprobe(context.Background(), it2.ID, r2.clk.Now(), ReprobeOptions{}); err != nil || !res.Done {
		t.Fatalf("垃圾桶：%+v %v", res, err)
	}
}

func TestReprobeLastRunIsClampedToUntil(t *testing.T) {
	r, it := reprobeRig(t, nil)
	r.clk.Advance(reprobeWindow - time.Hour)
	res := reprobeAt(r, it.ID, ReprobeOptions{})
	if res.Done || !res.Next.Equal(detailOf(t, r, it.ID).Reprobe.Until) {
		t.Fatalf("最後一次不晚於期限：%+v", res)
	}
}

func TestReprobeNotScheduled(t *testing.T) {
	ctx := context.Background()
	// 已經是第一條規則。
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	if d := detailOf(t, r, it.ID); d.Reprobe != nil {
		t.Fatalf("第一條規則：%+v", d.Reprobe)
	}
	// better_source: off。
	r2, it2 := reprobeRig(t, map[string]any{"language": map[string]any{"better_source": "off"}})
	if d := detailOf(t, r2, it2.ID); d.Reprobe != nil {
		t.Fatalf("off：%+v", d.Reprobe)
	}
	// 發布超過 7 天。
	r3 := newRig(t)
	old := r3.clk.Now().Add(-8 * 24 * time.Hour)
	r3.yt.info.PublishedAt = &old
	r3.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
	it3 := r3.ingest(watch, nil, StageTranscript)
	r3.run()
	if d := detailOf(t, r3, it3.ID); d.Reprobe != nil {
		t.Fatalf("超過 7 天：%+v", d.Reprobe)
	}
	if _, err := r3.st.Read().ScheduledJob(ctx, store.ReprobeKind, it3.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不排程：%v", err)
	}
}

func TestReprobeOffAtRunTimeStopsSources(t *testing.T) {
	r, it := reprobeRig(t, nil)
	// 執行時讀 transcript 的設定快照；換成第一條規則已是現行來源的情況：把 probes 改成 manual 已被採用。
	ctx := context.Background()
	if err := r.st.Write().InsertProbes(ctx, it.ID, []store.NewProbe{{SourceID: transcript.SourceYouTubeManual, Lang: "en", Kind: "manual", State: transcript.StateUsed}}); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(ReprobeInterval)
	if res := reprobeAt(r, it.ID, ReprobeOptions{}); !res.Done {
		t.Fatalf("現行已是第一條：%+v", res)
	}
}

func TestReprobeAutoRerun(t *testing.T) {
	over := map[string]any{"language": map[string]any{"better_source": "rerun"}}
	r, it := reprobeRig(t, over)
	addManual(r)
	r.clk.Advance(ReprobeInterval)
	if res := reprobeAt(r, it.ID, ReprobeOptions{}); !res.Done {
		t.Fatalf("自動重跑：%+v", res)
	}
	tr := latestStage(t, r, it.ID, StageTranscript)
	tk, err := r.st.Read().TaskByStageRun(context.Background(), tr.ID)
	if err != nil || tk.Priority != queue.PriorityBackfill {
		t.Fatalf("重跑的任務：%+v %v", tk, err)
	}
	r.run()
	d := detailOf(t, r, it.ID)
	if u, _ := usedProbe(d.Sources); u.SourceID != transcript.SourceYouTubeManual {
		t.Fatalf("sources = %+v", d.Sources)
	}
	rp, _ := r.st.Read().Reprobe(context.Background(), it.ID)
	if rp.AutoReruns != 1 {
		t.Fatalf("auto reruns = %d", rp.AutoReruns)
	}
}

func TestReprobeAutoRerunSkipsEditedTranscript(t *testing.T) {
	over := map[string]any{"language": map[string]any{"better_source": "rerun"}}
	r, it := reprobeRig(t, over)
	seedVariant(t, r, it, subtitle.VariantEdited, "en", "My own line.")
	addManual(r)
	r.clk.Advance(ReprobeInterval)
	if res := reprobeAt(r, it.ID, ReprobeOptions{}); !res.Done {
		t.Fatalf("res = %+v", res)
	}
	if tr := latestStage(t, r, it.ID, StageTranscript); tr.Status != store.StatusDone || tr.Message == "" {
		t.Fatalf("只通知：%+v", tr)
	}
	rp, _ := r.st.Read().Reprobe(context.Background(), it.ID)
	if rp.AutoReruns != 0 || rp.Better == nil {
		t.Fatalf("rp = %+v", rp)
	}
}

func TestReprobeSponsorBlock(t *testing.T) {
	ctx := context.Background()
	fs := &fakeSponsor{}
	r := sponsorRig(t, fs)
	it := r.ingest(watch, nil, "")
	r.run()
	d := detailOf(t, r, it.ID)
	if d.Reprobe == nil || d.Reprobe.Sources || !d.Reprobe.SponsorBlock {
		t.Fatalf("reprobe = %+v", d.Reprobe)
	}
	versions := func() int {
		n, err := r.st.Read().ArtifactVersions(ctx, it.ID, KindSponsorSegments, KindSponsorSegments, "")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// 相同結果：不建立新版本、沒有訊息。
	r.clk.Advance(ReprobeInterval)
	reprobeAt(r, it.ID, ReprobeOptions{})
	if versions() != 1 || fs.calls != 2 || latestStage(t, r, it.ID, StageSponsorBlock).Message != "" {
		t.Fatalf("相同：versions=%d calls=%d", versions(), fs.calls)
	}
	// 查詢失敗：保留現行產出、不顯示訊息。
	fs.res = sponsorblock.Result{Outcome: sponsorblock.OutcomeError, Reason: "HTTP 503", Segments: []sponsorblock.Segment{}}
	r.clk.Advance(ReprobeInterval)
	reprobeAt(r, it.ID, ReprobeOptions{})
	if versions() != 1 || latestStage(t, r, it.ID, StageSponsorBlock).Message != "" {
		t.Fatalf("失敗：versions=%d", versions())
	}
	fs.mu.Lock()
	fs.err = errors.New("boom")
	fs.mu.Unlock()
	r.clk.Advance(ReprobeInterval)
	reprobeAt(r, it.ID, ReprobeOptions{})
	if versions() != 1 {
		t.Fatalf("錯誤：versions=%d", versions())
	}
	// 片段更新：新版本與提醒。
	fs.mu.Lock()
	fs.err = nil
	fs.res = sponsorblock.Result{Outcome: sponsorblock.OutcomeOK, Segments: []sponsorblock.Segment{{Category: "sponsor", StartMS: 1000, EndMS: 2000, UUID: "u"}}}
	fs.mu.Unlock()
	r.clk.Advance(ReprobeInterval)
	reprobeAt(r, it.ID, ReprobeOptions{})
	sb := latestStage(t, r, it.ID, StageSponsorBlock)
	if versions() != 2 || sb.Status != store.StatusDone || sb.Message != msgSponsorUpdated || len(sb.Actions) != 1 || sb.Actions[0] != actionRerunChapters {
		t.Fatalf("更新：versions=%d stage=%+v", versions(), sb)
	}
	segs, ok, err := r.p.SponsorSegments(ctx, it.ID)
	if err != nil || !ok || len(segs) != 1 {
		t.Fatalf("segs = %+v %v %v", segs, ok, err)
	}
	// 從 sponsorblock 重跑：結束 SponsorBlock 部分；完成後重新判斷。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSponsorBlock}); err != nil {
		t.Fatal(err)
	}
	if d := detailOf(t, r, it.ID); d.Reprobe != nil {
		t.Fatalf("重跑中：%+v", d.Reprobe)
	}
	r.run()
	if d := detailOf(t, r, it.ID); d.Reprobe == nil || !d.Reprobe.SponsorBlock {
		t.Fatalf("重跑完成：%+v", d.Reprobe)
	}
}

func TestReprobeSkipsWhileTranscriptReruns(t *testing.T) {
	r, it := reprobeRig(t, nil)
	addManual(r)
	// 重跑 transcript 但還沒執行：重新 Probe 這次略過（transcript 不是 done）。
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranscript, Until: StageTranscript}); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(ReprobeInterval)
	if res := reprobeAt(r, it.ID, ReprobeOptions{}); !res.Done {
		t.Fatalf("重跑已結束工作：%+v", res)
	}
	if d := detailOf(t, r, it.ID); d.BetterSource != nil {
		t.Fatalf("better = %+v", d.BetterSource)
	}
}

func TestEvalRankAndHelpers(t *testing.T) {
	lang := DefaultLanguage()
	cases := []struct {
		p    store.Probe
		orig string
		want int
	}{
		{store.Probe{SourceID: transcript.SourceYouTubeManual, Lang: "en", Kind: "manual"}, "en", 0},
		{store.Probe{SourceID: transcript.SourceYouTubeAuto, Lang: "en", Kind: "auto"}, "en", 1},
		{store.Probe{SourceID: transcript.SourceYouTubeManual, Lang: "zh-Hans", Kind: "manual"}, "en", 2},
		{store.Probe{SourceID: transcript.SourceYouTubeManual, Lang: "en", Kind: "manual"}, "", 3},
		{store.Probe{SourceID: "supadata"}, "en", 3},
		{store.Probe{SourceID: "groq", Lang: "en"}, "en", 4},
		{store.Probe{SourceID: "gemini"}, "en", 5},
	}
	for _, c := range cases {
		if got := evalRank("youtube", c.p, c.orig, lang); got != c.want {
			t.Errorf("evalRank(%+v, %q) = %d, want %d", c.p, c.orig, got, c.want)
		}
	}
	if got := betterMessage(transcript.SourceBilibiliCC, "zh", nil); got != "發現更好的字幕來源：Bilibili CC 字幕（zh）。重跑會再花一次 LLM 費用，目前無法估算金額。" {
		t.Errorf("message = %q", got)
	}
	v := 1.234
	if got := betterMessage("other", "en", &v); got != "發現更好的字幕來源：other（en）。重跑會再花一次 LLM 費用，預估 $1.23。" {
		t.Errorf("message = %q", got)
	}
	for _, l := range []string{"en", "zh-Hant-TW", "yue"} {
		if !reprobeLang.MatchString(l) {
			t.Errorf("%q 應合法", l)
		}
	}
	for _, l := range []string{"", "e", "en<script>", "en\nx", "en-", "a-b-c-d-e-f", "中文"} {
		if reprobeLang.MatchString(l) {
			t.Errorf("%q 應不合法", l)
		}
	}
	created := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	pub := created.Add(-2 * time.Hour)
	future := created.Add(365 * 24 * time.Hour)
	for _, c := range []struct {
		pub  *time.Time
		want time.Time
	}{
		{nil, created.Add(reprobeWindow)},
		{&pub, pub.Add(reprobeWindow)},
		{&future, created.Add(reprobeWindow)},
	} {
		if got := reprobeUntil(store.Item{CreatedAt: created, PublishedAt: c.pub}); !got.Equal(c.want) {
			t.Errorf("until(%v) = %v, want %v", c.pub, got, c.want)
		}
	}
	if !reprobePlatform("bilibili") || reprobePlatform("podcast") || reprobePlatform("local") {
		t.Error("reprobePlatform")
	}
}
