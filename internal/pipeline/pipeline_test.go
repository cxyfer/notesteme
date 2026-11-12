package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/worker"
)

// fakeYT 是 YouTube 的替身：回傳預先設定的資訊，並記錄呼叫。
type fakeYT struct {
	mu       sync.Mutex
	info     youtube.Info
	probeErr error
	probes   int
	fetched  []youtube.Track
	cues     int  // 下載的字幕有幾句；0 代表空檔
	tagKind  bool // 為 true 時字幕文字帶上字幕軌種類，讓不同軌的內容（sha256）不同
	onProbe  func()
}

func (f *fakeYT) Probe(context.Context, string) (*youtube.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probes++
	if f.onProbe != nil {
		f.onProbe()
	}
	if f.probeErr != nil {
		return nil, f.probeErr
	}
	i := f.info
	return &i, nil
}

func (f *fakeYT) FetchCaptions(_ context.Context, _ string, t youtube.Track, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, t)
	var sb strings.Builder
	sb.WriteString("WEBVTT\n\n")
	for i := 0; i < f.cues; i++ {
		tag := ""
		if f.tagKind {
			tag = " (" + t.Kind + ")"
		}
		fmt.Fprintf(&sb, "00:00:%02d.000 --> 00:00:%02d.000\nline %d%s\n\n", i*2, i*2+2, i+1, tag)
	}
	return []byte(sb.String()), nil
}

func (f *fakeYT) Version(context.Context) (string, error) { return "2026.11.01", nil }

type rig struct {
	t   *testing.T
	p   *Pipeline
	st  *store.Store
	clk *clock.Fake
	w   *worker.Worker
	yt  *fakeYT
	dir string
}

func newRig(t *testing.T, opts ...func(*Deps)) *rig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123", Title: "T", Language: "en"}}
	q := queue.New(st, clk, "w1")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test"}
	for _, o := range opts {
		o(&deps)
	}
	p := New(deps)
	return &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
}

func (r *rig) ingest(url string, overrides map[string]any, until string) store.Item {
	r.t.Helper()
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: url, Overrides: overrides, Until: until})
	if err != nil {
		r.t.Fatal(err)
	}
	return res[0].Item
}

