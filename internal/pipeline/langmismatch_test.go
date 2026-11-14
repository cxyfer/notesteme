package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

func TestLangMismatch(t *testing.T) {
	for _, tt := range []struct {
		want, got string
		mismatch  bool
	}{
		{"en", "ja", true},
		{"en", "en", false},
		{"en", "EN", false},
		{"zh", "zh-Hans", false},
		{"zh-Hant", "zh", false},
		{"en", "en-orig", false},
		{"pt_BR", "pt", false},
		{"", "ja", false},    // 原文語言未知：略過檢查
		{"und", "ja", false}, // und 也是未知
		{"en", "", false},    // 來源沒有回報語言：沒有可比較的
		{"en", "und", false},
		// 同一語言的別名（舊代碼與新代碼）視為相同。
		{"no", "nb", false},
		{"nb", "no", false},
		{"nn", "no", false},
		{"tl", "fil", false},
		{"he", "iw", false},
		{"id", "in", false},
		{"jv", "jw", false},
		{"nb-NO", "no", false},
		{"no", "sv", true},
	} {
		if got := langMismatch(tt.want, tt.got); got != tt.mismatch {
			t.Errorf("langMismatch(%q, %q) = %v", tt.want, tt.got, got)
		}
	}
}

// 來源回報的語言和影音原文語言不同時，記為 rejected（language_mismatch），繼續下一個來源（SRC-R4、SRC-R17）。
func TestFallbackRejectsLanguageMismatchAndMovesOn(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.lang = "ja" // 影音的原文語言是 en
	next := &fakeSupadata{lang: "en", cues: 2}
	r.p.fetchers["asr-fake"] = next.asFetcher("segment")
	it := r.ingest(watch, fallbackOverride("supadata", "asr-fake"), StageTranscript)
	r.run()

	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	var got []string
	for _, s := range d.Sources[len(d.Sources)-2:] {
		got = append(got, s.SourceID+":"+s.Lang+":"+s.State+":"+s.Reason)
	}
	if strings.Join(got, " ") != "supadata:ja:rejected:language_mismatch asr-fake:en:used:" {
		t.Errorf("sources = %v", got)
	}
}

// 所有來源都被拒絕時，照 SRC-R5 失敗。
func TestFallbackAllRejectedFailsWithNoSourceMessage(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.lang = "ja"
	it := r.ingest(watch, fallbackOverride("supadata"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.Message != "找不到可用的字幕來源。" {
		t.Errorf("transcript = %+v", st)
	}
}

// 原文語言未知時略過檢查；來源沒有回報語言也不算不符。
func TestFallbackSkipsLanguageCheckWhenUnknown(t *testing.T) {
	r, sd := newSupadataRig(t)
	r.yt.info.Language = ""
	sd.lang = "ja"
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Errorf("原文語言未知：transcript = %+v", st)
	}

	r, sd = newSupadataRig(t)
	sd.lang = ""
	it = r.ingest(watch, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Errorf("來源沒有回報語言：transcript = %+v", st)
	}
}
