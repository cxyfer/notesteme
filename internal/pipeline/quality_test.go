package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// spanCues 依 [start, end)（秒）建立 cue，文字各不相同。
func spanCues(spans ...[2]float64) []subtitle.Cue {
	out := make([]subtitle.Cue, len(spans))
	for i, s := range spans {
		out[i] = subtitle.Cue{Index: i + 1, StartMS: int64(s[0] * 1000), EndMS: int64(s[1] * 1000), Text: fmt.Sprintf("line number %d", i+1)}
	}
	return out
}

func TestCoverage(t *testing.T) {
	cases := []struct {
		name  string
		cues  []subtitle.Cue
		dur   float64
		want  float64
		valid bool
	}{
		{"整段", spanCues([2]float64{0, 100}), 100, 1, true},
		{"一半", spanCues([2]float64{0, 50}), 100, 0.5, true},
		{"重疊只算一次", spanCues([2]float64{0, 40}, [2]float64{20, 60}, [2]float64{10, 30}), 100, 0.6, true},
		{"包含在前一段內", spanCues([2]float64{0, 60}, [2]float64{10, 20}, [2]float64{70, 80}), 100, 0.7, true},
		{"超出片尾的部分不算", spanCues([2]float64{90, 500}), 100, 0.1, true},
		{"負的開始時間裁到 0", spanCues([2]float64{-50, 10}), 100, 0.1, true},
		{"結束早於開始不算", spanCues([2]float64{30, 10}), 100, 0, true},
		{"順序打亂", spanCues([2]float64{50, 60}, [2]float64{0, 10}), 100, 0.2, true},
		{"片長不知道", spanCues([2]float64{0, 10}), 0, 0, false},
		{"片長是負數", spanCues([2]float64{0, 10}), -5, 0, false},
	}
	for _, c := range cases {
		got, ok := coverage(c.cues, c.dur)
		if ok != c.valid || (ok && (got < c.want-1e-9 || got > c.want+1e-9)) {
			t.Errorf("%s：coverage = %v, %v，預期 %v, %v", c.name, got, ok, c.want, c.valid)
		}
	}
}

func TestMusicOnly(t *testing.T) {
	for text, want := range map[string]bool{
		"[Music]": true, "[music]": true, "(Music)": true, "［音樂］": true, "【音乐】": true, "（音楽）": true,
		"♪": true, "♪♪♪": true, "🎵": true, "[ Music ]": true, "♪ [Music] ♪": true, "[BGM]": true, "[Music] [Music]": true,
		"♪ la la la ♪": false, "♪ 歌詞 ♪": false, "[Music] hello": false, "[Applause]": false, "...": false, "": false,
		"[Laughter]": false, "Music is great": false, "hello": false,
	} {
		if got := musicOnly(text); got != want {
			t.Errorf("musicOnly(%q) = %v，預期 %v", text, got, want)
		}
	}
}

func TestRepetitionRatio(t *testing.T) {
	// 5 句中 1 句重複（20%）。
	if r := repetitionRatio(textCues("a", "b", "c", "d", "a")); r != 0.2 {
		t.Errorf("= %v，預期 0.2", r)
	}
	// 大小寫與空白不同仍算相同；數字不同不算相同。
	if r := repetitionRatio(textCues("Line 1", "line  1", "Line 2", "Line 3", "Line 4")); r != 0.2 {
		t.Errorf("= %v，預期 0.2", r)
	}
	// 少於 5 句不判斷。
	if r := repetitionRatio(textCues("a", "a", "a", "a")); r != 0 {
		t.Errorf("= %v，預期 0（太短）", r)
	}
	// 音樂標記不算句子，也不算重複。
	if r := repetitionRatio(textCues("[Music]", "[Music]", "[Music]", "a", "b", "c", "d", "e")); r != 0 {
		t.Errorf("= %v，預期 0", r)
	}
}