func (r *rig) run() {
	r.t.Helper()
	if err := r.w.RunUntilIdle(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) stage(item store.Item, name string) store.StageRun {
	r.t.Helper()
	runs, err := r.st.Read().StageRunsByItem(context.Background(), item.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, s := range runs {
		if s.Stage == name {
			return s
		}
	}
	r.t.Fatalf("沒有步驟 %s", name)
	return store.StageRun{}
}

const watch = "https://www.youtube.com/watch?v=abc123"

func TestIngestCreatesItemStagesAndManualTask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	res, err := r.p.Ingest(ctx, IngestRequest{URL: "https://youtu.be/abc123"})
	if err != nil || len(res) != 1 || res[0].AlreadyInLibrary {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	it := res[0].Item
	if it.Platform != "youtube" || it.ExternalID != "abc123" || it.Part != 0 || it.URL != watch {
		t.Errorf("item = %+v", it)
	}
	for _, s := range Stages {
		st := r.stage(it, s)
		if st.Status != store.StatusPending || st.ConfigSnapshotID == "" {
			t.Errorf("步驟 %s = %+v", s, st)
		}
	}
	tk, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageMetadata).ID)
	if err != nil || tk.Kind != StageMetadata || tk.Priority != queue.PriorityManual || tk.State != store.TaskPending {
		t.Fatalf("metadata 任務 = %+v, %v", tk, err)
	}
	if _, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageTranscript).ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("下游步驟在上游完成前不建立任務：%v", err)
	}

	// 已存在的影音不重複建立（ING-R2）。
	again, err := r.p.Ingest(ctx, IngestRequest{URL: watch})
	if err != nil || !again[0].AlreadyInLibrary || again[0].Item.ID != it.ID {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if items, _ := r.p.Items(ctx); len(items) != 1 {
		t.Errorf("影音數 = %d", len(items))
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 1 {
		t.Errorf("pending 任務數 = %d，預期 1", n)
	}
}

func TestIngestRejections(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var inv *InvalidError
	var ni *NotImplementedError
	tests := []struct {
		name string
		req  IngestRequest
		is   func(error) bool
	}{
		{"頻道", IngestRequest{URL: "https://www.youtube.com/@somechannel"}, func(e error) bool { return errors.Is(e, ErrChannel) }},
		{"Bilibili 個人空間", IngestRequest{URL: "https://space.bilibili.com/12345"}, func(e error) bool { return errors.Is(e, ErrChannel) }},
		{"無法辨識", IngestRequest{URL: "https://example.com/some/page"}, func(e error) bool { return errors.Is(e, ErrUnrecognizedURL) }},
		{"不是網址", IngestRequest{URL: "abc"}, func(e error) bool { return errors.Is(e, ErrUnrecognizedURL) }},
		{"Bilibili 影片", IngestRequest{URL: "https://www.bilibili.com/video/BV1xx411c7mD"}, func(e error) bool { return errors.As(e, &ni) }},
		{"播放清單", IngestRequest{URL: "https://www.youtube.com/playlist?list=PL123"}, func(e error) bool { return errors.As(e, &ni) }},
		{"處理方案", IngestRequest{URL: watch, Profile: "standard"}, func(e error) bool { return errors.As(e, &ni) }},
		{"未知的 until", IngestRequest{URL: watch, Until: "publish"}, func(e error) bool { return errors.As(e, &inv) }},
		{"YouTube 不能指定分P", IngestRequest{URL: watch, Parts: []int{1}}, func(e error) bool { return errors.As(e, &inv) }},
		{"不合法的覆寫", IngestRequest{URL: watch, Overrides: map[string]any{"language": map[string]any{"targets": []any{}}}}, func(e error) bool { return errors.As(e, &inv) }},
		{"拼錯的覆寫欄位", IngestRequest{URL: watch, Overrides: map[string]any{"language": map[string]any{"fallbck": []any{"groq"}}}}, func(e error) bool { return errors.As(e, &inv) }},
	}
	for _, tt := range tests {
		if _, err := r.p.Ingest(ctx, tt.req); err == nil || !tt.is(err) {
			t.Errorf("%s：err = %v", tt.name, err)
		}
	}
	if items, _ := r.p.Items(ctx); len(items) != 0 {
		t.Errorf("被拒絕的請求不應建立影音：%d", len(items))
	}
	if ErrChannel.Error() != "這是頻道。要追蹤新內容，請建立訂閱。" ||
		ErrUnrecognizedURL.Error() != "無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。" {
		t.Error("錯誤訊息要與規格逐字相同（ING-R1、ING-R5）")
	}
}

func TestFullRunProducesSourceAndNormalizedTranscripts(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	dur := 612
	r.yt.info = youtube.Info{ID: "abc123", Title: "PostgreSQL in 100 Seconds", DurationS: &dur, Language: "en", Availability: "public",
		ChannelID: "UC1", Channel: "Fireship", Tracks: []youtube.Track{{Lang: "en", Kind: "manual"}, {Lang: "en", Kind: "auto"}}}
	it := r.ingest(watch, nil, "")
	r.run()

	got, _ := r.st.Read().ItemByID(ctx, it.ID)
	if got.Title != "PostgreSQL in 100 Seconds" || got.DurationS == nil || *got.DurationS != 612 ||
		got.OriginalLang != "en" || got.LangSource != "metadata" || got.Availability != "public" || got.ChannelID == "" {
		t.Errorf("metadata 之後的影音 = %+v", got)
	}
	for _, s := range Stages {
		if st := r.stage(it, s); st.Status != store.StatusDone || st.CacheKey == "" || st.ImplVersion != 1 {
			t.Errorf("步驟 %s = %+v", s, st)
		}
	}
	if len(r.yt.fetched) != 1 || r.yt.fetched[0] != (youtube.Track{Lang: "en", Kind: "manual"}) {
		t.Errorf("應只下載人工字幕：%+v", r.yt.fetched)
	}

	arts, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if len(arts) != 2 || arts[0].Variant != "source" || arts[1].Variant != "normalized" {
		t.Fatalf("產出 = %+v", arts)
	}
	src, norm := arts[0], arts[1]
	if src.Lang != "en" || src.Format != cuesFormat || !strings.HasPrefix(src.Path, "artifacts/"+it.ID+"/") || !strings.HasSuffix(src.Path, "/"+src.ID+".json") {
		t.Errorf("source 產出 = %+v", src)
	}
	// 檔案存在，內容的 sha256 與記錄相符。
	if _, _, _, err := fileSHA(filepath.Join(r.dir, src.Path)); err != nil {
		t.Fatal(err)
	}
	if sha, size, _, _ := fileSHA(filepath.Join(r.dir, src.Path)); sha != src.SHA256 || size != src.Size {
		t.Errorf("檔案 sha256 %s（%d）與記錄 %s（%d）不符", sha, size, src.SHA256, src.Size)
	}
	if r.stage(it, StageTranscript).ArtifactID != src.ID || r.stage(it, StageNormalize).ArtifactID != norm.ID {
		t.Error("步驟應指向各自的產出")
	}

	// 來源紀錄（SRC-R16、NRM-R8）。
	var sp map[string]any
	_ = json.Unmarshal(src.Provenance, &sp)
	tv := sp["tool_versions"].(map[string]any)
	if sp["source"] != "youtube-manual" || sp["timing"] != "cue" || tv["yt-dlp"] != "2026.11.01" || tv["notesteme"] != "test" ||
		sp["executor"] != "local" || sp["created_at"] != "2026-11-12T10:00:00Z" {
		t.Errorf("source provenance = %s", src.Provenance)
	}
	var np map[string]any
	_ = json.Unmarshal(norm.Provenance, &np)
	from := np["derived_from"].([]any)[0].(map[string]any)
	if np["impl_version"].(float64) != 1 || from["artifact"] != src.ID || from["sha256"] != src.SHA256 || len(norm.Parents) != 1 || norm.Parents[0] != src.ID {
		t.Errorf("normalized provenance = %s，parents = %v", norm.Provenance, norm.Parents)
	}

	// 每個 cue 的時間來源是 source。
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 4 {
		t.Fatalf("Cues = %+v, %v", tr, err)
	}
	for _, c := range tr.Cues {
		if c.Timing != "source" {
			t.Errorf("cue %d timing = %s", c.Index, c.Timing)
		}
	}

	// Probe 紀錄（SRC-R17）。
	d, err := r.p.Item(ctx, it.ID)
	if err != nil || len(d.Sources) != 1 || d.Sources[0].SourceID != "youtube-manual" || d.Sources[0].State != "used" || d.Sources[0].Lang != "en" {
		t.Errorf("sources = %+v, %v", d.Sources, err)
	}
}

func fileSHA(path string) (sha string, size int64, _ []byte, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, nil, err
	}
	return sha256hex(b), int64(len(b)), b, nil
}

