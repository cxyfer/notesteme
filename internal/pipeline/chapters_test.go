package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/worker"
)

// SPB-R7、B2：一半以上的時間落在片段內才算片段內的 cue；沒有長度的 cue 看起點。
func TestCueInSponsor(t *testing.T) {
	segs := []sponsorblock.Segment{{StartMS: 10_000, EndMS: 20_000}, {StartMS: 30_000, EndMS: 31_000}}
	cases := []struct {
		start, end int64
		in         bool
	}{
		{11_000, 12_000, true},  // 完全在片段內
		{0, 5_000, false},       // 完全在片段外
		{8_000, 14_000, true},   // 4 秒在內、2 秒在外
		{7_000, 13_000, false},  // 剛好一半：不算
		{29_500, 31_500, false}, // 跨過短片段：1 秒在內、1 秒在外
		{19_000, 32_000, false}, // 跨兩個片段：共 2 秒在內
		{15_000, 15_000, true},  // 沒有長度、起點在片段內
		{20_000, 20_000, false}, // 起點在片段結束的那一刻
	}
	for _, c := range cases {
		if got := cueInSponsor(subtitle.Cue{StartMS: c.start, EndMS: c.end}, segs); got != c.in {
			t.Errorf("[%d,%d] = %v，預期 %v", c.start, c.end, got, c.in)
		}
	}
}

// CHAP-R5：第一個章節從 0 開始，最後一個到片長；不知道片長時用 lastEnd。
func TestFillChapterEnds(t *testing.T) {
	list := []Chapter{{StartMS: 20_000}, {StartMS: 300_000}}
	fillChapterEnds(list, 600_000, 545_000)
	if list[0].StartMS != 0 || list[0].EndMS != 300_000 || list[1].EndMS != 600_000 {
		t.Errorf("list = %+v", list)
	}
	list = []Chapter{{StartMS: 0}, {StartMS: 300_000}}
	fillChapterEnds(list, 0, 545_000)
	if list[1].EndMS != 545_000 {
		t.Errorf("沒有片長：%+v", list)
	}
	list = []Chapter{{StartMS: 0}, {StartMS: 300_000}}
	fillChapterEnds(list, 100_000, 0)
	if list[1].EndMS != 300_000 {
		t.Errorf("片長早於起點：%+v", list)
	}
}

func TestPlatformChapterList(t *testing.T) {
	dur := 900
	it := store.Item{DurationS: &dur, Metadata: json.RawMessage(`{"chapters":[
		{"title":"Main","start":300,"end":720},{"title":" Intro‮\n ","start":0.4,"end":300},{"title":"Q&A","start":720,"end":900}]}`)}
	got := platformChapterList(it)
	want := []Chapter{{Title: "Intro", StartMS: 0, EndMS: 300_000}, {Title: "Main", StartMS: 300_000, EndMS: 720_000}, {Title: "Q&A", StartMS: 720_000, EndMS: 900_000}}
	if len(got) != len(want) {
		t.Fatalf("got = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v，預期 %+v", i, got[i], want[i])
		}
	}
	if n := platformChapterCount(store.Item{Metadata: json.RawMessage(`not json`)}); n != 0 {
		t.Errorf("格式不對的 metadata：%d", n)
	}
	if s := sanitizePlatformTitle(strings.Repeat("a", 300)); len(s) != maxPlatformChapterTitle {
		t.Errorf("標題沒有截斷：%d", len(s))
	}
}

// CHAP-R1、R8：off 優先於平台章節；平台章節優先於 not_configured；1 個平台章節視為沒有。
func TestResolveChaptersPlatform(t *testing.T) {
	off := mustEffective(t, stagesOver(StageChapters, map[string]any{"executor": "off"}))
	api := mustEffective(t, stagesOver(StageChapters, map[string]any{"provider": "anthropic", "model": "m"}))
	def := mustEffective(t, nil)
	cases := []struct {
		name     string
		eff      Effective
		n        int
		executor string
		skip     string
	}{
		{"off 優先", off, 3, ExecutorOff, SkipOff},
		{"平台章節不需要 provider", def, 2, ExecutorLocal, ""},
		{"平台章節優先於 api", api, 3, ExecutorLocal, ""},
		{"1 個平台章節視為沒有", def, 1, ExecutorAPI, SkipNotConfigured},
		{"1 個平台章節走 api", api, 1, ExecutorAPI, ""},
	}
	for _, c := range cases {
		res, err := ResolveExecutor(c.eff, StageChapters, StageContext{Platform: "youtube", PlatformChapters: c.n})
		if err != nil {
			t.Fatal(err)
		}
		if res.Executor != c.executor || res.Reason != c.skip || res.Skip != (c.skip != "") {
			t.Errorf("%s：%+v", c.name, res)
		}
	}
	if !stageImplemented(StageChapters, ExecutorLocal) || !stageImplemented(StageChapters, ExecutorAPI) || !stageImplemented(StageChapters, ExecutorAgent) {
		t.Error("chapters 的 local、api、agent 都應該已實作")
	}
}

