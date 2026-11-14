package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

// rig 的時鐘是 2026-11-12T10:00:00Z；剛發布 30 分鐘、自動字幕還沒出來的影片。
func newFreshRig(t *testing.T) (*rig, *fakeSupadata) {
	t.Helper()
	r := newRig(t)
	pub := time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	r.yt.info.PublishedAt = &pub
	asr := &fakeSupadata{lang: "en", cues: 2}
	r.p.fetchers["asr-fake"] = asr.asFetcher("segment")
	return r, asr
}

func (r *rig) sourcesLine(item store.Item) string {
	r.t.Helper()
	d, err := r.p.Item(context.Background(), item.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	var got []string
	for _, s := range d.Sources {
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	return strings.Join(got, " ")
}

// SRC-R6：自動字幕還沒生成就等待，不呼叫付費來源、不消耗重試次數。
func TestWaitsForAutoCaptionsOfAFreshVideo(t *testing.T) {
	r, asr := newFreshRig(t)
	ctx := context.Background()
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	wantAt := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	if st.Status != store.StatusWaiting || st.ErrorClass != "wait" || st.NotBefore == nil || !st.NotBefore.Equal(wantAt) ||
		st.Message != "YouTube 還沒產生自動字幕，預計 2 小時後再檢查。" || !slices.Equal(st.Actions, []string{"現在改用語音轉錄"}) {
		t.Fatalf("transcript = %+v", st)
	}
	if asr.calls() != 0 {
		t.Errorf("等待期間不該呼叫 fallback 來源：%d", asr.calls())
	}
	tk, _ := r.st.Read().TaskByStageRun(ctx, st.ID)
	if tk.State != store.TaskPending || tk.Attempts != 0 {
		t.Errorf("wait 不消耗重試次數：%+v", tk)
	}
	if got, want := r.sourcesLine(it), "youtube-manual:unavailable:no_track youtube-auto:unavailable:not_ready youtube-manual:unavailable:no_track"; got != want {
		t.Errorf("sources = %q，預期 %q", got, want)
	}

	// 2 小時後仍然沒有：繼續等，不截到 24 小時的期限，仍不計入次數。
	r.clk.Advance(2 * time.Hour)
	r.run()
	st = r.stage(it, StageTranscript)
	if st.Status != store.StatusWaiting || st.NotBefore == nil || !st.NotBefore.Equal(wantAt.Add(2*time.Hour)) {
		t.Fatalf("再次檢查：%+v", st)
	}
	if tk, _ = r.st.Read().TaskByStageRun(ctx, st.ID); tk.Attempts != 0 {
		t.Errorf("attempts = %d", tk.Attempts)
	}

	// 自動字幕生成了：直接使用，不用付費來源。
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "auto"}}
	r.clk.Advance(2 * time.Hour)
	r.run()
	if st = r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 0 {
		t.Errorf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}
}

// 超過發布後 24 小時仍沒有自動字幕：改用下一個來源。
func TestStopsWaitingAfter24Hours(t *testing.T) {
	r, asr := newFreshRig(t)
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusWaiting {
		t.Fatalf("transcript = %+v", st)
	}
	r.clk.Advance(25 * time.Hour)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 1 {
		t.Errorf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}
	// 最近一次 Probe 不再是 not_ready。
	if got, want := r.sourcesLine(it), "youtube-manual:unavailable:no_track youtube-auto:unavailable:no_track youtube-manual:unavailable:no_track asr-fake:used:"; got != want {
		t.Errorf("sources = %q，預期 %q", got, want)
	}
}

// 不等待的情況：任何一個條件不成立就照一般的 fallback。
func TestDoesNotWaitWhenAConditionFails(t *testing.T) {
	manualOnly := fallbackOverride("asr-fake")
	manualOnly["language"].(map[string]any)["caption_priority"] = []any{map[string]any{"lang": "original", "kind": "manual"}}

	for _, tt := range []struct {
		name      string
		overrides map[string]any
		setup     func(r *rig)
	}{
		{"caption_priority 沒有 auto 的規則", manualOnly, nil},
		{"有平台翻譯軌", nil, func(r *rig) {
			r.yt.info.Tracks = []youtube.Track{{Lang: "ja", Kind: "platform-translated"}}
		}},
		{"沒有 published_at", nil, func(r *rig) { r.yt.info.PublishedAt = nil }},
		{"已超過 24 小時", nil, func(r *rig) {
			pub := r.clk.Now().Add(-24 * time.Hour)
			r.yt.info.PublishedAt = &pub
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, asr := newFreshRig(t)
			if tt.setup != nil {
				tt.setup(r)
			}
			ov := tt.overrides
			if ov == nil {
				ov = fallbackOverride("asr-fake")
			}
			it := r.ingest(watch, ov, StageTranscript)
			r.run()
			if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 1 {
				t.Errorf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
			}
		})
	}
}

// 只有日期精度（upload_date）時，實際發布時間可能是當天的任何時刻：期限是該日 00:00 UTC + 48 小時，
// 不是 + 24 小時（否則晚發布的影片等不滿 24 小時）。
func TestDateOnlyPublishedAtWaitsUntilTheEndOfThatDayPlus24Hours(t *testing.T) {
	r, asr := newFreshRig(t) // 時鐘 2026-11-12 10:00 UTC
	pub := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	r.yt.info.PublishedAt, r.yt.info.PublishedAtApprox = &pub, true
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusWaiting {
		t.Fatalf("日期精度、當天 00:00 起算 34 小時：應該還在等待：%+v", st)
	}
	// 超過 00:00 + 48 小時（2026-11-13 00:00）後改用下一個來源。
	r.clk.Advance(14 * time.Hour)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 1 {
		t.Errorf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}

	// 精確時間不受影響：同樣的時間點，沒有標示近似就是 24 小時。
	r, asr = newFreshRig(t)
	r.yt.info.PublishedAt = &pub
	it = r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 1 {
		t.Errorf("精確時間：transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}
}

// 剛好還差不到 2 小時就到 24 小時期限：仍然等 2 小時（不截到期限），下一次檢查時才改用 fallback。
func TestWaitIsNotTruncatedToTheDeadline(t *testing.T) {
	r, asr := newFreshRig(t)
	pub := r.clk.Now().Add(-23 * time.Hour)
	r.yt.info.PublishedAt = &pub
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusWaiting || st.NotBefore == nil || !st.NotBefore.Equal(r.clk.Now().Add(2*time.Hour)) {
		t.Fatalf("transcript = %+v", st)
	}
	r.clk.Advance(2 * time.Hour)
	r.run()
	if st = r.stage(it, StageTranscript); st.Status != store.StatusDone || asr.calls() != 1 {
		t.Errorf("transcript = %+v，fallback 呼叫 %d 次", st, asr.calls())
	}
}