func TestUntilStopsTheChain(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "transcript")
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	r.run()
	if r.stage(it, StageTranscript).Status != store.StatusDone {
		t.Error("transcript 應完成")
	}
	if n := r.stage(it, StageNormalize); n.Status != store.StatusPending {
		t.Errorf("until 之後的步驟維持 pending：%+v", n)
	}
	if _, err := r.st.Read().TaskByStageRun(context.Background(), r.stage(it, StageNormalize).ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("until 之後的步驟不建立任務：%v", err)
	}

	it2 := r.ingest("https://www.youtube.com/watch?v=meta0", nil, "metadata")
	r.run()
	if r.stage(it2, StageMetadata).Status != store.StatusDone || r.stage(it2, StageTranscript).Status != store.StatusPending {
		t.Error("until=metadata 只做 metadata")
	}
}

func TestCaptionPriorityFromGlobalSettingsAndOverrides(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Language = "ja"
	r.yt.info.Tracks = []youtube.Track{{Lang: "zh-Hans", Kind: "manual"}, {Lang: "ja", Kind: "auto"}}

	// 預設的優先順序：original manual → original auto。
	a := r.ingest(watch, nil, "transcript")
	r.run()
	if len(r.yt.fetched) != 1 || r.yt.fetched[0] != (youtube.Track{Lang: "ja", Kind: "auto"}) {
		t.Fatalf("預設應使用 ja 自動字幕：%+v", r.yt.fetched)
	}
	arts, _ := r.st.Read().CurrentArtifacts(ctx, a.ID, "transcript")
	if arts[0].Lang != "ja" {
		t.Errorf("lang = %s", arts[0].Lang)
	}

	// 全域設定改成只用簡中人工字幕。
	if _, err := r.p.SetSettings(ctx, "language", map[string]any{
		"caption_priority": []any{map[string]any{"lang": "zh-Hans", "kind": "manual"}},
	}); err != nil {
		t.Fatal(err)
	}
	b := r.ingest("https://www.youtube.com/watch?v=second", nil, "transcript")
	r.run()
	if last := r.yt.fetched[len(r.yt.fetched)-1]; last != (youtube.Track{Lang: "zh-Hans", Kind: "manual"}) {
		t.Fatalf("全域設定應生效：%+v", r.yt.fetched)
	}

	// 單次覆寫優先於全域設定，而且記錄在影音與快照中，不改全域設定（ING-R7）。
	c := r.ingest("https://www.youtube.com/watch?v=third", map[string]any{
		"language": map[string]any{"caption_priority": []any{map[string]any{"lang": "original", "kind": "auto"}}},
	}, "transcript")
	r.run()
	if last := r.yt.fetched[len(r.yt.fetched)-1]; last != (youtube.Track{Lang: "ja", Kind: "auto"}) {
		t.Fatalf("單次覆寫應生效：%+v", r.yt.fetched)
	}
	cs, _ := r.st.Read().ItemByID(ctx, c.ID)
	if !strings.Contains(string(cs.Overrides), "caption_priority") {
		t.Errorf("overrides_json = %s", cs.Overrides)
	}
	snap, err := r.st.Read().Snapshot(ctx, r.stage(c, StageTranscript).ConfigSnapshotID)
	if err != nil || !strings.Contains(snap, `"lang":"original"`) {
		t.Errorf("快照 = %s, %v", snap, err)
	}
	snapB, _ := r.st.Read().Snapshot(ctx, r.stage(b, StageTranscript).ConfigSnapshotID)
	if strings.Contains(snapB, `"lang":"original"`) {
		t.Errorf("別部影音的快照不應受影響：%s", snapB)
	}
	if g, _ := r.p.GetSettings(ctx, "language"); len(g["caption_priority"].([]any)) != 1 {
		t.Errorf("全域設定被單次覆寫改掉了：%v", g)
	}
}