// 平台章節（CHAP-R1）：完整處理後 chapters 是 skipped（platform_chapters），產出是 origin platform；
// 重跑時內容相同就沿用現行產出。
func TestPlatformChaptersArtifact(t *testing.T) {
	r := sponsorRig(t, &fakeSponsor{})
	dur := 900
	r.yt.info.DurationS = &dur
	r.yt.info.Chapters = []youtube.Chapter{{Title: "Intro", Start: 0, End: 300}, {Title: "Main", Start: 300, End: 720}, {Title: "Q&A", Start: 720, End: 900}}
	it := r.ingest(watch, nil, "")
	r.run()
	st := latestStage(t, r, it.ID, StageChapters)
	if st.Status != store.StatusSkipped || st.BlockReason != SkipPlatformChapters || st.Executor != ExecutorLocal || st.ArtifactID == "" {
		t.Fatalf("chapters = %+v", st)
	}
	doc, ok, err := r.p.ItemChapters(context.Background(), it.ID)
	if err != nil || !ok || doc.Origin != ChapterOriginPlatform || len(doc.Chapters) != 3 || doc.Chapters[2].EndMS != 900_000 {
		t.Fatalf("chapters = %+v %v %v", doc, ok, err)
	}
	if pub := latestStage(t, r, it.ID, StagePublish); pub.Status != store.StatusSkipped {
		t.Errorf("publish = %+v", pub)
	}
	r.rerun(it.ID, StageChapters, nil)
	if again := latestStage(t, r, it.ID, StageChapters); again.ArtifactID != st.ArtifactID {
		t.Errorf("內容相同應沿用現行產出：%s → %s", st.ArtifactID, again.ArtifactID)
	}
}

// chapPrompts 把每個引用都解析成 chapters 類型的第 1 版。
type chapPrompts struct{}

func (chapPrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	return ResolvedPrompt{ID: id, Kind: StageChapters, Version: 1, SHA256: "sha-ch1", Body: "分章節。{{title}}"}, nil
}

// chapLLM 是回答固定章節的假 provider，記下每個請求的資料區塊。
type chapLLM struct {
	mu   sync.Mutex
	out  string
	data []string
}

func (f *chapLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = append(f.data, req.Fixed.System+"\n"+req.Fixed.Prefix+"\n"+req.Data+"\n"+req.Retry)
	return llm.Response{Output: json.RawMessage(f.out)}, nil
}

func (f *chapLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

// newChapRig 建立會走 LLM 章節的 rig：影音做到 metadata（片長 600 秒），寫入 4 句 corrected。
// 第 3 句（180～185 秒）在 SponsorBlock 片段內。
func newChapRig(t *testing.T, fake *chapLLM, fs *fakeSponsor) (*rig, store.Item) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dur := 600
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", DurationS: &dur}}
	q := queue.New(st, clk, "w1")
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: chapPrompts{}, LLM: reg, SponsorBlock: fs, SponsorBlockBaseURL: testSponsorBase})
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	over := map[string]any{"stages": map[string]any{"chapters": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "chapters-default"}}}
	it := r.ingest(watch, over, StageMetadata)
	r.run()
	cues := []subtitle.Cue{
		{Index: 1, StartMS: 5_000, EndMS: 10_000, Text: "Welcome back.", Timing: subtitle.TimingSource},
		{Index: 2, StartMS: 20_000, EndMS: 25_000, Text: "Toolchain setup.", Timing: subtitle.TimingSource},
		{Index: 3, StartMS: 180_000, EndMS: 185_000, Text: "Sponsored by NordWidget.", Timing: subtitle.TimingSource},
		{Index: 4, StartMS: 300_000, EndMS: 305_000, Text: "The boot loader.", Timing: subtitle.TimingSource},
	}
	if err := r.p.SeedTranscript(context.Background(), SeedRequest{ItemID: it.ID, Variant: subtitle.VariantCorrected, Lang: "en", Cues: cues}); err != nil {
		t.Fatal(err)
	}
	return r, it
}

func sponsorAt(start, end int64) *fakeSponsor {
	return &fakeSponsor{res: sponsorblock.Result{Outcome: sponsorblock.OutcomeOK, Segments: []sponsorblock.Segment{{Category: "sponsor", StartMS: start, EndMS: end}}}}
}

