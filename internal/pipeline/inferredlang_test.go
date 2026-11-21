package pipeline

import (
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/transcript"
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

// metadata 完成、建立 transcript 任務時，Bilibili 也要用 AI 字幕軌推斷的原文語言判斷資源類別（PIPE 待決問題）：
// 預設的 caption_priority 要求 original 語言，沒有推斷就找不到符合的軌而誤歸為 asr。
func TestBilibiliMetadataUsesInferredLanguageForResourceClass(t *testing.T) {
	for _, tt := range []struct {
		name   string
		tracks []string
		want   string
	}{
		{"AI 字幕軌符合推斷出的原文語言", []string{"ai-zh"}, "download"},
		{"沒有字幕軌", nil, "asr"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, fb := newBiliRig(t)
			fb.info.Tracks = tt.tracks
			it := r.ingest(biliP1, fallbackOverride("groq"), StageTranscript)
			r.run()
			if got := r.transcriptClass(it); got != tt.want {
				t.Errorf("資源類別 = %s，預期 %s", got, tt.want)
			}
		})
	}
}

func TestInferOriginalLang(t *testing.T) {
	ai := []transcript.Track{{Lang: "zh", Kind: transcript.KindAuto}}
	for _, tt := range []struct {
		name, platform, current string
		tracks                  []transcript.Track
		want                    string
		inferred                bool
	}{
		{"Bilibili 沒有原文語言，用 AI 軌", source.PlatformBilibili, "", ai, "zh", true},
		{"Bilibili 已有原文語言", source.PlatformBilibili, "ja", ai, "ja", false},
		{"Bilibili 沒有 AI 軌", source.PlatformBilibili, "", []transcript.Track{{Lang: "zh", Kind: transcript.KindManual}}, "", false},
		{"YouTube 不推斷", source.PlatformYouTube, "", ai, "", false},
		{"本機：用選中的 sidecar 語言", source.PlatformLocal, "", []transcript.Track{{Lang: "ja", Kind: transcript.KindSidecar, Handle: "/d/a.ja.srt"}}, "ja", true},
		{"本機：內嵌字幕沒有語言標記不推斷", source.PlatformLocal, "", []transcript.Track{{Kind: transcript.KindEmbedded, Handle: "2"}}, "", false},
		{"本機：已有原文語言", source.PlatformLocal, "en", []transcript.Track{{Lang: "ja", Kind: transcript.KindSidecar, Handle: "/d/a.ja.srt"}}, "en", false},
		{"本機：沒有字幕軌", source.PlatformLocal, "", nil, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, inferred := inferOriginalLang(tt.platform, tt.current, tt.tracks, DefaultLanguage())
			if got != tt.want || inferred != tt.inferred {
				t.Errorf("inferOriginalLang = %q, %v，預期 %q, %v", got, inferred, tt.want, tt.inferred)
			}
		})
	}
}