func TestNoUsableSourceFailsWithFixAction(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "zh-Hans", Kind: "platform-translated"}}
	it := r.ingest(watch, nil, "transcript")
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.Message != "找不到可用的字幕來源。" ||
		len(st.Actions) != 1 || st.Actions[0] != "設定語音轉錄服務" {
		t.Errorf("transcript = %+v", st)
	}
	if st.FinishedAt == nil {
		t.Error("failed 的步驟要有結束時間")
	}
	d, _ := r.p.Item(ctx, it.ID)
	var names []string
	for _, s := range d.Sources {
		names = append(names, s.SourceID+":"+s.State+":"+s.Reason)
	}
	want := []string{
		"youtube-manual:unavailable:no_track", "youtube-auto:unavailable:no_track", "youtube-manual:unavailable:no_track",
		"youtube-auto:rejected:platform_translated", "supadata:unavailable:not_configured", "groq:unavailable:not_configured",
	}
	// 第三項（zh-Hans manual）與第一項來源相同但語言不同，所以不會被去重。
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("sources = %v\n預期 %v", names, want)
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskFailed); n != 1 {
		t.Errorf("失敗的任務數 = %d", n)
	}
	if tr := r.stage(it, StageNormalize); tr.Status != store.StatusPending {
		t.Errorf("上游失敗，normalize 維持 pending：%s", tr.Status)
	}
}

func TestLiveStreamWaitsWithoutCountingAttempts(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.LiveStatus = "is_live"
	it := r.ingest(watch, nil, "")
	r.run()

	st := r.stage(it, StageMetadata)
	wantAt := r.clk.Now().Add(liveRecheckDelay)
	if st.Status != store.StatusWaiting || st.NotBefore == nil || !st.NotBefore.Equal(wantAt) || st.ErrorClass != "wait" {
		t.Fatalf("metadata = %+v", st)
	}
	tk, _ := r.st.Read().TaskByStageRun(ctx, st.ID)
	if tk.State != store.TaskPending || tk.Attempts != 0 {
		t.Errorf("wait 不消耗重試次數：%+v", tk)
	}
	if got, _ := r.st.Read().ItemByID(ctx, it.ID); got.Title != "T" {
		t.Error("等待期間 metadata 仍然更新")
	}

	// 時間還沒到：什麼都不做。
	r.run()
	if r.yt.probes != 1 {
		t.Errorf("未到期不應再 Probe：%d", r.yt.probes)
	}
	// 到期後仍在直播：繼續等。
	r.clk.Advance(liveRecheckDelay)
	r.run()
	if st = r.stage(it, StageMetadata); st.Status != store.StatusWaiting || r.yt.probes != 2 {
		t.Errorf("到期後重新檢查：%+v（Probe %d 次）", st, r.yt.probes)
	}
	// VOD 出來了：往下做。
	r.yt.info.LiveStatus = "was_live"
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	r.clk.Advance(liveRecheckDelay)
	r.run()
	for _, s := range Stages {
		if r.stage(it, s).Status != store.StatusDone {
			t.Errorf("步驟 %s = %s", s, r.stage(it, s).Status)
		}
	}
}