// CHAP-R2、R5～R7：沒有查過 SponsorBlock 的影音從 chapters 重跑時先查詢（H8）；片段內的 cue 不放進輸入，
// 時間由程式換算，parents 是輸入字幕與 sponsor_segments。再從 chapters 重跑不重新查詢，步驟層的快取命中。
func TestChaptersFromLLM(t *testing.T) {
	fake := &chapLLM{out: `{"chapters":[{"cue":2,"title":"Toolchain"},{"cue":4,"title":"Boot"}]}`}
	fs := sponsorAt(180_000, 210_000)
	r, it := newChapRig(t, fake, fs)
	r.rerun(it.ID, StageChapters, nil)
	if fs.calls != 1 {
		t.Fatalf("SponsorBlock 應查詢 1 次：%d", fs.calls)
	}
	st := latestStage(t, r, it.ID, StageChapters)
	if st.Status != store.StatusDone || st.Executor != ExecutorAPI || fake.calls() != 1 {
		t.Fatalf("chapters = %+v，calls = %d", st, fake.calls())
	}
	if req := fake.data[0]; strings.Contains(req, "NordWidget") || !strings.Contains(req, "Toolchain setup.") || strings.Contains(req, "00:05:00") {
		t.Errorf("請求的資料不正確：%s", req)
	}
	doc, ok, err := r.p.ItemChapters(context.Background(), it.ID)
	want := []Chapter{{Title: "Toolchain", StartMS: 0, EndMS: 300_000, Cue: 2}, {Title: "Boot", StartMS: 300_000, EndMS: 600_000, Cue: 4}}
	if err != nil || !ok || doc.Origin != ChapterOriginLLM || len(doc.Chapters) != 2 || doc.Chapters[0] != want[0] || doc.Chapters[1] != want[1] {
		t.Fatalf("chapters = %+v %v %v", doc, ok, err)
	}
	art, err := r.st.Read().Artifact(context.Background(), st.ArtifactID)
	if err != nil || len(art.Parents) != 2 {
		t.Fatalf("parents = %v %v", art.Parents, err)
	}
	if sb, _, _ := r.p.currentSponsorArtifact(context.Background(), it.ID); art.Parents[1] != sb.ID {
		t.Errorf("parents[1] 應是 sponsor_segments：%v", art.Parents)
	}
	var prov map[string]any
	_ = json.Unmarshal(art.Provenance, &prov)
	if prov["origin"] != ChapterOriginLLM || prov["provider"] != "anthropic" || prov["stage"] != StageChapters {
		t.Errorf("provenance = %v", prov)
	}

	r.rerun(it.ID, StageChapters, nil)
	if fs.calls != 1 || fake.calls() != 1 {
		t.Errorf("已經查過不再查詢（SPB-R8）、步驟層快取命中：sponsor=%d llm=%d", fs.calls, fake.calls())
	}
}

// 片段改變時輸入的 cue 改變，步驟層的快取不能命中舊的結果（sponsor 的 sha256 在快取鍵中）。
func TestChaptersCacheFollowsSponsorSegments(t *testing.T) {
	fake := &chapLLM{out: `{"chapters":[{"cue":1,"title":"All"}]}`}
	fs := sponsorAt(180_000, 210_000)
	r, it := newChapRig(t, fake, fs)
	r.rerun(it.ID, StageChapters, nil)
	r.rerun(it.ID, StageSponsorBlock, nil)
	if fake.calls() != 1 {
		t.Fatalf("片段相同時快取命中：%d", fake.calls())
	}
	fs.mu.Lock()
	fs.res.Segments = []sponsorblock.Segment{{Category: "sponsor", StartMS: 290_000, EndMS: 320_000}}
	fs.mu.Unlock()
	r.rerun(it.ID, StageSponsorBlock, nil)
	if fake.calls() != 2 {
		t.Fatalf("片段改變後應重新送出：%d", fake.calls())
	}
	if req := fake.data[1]; !strings.Contains(req, "NordWidget") || strings.Contains(req, "The boot loader.") {
		t.Errorf("新的片段排除第 4 句：%s", req)
	}
}

// CHAP-R3、R4：驗證失敗會帶著錯誤重試；重試用盡時沒有章節，步驟 done，訊息說明原因。
func TestChaptersFallbackLeavesNoChapters(t *testing.T) {
	fake := &chapLLM{out: `{"chapters":[{"cue":2,"title":"a"},{"cue":3,"title":"b"}]}`}
	r, it := newChapRig(t, fake, sponsorAt(180_000, 210_000))
	r.rerun(it.ID, StageChapters, nil)
	st := latestStage(t, r, it.ID, StageChapters)
	wantMsg := "無法產生章節：模型回傳的內容沒有通過驗證（chapters[1].cue 3 不在輸入中）。筆記不會分章節。"
	if st.Status != store.StatusDone || st.Message != wantMsg || strings.Join(st.Actions, ",") != "重新產生章節,開啟 Prompt" || st.CacheKey != "" {
		t.Fatalf("chapters = %+v", st)
	}
	if fake.calls() < 2 || !strings.Contains(fake.data[1], "chapters[1].cue 3 不在輸入中") {
		t.Errorf("重試沒有帶上錯誤：%d %v", fake.calls(), fake.data)
	}
	doc, ok, _ := r.p.ItemChapters(context.Background(), it.ID)
	if !ok || doc.Origin != ChapterOriginLLM || len(doc.Chapters) != 0 {
		t.Errorf("chapters = %+v %v", doc, ok)
	}
}

