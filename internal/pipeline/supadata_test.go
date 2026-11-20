package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// fakeSupadata 是 Supadata 的替身：記錄呼叫，依 err 與 cues 回應。
type fakeSupadata struct {
	mu    sync.Mutex
	urls  []string
	modes []string
	err   error
	lang  string
	cues  int
}

func (f *fakeSupadata) Fetch(_ context.Context, videoURL, mode string) (*supadata.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, videoURL)
	f.modes = append(f.modes, mode)
	if f.err != nil {
		return nil, f.err
	}
	res := &supadata.Result{Lang: f.lang}
	for i := 0; i < f.cues; i++ {
		res.Cues = append(res.Cues, subtitle.Cue{Index: i + 1, StartMS: int64(i) * 2000, EndMS: int64(i+1) * 2000, Text: "line", Timing: subtitle.TimingSource})
	}
	return res, nil
}

// asFetcher 把替身包成 fallback 來源，測試用它模擬 Supadata 以外的來源。
func (f *fakeSupadata) asFetcher(timing string) fallbackFetcher {
	return fallbackFetcher{
		timing: timing,
		params: func(store.Item, Language) any { return nil },
		fetch: func(ctx context.Context, it store.Item, _ Language) (*fetchResult, error) {
			got, err := f.Fetch(ctx, it.URL, "")
			if err != nil {
				return nil, err
			}
			return &fetchResult{Lang: got.Lang, Cues: got.Cues}, nil
		},
	}
}

func (f *fakeSupadata) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.urls)
}

// newSupadataRig 建立有 Supadata 的 rig；影片沒有任何平台字幕軌。
func newSupadataRig(t *testing.T) (*rig, *fakeSupadata) {
	t.Helper()
	sd := &fakeSupadata{lang: "en", cues: 3}
	r := newRig(t, func(d *Deps) { d.Supadata = sd })
	return r, sd
}

func (r *rig) sourceVersions(item store.Item) int {
	r.t.Helper()
	n, err := r.st.Read().ArtifactVersions(context.Background(), item.ID, "transcript", "source", "en")
	if err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *rig) rerunTranscript(item store.Item, overrides map[string]any) error {
	r.t.Helper()
	_, err := r.p.Rerun(context.Background(), item.ID, RerunRequest{From: StageTranscript, Overrides: overrides, Until: StageTranscript})
	return err
}