func TestRetryableErrorsBackOffThenFail(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.probeErr = &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", ExitCode: 1}
	it := r.ingest(watch, nil, "")
	t0 := r.clk.Now()

	r.run()
	st := r.stage(it, StageMetadata)
	if st.Status != store.StatusPending || st.ErrorClass != "retryable" || st.NotBefore == nil || !st.NotBefore.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("第 1 次失敗後：%+v", st)
	}
	tk, _ := r.st.Read().TaskByStageRun(ctx, st.ID)
	if tk.Attempts != 1 {
		t.Errorf("attempts = %d", tk.Attempts)
	}

	r.clk.Advance(30 * time.Second)
	r.run()
	st = r.stage(it, StageMetadata)
	if st.Status != store.StatusPending || !st.NotBefore.Equal(t0.Add(30*time.Second).Add(2*time.Minute)) {
		t.Fatalf("第 2 次失敗後：%+v", st)
	}

	r.clk.Advance(2 * time.Minute)
	r.run()
	st = r.stage(it, StageMetadata)
	if st.Status != store.StatusFailed || st.ErrorClass != "retryable" || st.NotBefore != nil {
		t.Fatalf("次數用盡後：%+v", st)
	}
	if tk, _ = r.st.Read().TaskByStageRun(ctx, st.ID); tk.State != store.TaskFailed || tk.Attempts != 3 {
		t.Errorf("task = %+v", tk)
	}
	if r.yt.probes != 3 {
		t.Errorf("Probe %d 次，預期 3", r.yt.probes)
	}
}

func TestNeedsActionAndPermanentErrors(t *testing.T) {
	for _, tt := range []struct {
		class  errclass.Class
		status string
		reason string
	}{
		{errclass.ClassNeedsAction, store.StatusBlocked, "needs_action"},
		{errclass.ClassBudgetBlocked, store.StatusBlocked, "budget_blocked"},
		{errclass.ClassPermanent, store.StatusFailed, ""},
	} {
		r := newRig(t)
		r.yt.probeErr = &errclass.Error{Class: tt.class, Source: "yt-dlp"}
		it := r.ingest(watch, nil, "")
		r.run()
		st := r.stage(it, StageMetadata)
		if st.Status != tt.status || st.BlockReason != tt.reason || st.ErrorClass != string(tt.class) || st.Message == "" {
			t.Errorf("%s：%+v", tt.class, st)
		}
		if r.yt.probes != 1 {
			t.Errorf("%s 不應重試：Probe %d 次", tt.class, r.yt.probes)
		}
	}
}

func TestUnclassifiedErrorsAreRetryable(t *testing.T) {
	r := newRig(t)
	r.yt.probeErr = errors.New("something odd")
	it := r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusPending || st.ErrorClass != "retryable" {
		t.Errorf("%+v", st)
	}
}

func TestCancelledContextLeavesStateUntouched(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	// 執行到一半（Probe 時）收到關閉訊號。
	r.yt.onProbe = cancel
	r.yt.probeErr = context.Canceled
	it := r.ingest(watch, nil, "")
	tasks, err := queue.New(r.st, r.clk, "w1").Claim(context.Background(), 1)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	if err := r.p.Handle(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	if st := r.stage(it, StageMetadata); st.Status != store.StatusRunning {
		t.Errorf("取消不應改變步驟狀態：%+v", st)
	}
	if tk, _ := r.st.Read().Task(context.Background(), tasks[0].ID); tk.State != store.TaskLeased {
		t.Errorf("任務應維持 leased，由租約到期或重啟放回：%+v", tk)
	}
}

func TestEmptyCaptionFileIsRejected(t *testing.T) {
	r := newRig(t)
	r.yt.cues = 0
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "transcript")
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed {
		t.Fatalf("%+v", st)
	}
	d, _ := r.p.Item(context.Background(), it.ID)
	if d.Sources[0].SourceID != "youtube-manual" || d.Sources[0].State != "rejected" || d.Sources[0].Reason != "empty" {
		t.Errorf("sources = %+v", d.Sources)
	}
}