func TestQualityReasonThresholdsAndOrder(t *testing.T) {
	distinct := func(n int, step float64) []subtitle.Cue {
		spans := make([][2]float64, n)
		for i := range spans {
			spans[i] = [2]float64{float64(i) * step, float64(i+1) * step}
		}
		return spanCues(spans...)
	}
	withTexts := func(cues []subtitle.Cue, texts map[int]string) []subtitle.Cue {
		for i, t := range texts {
			cues[i].Text = t
		}
		return cues
	}
	cases := []struct {
		name string
		cues []subtitle.Cue
		dur  float64
		want string
	}{
		{"通過", distinct(10, 10), 100, ""},
		{"空的結果不在這裡判斷", nil, 100, ""},
		{"涵蓋剛好 50% 通過", distinct(5, 10), 100, ""},
		{"涵蓋 49% 不通過", distinct(7, 7), 100, reasonLowCoverage},
		{"片長不知道時不檢查涵蓋率", distinct(2, 1), 0, ""},
		// 10 句中 3 句重複＝30%，剛好通過；4 句＝40% 不通過。
		{"重複剛好 30% 通過", withTexts(distinct(10, 10), map[int]string{1: "x", 2: "x", 3: "x", 4: "x"}), 100, ""},
		{"重複 40% 不通過", withTexts(distinct(10, 10), map[int]string{1: "x", 2: "x", 3: "x", 4: "x", 5: "x"}), 100, reasonRepetition},
		// 10 句中 8 句只有音樂標記＝80%，剛好通過；9 句不通過。
		{"音樂剛好 80% 通過", withTexts(distinct(10, 10), map[int]string{0: "[Music]", 1: "♪", 2: "[Music]", 3: "[Music]", 4: "[Music]", 5: "[Music]", 6: "[Music]", 7: "[Music]"}), 100, ""},
		{"音樂 90% 不通過，不記成重複", withTexts(distinct(10, 10), map[int]string{0: "[Music]", 1: "[Music]", 2: "[Music]", 3: "[Music]", 4: "[Music]", 5: "[Music]", 6: "[Music]", 7: "[Music]", 8: "[Music]"}), 100, reasonMusicOnly},
		{"音樂優先於涵蓋率", withTexts(distinct(1, 1), map[int]string{0: "[Music]"}), 100, reasonMusicOnly},
		{"涵蓋率優先於重複", withTexts(distinct(10, 1), map[int]string{1: "x", 2: "x", 3: "x", 4: "x", 5: "x", 6: "x"}), 100, reasonLowCoverage},
	}
	for _, c := range cases {
		if got := qualityReason(c.cues, c.dur); got != c.want {
			t.Errorf("%s：qualityReason = %q，預期 %q", c.name, got, c.want)
		}
	}
}

// staticFetcher 是回傳固定結果的 fallback 來源。
func staticFetcher(got fetchResult) fallbackFetcher {
	return fallbackFetcher{
		timing: "segment",
		params: func(store.Item, Language) any { return nil },
		fetch: func(context.Context, store.Item, Language) (*fetchResult, error) {
			out := got
			return &out, nil
		},
	}
}

// 沒有通過品質檢查的來源記為 rejected 並附原因，改用下一個來源（SRC-R4、R17）。
func TestFallbackRejectsLowQualityAndMovesOn(t *testing.T) {
	repeat := textCues("a", "x", "x", "x", "x", "b")
	music := textCues("[Music]", "[Music]", "♪", "[Music]", "[Music]", "hi")
	for _, c := range []struct {
		reason string
		got    fetchResult
	}{
		{reasonLowCoverage, fetchResult{Lang: "en", Cues: spanCues([2]float64{0, 10}), DurationS: 100}},
		{reasonRepetition, fetchResult{Lang: "en", Cues: repeat, DurationS: 6}},
		{reasonMusicOnly, fetchResult{Lang: "en", Cues: music, DurationS: 6}},
	} {
		t.Run(c.reason, func(t *testing.T) {
			r, _ := newSupadataRig(t)
			r.p.fetchers["bad-fake"] = staticFetcher(c.got)
			r.p.fetchers["good-fake"] = staticFetcher(fetchResult{Lang: "en", Cues: spanCues([2]float64{0, 5}), DurationS: 5})
			it := r.ingest(watch, fallbackOverride("bad-fake", "good-fake"), StageTranscript)
			r.run()
			if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
				t.Fatalf("transcript = %+v", st)
			}
			d, _ := r.p.Item(context.Background(), it.ID)
			var got []string
			for _, s := range d.Sources[len(d.Sources)-2:] {
				got = append(got, s.SourceID+":"+s.Lang+":"+s.State+":"+s.Reason)
			}
			if want := "bad-fake:en:rejected:" + c.reason + " good-fake:en:used:"; strings.Join(got, " ") != want {
				t.Errorf("sources = %v，預期 %s", got, want)
			}
		})
	}
}

// ASR 來源沒有回報音訊長度時不檢查涵蓋率，不會拿影音的片長來比（SRC-R4）。
func TestQualityDurationForASR(t *testing.T) {
	d := 600
	it := store.Item{DurationS: &d}
	if got := qualityDuration("groq", it, &fetchResult{}); got != 0 {
		t.Errorf("groq 沒有回報長度 = %v，預期 0", got)
	}
	if got := qualityDuration("groq", it, &fetchResult{DurationS: 6.5}); got != 6.5 {
		t.Errorf("groq = %v，預期 6.5", got)
	}
	if got := qualityDuration("supadata", it, &fetchResult{}); got != 600 {
		t.Errorf("supadata = %v，預期片長 600", got)
	}
	if got := qualityDuration("supadata", store.Item{}, &fetchResult{}); got != 0 {
		t.Errorf("片長不知道 = %v，預期 0", got)
	}
}