func TestSupadataIsUsedWhenNoPlatformCaptionFits(t *testing.T) {
	r, sd := newSupadataRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	r.run()

	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	if sd.calls() != 1 || sd.urls[0] != watch || sd.modes[0] != supadata.ModeAuto {
		t.Errorf("呼叫 = %v %v", sd.urls, sd.modes)
	}
	tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 3 || tr.Lang != "en" {
		t.Fatalf("cues = %+v, %v", tr, err)
	}
	arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	var prov map[string]any
	if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	if prov["source"] != "supadata" || prov["timing"] != "chunk" {
		t.Errorf("provenance = %v", prov)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	var got []string
	for _, s := range d.Sources {
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	want := "youtube-manual:unavailable:no_track youtube-auto:unavailable:no_track youtube-manual:unavailable:no_track supadata:used:"
	if strings.Join(got, " ") != want {
		t.Errorf("sources = %v", got)
	}
}

func TestPlatformCaptionsDoNotCallSupadata(t *testing.T) {
	r, sd := newSupadataRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	r.ingest(watch, nil, StageTranscript)
	r.run()
	if sd.calls() != 0 {
		t.Errorf("有平台字幕時不該呼叫 Supadata：%d", sd.calls())
	}
}

func TestSupadataFallbackOrderFollowsSettings(t *testing.T) {
	r, sd := newSupadataRig(t)
	it := r.ingest(watch, map[string]any{"language": map[string]any{"fallback": []any{"groq"}}}, StageTranscript)
	r.run()
	if sd.calls() != 0 {
		t.Errorf("fallback 沒有列出 supadata 時不該呼叫：%d", sd.calls())
	}
	// groq 沒有設定，沒有來源真的執行過：blocked。
	if st := r.stage(it, StageTranscript); st.Status != store.StatusBlocked {
		t.Errorf("transcript = %+v", st)
	}
}

func TestSupadataSameSettingsReuseTheCache(t *testing.T) {
	r, sd := newSupadataRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	if err := r.rerunTranscript(it, nil); err != nil {
		t.Fatal(err)
	}
	r.run()
	if sd.calls() != 1 {
		t.Errorf("相同設定重跑不該再呼叫 Supadata：%d", sd.calls())
	}
	if n := r.sourceVersions(it); n != 1 {
		t.Errorf("版本數 = %d", n)
	}
}

// 平台字幕軌選到了，但下載後是空的而改用 fallback 來源時，快取鍵也要含 fallback 來源的參數：
// 否則換 mode 重跑會命中舊結果（選到字幕軌時快取鍵一開始不含 fallback 參數）。
func TestFallbackAfterEmptyTrackKeysOnSourceParams(t *testing.T) {
	r, sd := newSupadataRig(t)
	r.yt.cues = 0
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	if sd.calls() != 1 {
		t.Fatalf("第一次應用 Supadata：%d", sd.calls())
	}
	for _, mode := range []string{"native", "auto"} {
		if err := r.rerunTranscript(it, map[string]any{"supadata": map[string]any{"mode": mode}}); err != nil {
			t.Fatal(err)
		}
		r.run()
	}
	// 換成 native 要重新取得；回到 auto 與第一次的快取鍵相同，重用（不再呼叫）。
	if got := strings.Join(sd.modes, ","); got != "auto,native" {
		t.Errorf("mode = %s", got)
	}
}

func TestSupadataModeIsPartOfTheCacheKey(t *testing.T) {
	r, sd := newSupadataRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	for _, mode := range []string{"native", "generate", "auto"} {
		if err := r.rerunTranscript(it, map[string]any{"supadata": map[string]any{"mode": mode}}); err != nil {
			t.Fatal(err)
		}
		r.run()
	}
	// 預設 mode 是 auto：最後一次與第一次的快取鍵相同，不再呼叫，也不建立新版本。
	if got := strings.Join(sd.modes, ","); got != "auto,native,generate" {
		t.Errorf("mode = %s", got)
	}
	if n := r.sourceVersions(it); n != 3 {
		t.Errorf("版本數 = %d，預期 3", n)
	}
}

// PIPE-R7：同一組 (item, kind, variant, lang) 只保留最近 3 版，更舊的記錄與檔案都清除。
func TestRerunKeepsOnlyTheLatestThreeVersionsAndDeletesOlderFiles(t *testing.T) {
	r, _ := newSupadataRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	// 每次的 fallback 不同，快取鍵就不同，每次都產生新版本。
	for _, fb := range [][]any{{"supadata", "a"}, {"supadata", "b"}, {"supadata", "c"}, {"supadata", "d"}} {
		if err := r.rerunTranscript(it, fallbackOverride(fb...)); err != nil {
			t.Fatal(err)
		}
		r.run()
	}

	if n := r.sourceVersions(it); n != 3 {
		t.Errorf("版本數 = %d，預期 3（5 次產生，只留最近 3 版）", n)
	}
	if n := artifactFiles(r, it.ID); n != 3 {
		t.Errorf("產出檔 = %d 個，預期 3（舊版本的檔案要一併刪除）", n)
	}
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	if err != nil || len(arts) != 1 {
		t.Fatalf("現行版本 = %+v, %v", arts, err)
	}
	// 留下的每個檔案都對應一筆記錄。
	if _, err := os.Stat(filepath.Join(r.dir, filepath.FromSlash(arts[0].Path))); err != nil {
		t.Errorf("現行版本的檔案不見了：%v", err)
	}
	// 清除後仍能讀取字幕，也能再重跑。
	if tr, err := r.p.Cues(context.Background(), it.ID, CuesQuery{Variant: "source", ToMS: -1}); err != nil || len(tr.Cues) != 3 {
		t.Errorf("cues = %+v, %v", tr, err)
	}
}

func TestSupadataModeMustBeKnown(t *testing.T) {
	r, _ := newSupadataRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	err := r.rerunTranscript(it, map[string]any{"supadata": map[string]any{"mode": "fast"}})
	var inv *InvalidError
	if !errors.As(err, &inv) || !strings.Contains(inv.Msg, "supadata.mode") {
		t.Errorf("err = %v", err)
	}
	err = r.rerunTranscript(it, map[string]any{"supadata": map[string]any{"mood": "auto"}})
	if !errors.As(err, &inv) {
		t.Errorf("不認識的欄位也要拒絕：%v", err)
	}
}

func TestSupadataRetryableFailureBacksOff(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{Class: errclass.ClassRetryable, Source: "supadata", Detail: "HTTP 503"}
	it := r.ingest(watch, map[string]any{"language": map[string]any{"fallback": []any{"supadata"}}}, StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	wantAt := r.clk.Now().Add(30 * time.Second)
	if st.Status != store.StatusPending || st.NotBefore == nil || !st.NotBefore.Equal(wantAt) || st.ErrorClass != "retryable" {
		t.Fatalf("transcript = %+v", st)
	}
	r.clk.Advance(30 * time.Second)
	r.run()
	r.clk.Advance(2 * time.Minute)
	r.run()
	st = r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed || st.ErrorClass != "retryable" || sd.calls() != 3 {
		t.Errorf("三次嘗試後 transcript = %+v，呼叫 %d 次", st, sd.calls())
	}
}

func TestSupadataWithoutResultMovesOn(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = supadata.ErrNoTranscript
	it := r.ingest(watch, nil, StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.Message != "找不到可用的字幕來源。" {
		t.Fatalf("transcript = %+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	var got []string
	for _, s := range d.Sources {
		got = append(got, s.SourceID+":"+s.State+":"+s.Reason)
	}
	tail := strings.Join(got[len(got)-2:], " ")
	if tail != "supadata:unavailable:no_transcript groq:unavailable:not_configured" {
		t.Errorf("sources = %v", got)
	}
}

func TestSupadataRejectedKeyBlocksWithFixAction(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{Class: errclass.ClassNeedsAction, Source: "supadata", Detail: "HTTP 401"}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || len(st.Actions) != 1 || st.Actions[0] != "設定 API key" {
		t.Errorf("transcript = %+v", st)
	}
}

// 402：方案不支援，訊息要說明原因與出路，不能說成 key 被拒絕。
func TestSupadataUnsupportedPlanBlocksWithPlanMessage(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{Class: errclass.ClassNeedsAction, Source: "supadata", Detail: "HTTP 402", Err: supadata.ErrPlanUnsupported}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" {
		t.Fatalf("transcript = %+v", st)
	}
	if !strings.Contains(st.Message, "方案不支援") || !strings.Contains(st.Message, "native") || strings.Contains(st.Message, "拒絕了 API key") {
		t.Errorf("訊息 = %q", st.Message)
	}
}

// 某個來源 permanent（請求有問題、非同步 job）時，記下原因後繼續下一個來源（SRC-R3）。
func TestFallbackPermanentErrorMovesOnToTheNextSource(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{Class: errclass.ClassPermanent, Source: "supadata", Detail: "HTTP 400"}
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
	if strings.Join(got, " ") != "supadata:unavailable:permanent asr-fake:used:" {
		t.Errorf("sources = %v", got)
	}
}

// 所有來源都用完仍沒有結果（含 permanent）：照 SRC-R5。
func TestFallbackAllSourcesExhaustedFailsWithNoSourceMessage(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{Class: errclass.ClassPermanent, Source: "supadata", Detail: "回應為非同步任務（202）"}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.Message != "找不到可用的字幕來源。" {
		t.Fatalf("transcript = %+v", st)
	}
	if sd.calls() != 1 {
		t.Errorf("permanent 不該重試：呼叫 %d 次", sd.calls())
	}
}

func TestWithoutSupadataItIsNotConfigured(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	d, _ := r.p.Item(context.Background(), it.ID)
	for _, s := range d.Sources {
		if s.SourceID == "supadata" && (s.State != "unavailable" || s.Reason != "not_configured") {
			t.Errorf("supadata = %+v", s)
		}
	}
}
