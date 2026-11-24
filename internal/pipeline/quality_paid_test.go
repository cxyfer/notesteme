package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
)

// 已付費的結果沒通過 low_coverage、repetition，而且沒有其他來源可用時，採用它（SRC-R4 的例外，收尾 D5）。
func TestPaidRejectedResultIsAdoptedWhenNoSourceIsLeft(t *testing.T) {
	repeat := textCues("a", "x", "x", "x", "x", "b")
	for _, c := range []struct {
		reason string
		got    fetchResult
	}{
		{reasonLowCoverage, fetchResult{Lang: "en", Cues: spanCues([2]float64{0, 10}), DurationS: 100}},
		{reasonRepetition, fetchResult{Lang: "en", Cues: repeat, DurationS: 6}},
	} {
		t.Run(c.reason, func(t *testing.T) {
			r, _ := newSupadataRig(t)
			r.p.fetchers["groq"] = staticFetcher(c.got)
			it := r.ingest(watch, fallbackOverride("groq"), StageTranscript)
			r.run()

			st := r.stage(it, StageTranscript)
			if st.Status != store.StatusDone {
				t.Fatalf("transcript = %+v，預期採用已付費的結果", st)
			}
			if !strings.Contains(st.Message, "已付費") || !strings.Contains(st.Message, "沒有通過品質檢查") {
				t.Errorf("訊息 = %q，預期說明沒通過檢查但因為已付費而採用", st.Message)
			}
			d, _ := r.p.Item(context.Background(), it.ID)
			// 只留一列 probe：rejected 換成 used，保留原因。
			var rows []string
			for _, s := range d.Sources {
				if s.SourceID == "groq" {
					rows = append(rows, s.State+":"+s.Reason)
				}
			}
			if want := "used:" + c.reason; len(rows) != 1 || rows[0] != want {
				t.Errorf("groq 的 probe = %v，預期只有 %s", rows, want)
			}
		})
	}
}

// 不採用的情況：music_only、免費來源、後面有來源通過、後面要使用者處理（blocked）。
func TestPaidRejectedResultIsNotAdopted(t *testing.T) {
	music := textCues("[Music]", "[Music]", "♪", "[Music]", "[Music]", "hi")
	lowCoverage := fetchResult{Lang: "en", Cues: spanCues([2]float64{0, 10}), DurationS: 100}

	t.Run("music_only 照舊失敗", func(t *testing.T) {
		r, _ := newSupadataRig(t)
		r.p.fetchers["groq"] = staticFetcher(fetchResult{Lang: "en", Cues: music, DurationS: 6})
		it := r.ingest(watch, fallbackOverride("groq"), StageTranscript)
		r.run()
		if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.Message != msgNoSource {
			t.Fatalf("transcript = %+v", st)
		}
	})

	t.Run("免費來源被拒照舊失敗", func(t *testing.T) {
		r, _ := newSupadataRig(t)
		r.p.fetchers["free-fake"] = staticFetcher(lowCoverage)
		it := r.ingest(watch, fallbackOverride("free-fake"), StageTranscript)
		r.run()
		if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.Message != msgNoSource {
			t.Fatalf("transcript = %+v", st)
		}
	})

	t.Run("語言不符的付費結果照舊失敗", func(t *testing.T) {
		r, sd := newSupadataRig(t)
		sd.lang = "ja" // 影音的原文語言是 en
		it := r.ingest(watch, fallbackOverride("supadata"), StageTranscript)
		r.run()
		if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.Message != msgNoSource {
			t.Fatalf("transcript = %+v", st)
		}
	})

	t.Run("後面有來源通過就用後面的", func(t *testing.T) {
		r, _ := newSupadataRig(t)
		r.p.fetchers["groq"] = staticFetcher(lowCoverage)
		r.p.fetchers["good-fake"] = staticFetcher(fetchResult{Lang: "en", Cues: spanCues([2]float64{0, 5}), DurationS: 5})
		it := r.ingest(watch, fallbackOverride("groq", "good-fake"), StageTranscript)
		r.run()
		if st := r.stage(it, StageTranscript); st.Status != store.StatusDone || strings.Contains(st.Message, "已付費") {
			t.Fatalf("transcript = %+v", st)
		}
		d, _ := r.p.Item(context.Background(), it.ID)
		var got []string
		for _, s := range d.Sources[len(d.Sources)-2:] {
			got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
		}
		if want := "groq:rejected:low_coverage good-fake:used:"; strings.Join(got, " ") != want {
			t.Errorf("sources = %v，預期 %s", got, want)
		}
	})

	t.Run("後面的來源要使用者處理時維持 blocked", func(t *testing.T) {
		r, _ := newSupadataRig(t)
		r.p.fetchers["groq"] = staticFetcher(lowCoverage)
		blocked := staticFetcher(fetchResult{})
		blocked.fetch = func(context.Context, store.Item, Language) (*fetchResult, error) {
			return nil, &errclass.Error{Class: errclass.ClassNeedsAction, Source: "blocked-fake", Detail: "HTTP 401"}
		}
		r.p.fetchers["blocked-fake"] = blocked
		it := r.ingest(watch, fallbackOverride("groq", "blocked-fake"), StageTranscript)
		r.run()
		if st := r.stage(it, StageTranscript); st.Status != store.StatusBlocked {
			t.Fatalf("transcript = %+v，預期 blocked（讓使用者補 key 可能拿到更好的結果）", st)
		}
	})
}

// 兩個付費來源都被拒時，採用 language.fallback 順序中的第一個。
func TestPaidRejectedFirstInOrderIsAdopted(t *testing.T) {
	r, _ := newSupadataRig(t)
	r.p.fetchers["supadata"] = staticFetcher(fetchResult{Lang: "en", Cues: spanCues([2]float64{0, 10}), DurationS: 100})
	r.p.fetchers["groq"] = staticFetcher(fetchResult{Lang: "en", Cues: textCues("a", "x", "x", "x", "x", "b"), DurationS: 6})
	it := r.ingest(watch, fallbackOverride("supadata", "groq"), StageTranscript)
	r.run()

	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	var got []string
	for _, s := range d.Sources {
		if s.SourceID == "supadata" || s.SourceID == "groq" {
			got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
		}
	}
	// 第一個（supadata）改為 used 並保留原因；第二個（groq）仍是 rejected。順序不固定，排序後比較。
	slices.Sort(got)
	if want := "groq:rejected:repetition supadata:used:low_coverage"; strings.Join(got, " ") != want {
		t.Errorf("sources = %v，預期 %s", got, want)
	}
}
