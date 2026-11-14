package pipeline

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// 只接受人工字幕：AI 字幕軌不符合 caption_priority，會走 fallback，但原文語言仍然從 AI 軌推斷。
func biliInferredOverrides(fallback ...any) map[string]any {
	return map[string]any{"language": map[string]any{
		"caption_priority": []any{map[string]any{"lang": "original", "kind": "manual"}},
		"fallback":         fallback,
	}}
}

// Bilibili 的原文語言是從 AI 字幕軌推斷的（transcript 步驟內）：同一次執行的 fallback 也要用推斷的值，
// 不能還拿著影音尚未寫入的舊值（空字串）而略過語言檢查（SRC-R4）。
func TestFallbackUsesInferredOriginalLanguage(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.Tracks = []string{"ai-zh"}
	fake := &fakeSupadata{lang: "en", cues: 2} // 原文是 zh（AI 軌），來源回報 en
	r.p.fetchers["asr-fake"] = fake.asFetcher("segment")
	it := r.ingest(biliP1, biliInferredOverrides("asr-fake"), StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed {
		t.Fatalf("transcript = %+v，預期語言不符被拒絕而失敗", st)
	}
	if want := "asr-fake:rejected:language_mismatch"; !strings.Contains(sourcesOf(r, it), want) {
		t.Errorf("sources = %s，預期含 %s", sourcesOf(r, it), want)
	}
}

// 原文語言相符時照常採用。
func TestFallbackUsesInferredOriginalLanguageAccepted(t *testing.T) {
	r, fb := newBiliRig(t)
	fb.info.Tracks = []string{"ai-zh"}
	fake := &fakeSupadata{lang: "zh", cues: 2}
	r.p.fetchers["asr-fake"] = fake.asFetcher("segment")
	it := r.ingest(biliP1, biliInferredOverrides("asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
}