// CHAP-R2：多個 chunk 時，重疊區的章節屬於前一個 chunk；總數超過上限只保留前面的。
func TestMergeChapters(t *testing.T) {
	job := &llmJob{
		cues:   []llm.Cue{{I: 1}, {I: 2}, {I: 4}, {I: 5}, {I: 6}},
		chunks: []llm.Chunk{{Index: 1, From: 1, To: 3}, {Index: 2, From: 3, To: 5, Overlap: 1}},
	}
	outs := []json.RawMessage{
		json.RawMessage(`{"chapters":[{"cue":1,"title":"a"},{"cue":4,"title":"b"}]}`),
		json.RawMessage(`{"chapters":[{"cue":4,"title":"b2"},{"cue":6,"title":"c"}]}`),
	}
	got, err := mergeChapters(job, outs, 20)
	if err != nil || len(got) != 3 || got[1].Title != "b" || got[2].Cue != 6 {
		t.Fatalf("got = %+v %v", got, err)
	}
	if got, _ := mergeChapters(job, outs, 2); len(got) != 2 {
		t.Errorf("上限 2：%+v", got)
	}
}

// H8：沒有執行過 sponsorblock 時，從 chapters、frames、note 重跑改從 sponsorblock 開始；其他起點不變。
func TestRerunStart(t *testing.T) {
	fs := &fakeSponsor{}
	r, it := newChapRig(t, &chapLLM{out: `{"chapters":[{"cue":1,"title":"a"}]}`}, fs)
	ctx := context.Background()
	for _, c := range []struct{ from, want string }{
		{StageChapters, StageSponsorBlock}, {StageFrames, StageSponsorBlock}, {StageNote, StageSponsorBlock},
		{StagePublish, StagePublish}, {StageCorrect, StageCorrect},
	} {
		if got, err := r.p.rerunStart(ctx, r.st.Read(), it.ID, c.from); err != nil || got != c.want {
			t.Errorf("%s → %s %v，預期 %s", c.from, got, err, c.want)
		}
	}
	r.rerun(it.ID, StageChapters, nil)
	if got, _ := r.p.rerunStart(ctx, r.st.Read(), it.ID, StageChapters); got != StageChapters {
		t.Errorf("查過之後不再改起點：%s", got)
	}
}

// CHAP-R8、AGT 的 M3 修訂：agent 時任務的 kind 是 chapters；省略 kinds 時不領 chapters，指定時可以領，
// 提交的結果通過驗證後寫成章節（驗證與 api 共用 Spec，見 llm 的 CheckChapters）。
func TestChaptersAgent(t *testing.T) {
	fake := &chapLLM{out: `{}`}
	r, it := newChapRig(t, fake, sponsorAt(180_000, 210_000))
	ctx := context.Background()
	r.rerun(it.ID, StageChapters, map[string]any{"stages": map[string]any{"chapters": map[string]any{"executor": "agent"}}})
	if st := latestStage(t, r, it.ID, StageChapters); st.Status != store.StatusAwaitingAgent {
		t.Fatalf("chapters = %+v", st)
	}
	if got, err := r.p.ClaimAgentTasks(ctx, "a1", nil, 0); err != nil || len(got) != 0 {
		t.Fatalf("省略 kinds 不領 chapters：%+v %v", got, err)
	}
	got, err := r.p.ClaimAgentTasks(ctx, "a1", []string{StageChapters}, 0)
	if err != nil || len(got) != 1 || got[0].Kind != StageChapters || strings.Contains(got[0].Prompt.User, "NordWidget") {
		t.Fatalf("領取 = %+v %v", got, err)
	}
	ok := AgentSubmission{Output: json.RawMessage(`{"chapters":[{"cue":2,"title":"Toolchain"}]}`)}
	if err := r.p.SubmitAgentTask(ctx, "a1", got[0].TaskID, got[0].LeaseToken, ok); err != nil {
		t.Fatal(err)
	}
	r.run()
	doc, found, _ := r.p.ItemChapters(ctx, it.ID)
	if !found || len(doc.Chapters) != 1 || doc.Chapters[0].Title != "Toolchain" || fake.calls() != 0 {
		t.Errorf("chapters = %+v，llm calls = %d", doc, fake.calls())
	}
}