func TestCacheKeyHitReusesArtifact(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")
	r.run()
	before, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	probes, fetches := r.yt.probes, len(r.yt.fetched)

	// 重跑 transcript 與 normalize：上游與參數都沒變，直接重用既有的產出（PIPE-R5）。
	for _, stage := range []string{StageTranscript, StageNormalize} {
		old := r.stage(it, stage)
		var run store.StageRun
		err := r.st.Tx(ctx, func(rp *store.Repo) error {
			var err error
			if run, err = rp.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: stage, Status: store.StatusPending, ConfigSnapshotID: old.ConfigSnapshotID}); err != nil {
				return err
			}
			_, err = insertStageTask(ctx, rp, it.ID, run, queue.PriorityManual)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		r.run()
		got, _ := r.st.Read().StageRun(ctx, run.ID)
		if got.Status != store.StatusDone || got.ArtifactID != old.ArtifactID || got.CacheKey != old.CacheKey {
			t.Errorf("%s 重跑：%+v，預期重用 %s", stage, got, old.ArtifactID)
		}
	}
	after, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
	if len(after) != len(before) {
		t.Errorf("快取命中不建立新版本：%d → %d", len(before), len(after))
	}
	// transcript 的快取鍵含字幕軌清單，所以要 Probe 一次（只讀 metadata）；字幕不會重新下載。
	if r.yt.probes != probes+1 || len(r.yt.fetched) != fetches {
		t.Errorf("快取命中只 Probe、不下載：Probe %d → %d，下載 %d → %d", probes, r.yt.probes, fetches, len(r.yt.fetched))
	}
}

func TestCuesVariantLangAndRange(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.cues = 10
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "")

	if _, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1}); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("還沒有字幕：%v", err)
	}
	if _, err := r.p.Cues(ctx, "nope", CuesQuery{ToMS: -1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("影音不存在：%v", err)
	}
	r.run()

	// 沒指定變體：依 OUT-R2 選處理得最完整的（normalized 優先於 source）。
	tr, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1})
	if err != nil || tr.Variant != "normalized" || tr.Lang != "en" {
		t.Fatalf("Cues = %v, %v", tr.Variant, err)
	}
	if src, _ := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", ToMS: -1}); src.Variant != "source" {
		t.Errorf("指定變體 = %s", src.Variant)
	}
	// 時間範圍（OUT-R3）：cue 每句 2 秒，[4s, 8s) 與 cue 3、4 重疊（4–6、6–8）。
	part, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", FromMS: 4000, ToMS: 8000})
	if err != nil || len(part.Cues) != 2 || part.Cues[0].StartMS != 4000 {
		t.Errorf("範圍：%+v, %v", part.Cues, err)
	}
	// 語言不符、變體不存在、變體不合法。
	if _, err := r.p.Cues(ctx, it.ID, CuesQuery{Lang: "ja", ToMS: -1}); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("語言不符：%v", err)
	}
	if _, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "translated", ToMS: -1}); !errors.Is(err, ErrNoTranscript) {
		t.Errorf("變體不存在：%v", err)
	}
	var inv *InvalidError
	if _, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "bogus", ToMS: -1}); !errors.As(err, &inv) {
		t.Errorf("變體不合法：%v", err)
	}
	if tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Lang: "EN", Variant: "source", ToMS: -1}); err != nil || len(tr.Cues) != 10 {
		t.Errorf("語言不分大小寫：%v", err)
	}
}

