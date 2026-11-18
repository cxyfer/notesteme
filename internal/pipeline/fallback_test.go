package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
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

// fakeSource 是只回傳固定錯誤的 fallback 來源，用來驗證 runFallback 與來源無關。
func fakeSource(err error) fallbackFetcher {
	return fallbackFetcher{
		timing: "segment",
		params: func(store.Item, Language) any { return nil },
		fetch:  func(context.Context, store.Item, Language) (*fetchResult, error) { return nil, err },
	}
}

// needs_action 的訊息與動作由來源的錯誤決定，不綁定特定來源；沒帶訊息時用通用訊息、不帶動作。
func TestFallbackNeedsActionUsesTheSourceMessageAndActions(t *testing.T) {
	r := newRig(t)
	r.p.fetchers["asr-fake"] = fakeSource(&errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "asr-fake", Message: "asr-fake 要你處理。", Actions: []string{"動作甲", "動作乙"},
	})
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.Message != "asr-fake 要你處理。" ||
		!slices.Equal(st.Actions, []string{"動作甲", "動作乙"}) {
		t.Errorf("transcript = %+v", st)
	}

	r = newRig(t)
	r.p.fetchers["asr-fake"] = fakeSource(&errclass.Error{Class: errclass.ClassNeedsAction, Source: "asr-fake"})
	it = r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusBlocked || st.Message != msgSourceNeedsAction || len(st.Actions) != 0 {
		t.Errorf("沒有訊息：transcript = %+v", st)
	}
}

// 來源自己回報沒有設定（errclass.ErrNotConfigured）：記為 not_configured 並略過；全部如此時 blocked。
func TestFallbackSourceReportingNotConfiguredIsSkipped(t *testing.T) {
	r := newRig(t)
	r.p.fetchers["asr-fake"] = fakeSource(&errclass.Error{Class: errclass.ClassNeedsAction, Source: "asr-fake", Err: errclass.ErrNotConfigured})
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || !slices.Equal(st.Actions, []string{"設定 API key"}) {
		t.Errorf("transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	if last := d.Sources[len(d.Sources)-1]; last.SourceID != "asr-fake" || last.State != "unavailable" || last.Reason != "not_configured" {
		t.Errorf("sources = %+v", d.Sources)
	}
}

// 來源回報沒有結果（errclass.ErrNoResult）：繼續下一個來源，最後照 SRC-R5。
func TestFallbackSourceReportingNoResultMovesOn(t *testing.T) {
	r := newRig(t)
	r.p.fetchers["asr-fake"] = fakeSource(errclass.ErrNoResult)
	it := r.ingest(watch, fallbackOverride("asr-fake"), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusFailed || st.ErrorClass != "permanent" {
		t.Errorf("transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	if last := d.Sources[len(d.Sources)-1]; last.Reason != "no_transcript" {
		t.Errorf("sources = %+v", d.Sources)
	}
}
