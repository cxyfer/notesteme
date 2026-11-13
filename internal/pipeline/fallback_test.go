package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/supadata"

	"github.com/cxyfer/notesteme/internal/store"
)

// 沒有設定的來源依序略過（not_configured）；結果取決於有沒有來源真的執行（SRC-R10 的折衷）。

func fallbackOverride(ids ...any) map[string]any {
	return map[string]any{"language": map[string]any{"fallback": ids}}
}

func TestFallbackWithoutKeysBlocksForAPIKey(t *testing.T) {
	// 只有 supadata，且沒有 API key（Deps.Supadata 為 nil）。
	r := newRig(t)
	it := r.ingest(watch, fallbackOverride("supadata"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.ErrorClass != "needs_action" ||
		len(st.Actions) != 1 || st.Actions[0] != "設定 API key" {
		t.Errorf("只有 supadata 且沒有 key：transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	last := d.Sources[len(d.Sources)-1]
	if last.SourceID != "supadata" || last.Reason != "not_configured" {
		t.Errorf("sources = %+v", d.Sources)
	}

	// 全部都沒有 key（預設的 supadata、groq）。
	r = newRig(t)
	it = r.ingest(watch, nil, StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusBlocked || len(st.Actions) != 1 || st.Actions[0] != "設定 API key" {
		t.Errorf("全部沒有 key：transcript = %+v", st)
	}
}

func TestFallbackSkipsUnconfiguredSourceAndUsesTheNext(t *testing.T) {
	r := newRig(t) // supadata 沒有 key
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
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	if strings.Join(got, " ") != "supadata:unavailable:not_configured asr-fake:used:" {
		t.Errorf("sources = %v", got)
	}
}

// 有來源真的執行並回報沒有結果時，仍然照 SRC-R5 permanent 失敗，即使後面的來源沒有設定。
func TestFallbackExecutedWithoutResultFailsPermanently(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = supadata.ErrNoTranscript
	it := r.ingest(watch, fallbackOverride("supadata", "groq"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.ErrorClass != "permanent" {
		t.Errorf("transcript = %+v", st)
	}
}

// fallback 是空的：沒有任何來源可以等你設定，照 SRC-R5。
func TestEmptyFallbackFailsPermanently(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, fallbackOverride(), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.ErrorClass != "permanent" {
		t.Errorf("transcript = %+v", st)
	}
}