func TestCuesDetectsCorruptedArtifact(t *testing.T) {
	r := newRig(t)
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, "transcript")
	r.run()
	arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, "transcript")
	if err := os.WriteFile(filepath.Join(r.dir, arts[0].Path), []byte(`{"lang":"en"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Cues(context.Background(), it.ID, CuesQuery{ToMS: -1}); !errors.Is(err, ErrCorruptArtifact) {
		t.Errorf("err = %v", err)
	}
}

func TestArtifactFilesAreWrittenAtomically(t *testing.T) {
	dir := t.TempDir()
	// render 失敗時不留下正式檔，也不留下暫存檔。
	boom := errors.New("boom")
	if _, _, _, err := writeArtifactFile(dir, "item", "art", func(io.Writer) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "artifacts", "item"))
	if len(entries) != 0 {
		t.Errorf("失敗後不應留下檔案：%v", entries)
	}
	rel, sha, size, err := writeArtifactFile(dir, "item", "art", func(w io.Writer) error { _, err := io.WriteString(w, "hello"); return err })
	if err != nil || rel != "artifacts/item/art.json" || size != 5 || sha != sha256hex([]byte("hello")) {
		t.Fatalf("%q %q %d %v", rel, sha, size, err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, rel)); fi.Mode().Perm() != 0o600 {
		t.Errorf("權限 = %v", fi.Mode().Perm())
	}
}

func TestSettingsValidationAndMerge(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var inv *InvalidError
	var ni *NotImplementedError
	for name, body := range map[string]map[string]any{
		"沒有目標語言":   {"targets": []any{}},
		"kind 不合法": {"caption_priority": []any{map[string]any{"lang": "en", "kind": "translated"}}},
		"沒有 lang":  {"caption_priority": []any{map[string]any{"kind": "manual"}}},
		"未知欄位":     {"nope": true},
		"策略不合法":    {"variant_strategy": "magic"},
	} {
		if _, err := r.p.SetSettings(ctx, "language", body); !errors.As(err, &inv) {
			t.Errorf("%s：err = %v", name, err)
		}
	}
	if _, err := r.p.SetSettings(ctx, "services", map[string]any{}); !errors.As(err, &ni) {
		t.Errorf("services：%v", err)
	}
	if _, err := r.p.GetSettings(ctx, "services"); !errors.As(err, &ni) {
		t.Errorf("services：%v", err)
	}

	// 沒設定過：回傳內建預設。
	def, err := r.p.GetSettings(ctx, "language")
	if err != nil || def["allow_platform_translated"] != false || len(def["caption_priority"].([]any)) != 3 {
		t.Fatalf("預設 = %v, %v", def, err)
	}
	// 部分設定與預設合併。
	got, err := r.p.SetSettings(ctx, "language", map[string]any{"targets": []any{"en"}, "fallback": []any{"groq"}})
	if err != nil {
		t.Fatal(err)
	}
	if got["targets"].([]any)[0] != "en" || got["fallback"].([]any)[0] != "groq" || len(got["caption_priority"].([]any)) != 3 {
		t.Errorf("合併結果 = %v", got)
	}
	// 被拒絕的設定不會存入。
	_, _ = r.p.SetSettings(ctx, "language", map[string]any{"targets": []any{}})
	if again, _ := r.p.GetSettings(ctx, "language"); again["targets"].([]any)[0] != "en" {
		t.Errorf("無效的設定不應存入：%v", again)
	}
}

func TestEffectiveMergeOrder(t *testing.T) {
	eff, err := BuildEffective(
		map[string]any{"language": map[string]any{"targets": []any{"ja"}, "fallback": []any{"groq"}}},
		map[string]any{"language": map[string]any{"targets": []any{"en"}}, "llm": map[string]any{"x": 1}},
	)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := eff.Language()
	if l.Targets[0] != "en" || l.Fallback[0] != "groq" || len(l.CaptionPriority) != 3 {
		t.Errorf("language = %+v", l)
	}
	if eff["llm"] == nil {
		t.Error("還不認識的區段要保留在快照中")
	}
	// 相同的設定得到相同的快照。
	a, _, _ := eff.Snapshot()
	eff2, _ := BuildEffective(map[string]any{"language": map[string]any{"targets": []any{"en"}, "fallback": []any{"groq"}}}, map[string]any{"llm": map[string]any{"x": 1}})
	if b, _, _ := eff2.Snapshot(); a != b {
		t.Error("相同的生效設定應有相同的 sha256")
	}
}

func TestNextStageHonoursUntil(t *testing.T) {
	tests := []struct {
		stage, until, next string
		ok                 bool
	}{
		{"metadata", "", "transcript", true},
		{"metadata", "metadata", "", false},
		{"metadata", "transcript", "transcript", true},
		{"transcript", "transcript", "", false},
		{"transcript", "normalize", "normalize", true},
		{"normalize", "", "", false},
	}
	for _, tt := range tests {
		if n, ok := nextStage(tt.stage, tt.until); n != tt.next || ok != tt.ok {
			t.Errorf("nextStage(%s, %s) = %s, %v", tt.stage, tt.until, n, ok)
		}
	}
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
