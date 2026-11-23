package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/notes"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/worker"
)

// notePrompts 把 note-default、note-map-default 解析成對應類型的第 1 版；body 帶上 note_lang，驗證變數有代入。
type notePrompts struct{}

func (notePrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	kind := StageNote
	if strings.HasPrefix(id, "note-map-") {
		kind = TaskNoteMap
	}
	return ResolvedPrompt{ID: id, Kind: kind, Version: 1, SHA256: "sha-" + id, Body: "寫筆記，語言 {{note_lang}}。{{title}}"}, nil
}

// noteLLM 依 Spec 的步驟回答 note 或 note_map，記下每個請求。
type noteLLM struct {
	mu       sync.Mutex
	note     []string // 依序回答；用完之後重複最後一個
	mapOut   string
	reqs     []llm.Request
	noteHits int
	mapHits  int
}

func (f *noteLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if req.Spec != nil && req.Spec.Stage == llm.StageNoteMap {
		f.mapHits++
		return llm.Response{Output: json.RawMessage(f.mapOut)}, nil
	}
	out := f.note[min(f.noteHits, len(f.note)-1)]
	f.noteHits++
	return llm.Response{Output: json.RawMessage(out)}, nil
}

func (f *noteLLM) text(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.reqs[i]
	return r.Fixed.System + "\n" + r.Fixed.Prefix + "\n" + r.Data + "\n" + r.Retry
}

func (f *noteLLM) count() (note, maps int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.noteHits, f.mapHits
}

const goodNote = `{"sections":[
	{"level":2,"title":"Toolchain","cue":2,"body":"Install it [[cue:2]].","frames":[1]},
	{"level":2,"title":"Boot","cue":4,"body":"The loader.","frames":[]}],
	"self_check":["What is a toolchain?"]}`

// noteOver 是 note 走 api、chapters 與 frames 關閉的覆寫；extra 合併進 stages.note。
func noteOver(extra map[string]any) map[string]any {
	note := map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "note-default"}
	for k, v := range extra {
		note[k] = v
	}
	return map[string]any{"stages": map[string]any{
		"chapters": map[string]any{"executor": "off"}, "frames": map[string]any{"executor": "off"}, "note": note,
	}}
}

// newNoteRig 建立 note 走 LLM 的 rig：影音做到 metadata（片長 600 秒，durS 可改），寫入 4 句 corrected，
// 第 3 句在 SponsorBlock 片段內；sponsorblock、chapters、frames 先做過一次（後兩者關閉），之後由測試寫入章節與畫面。
func newNoteRig(t *testing.T, fake *noteLLM, durS int) (*rig, store.Item) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "Writing a Toy Kernel", Language: "en", DurationS: &durS}}
	q := queue.New(st, clk, "w1")
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: notePrompts{},
		LLM: reg, SponsorBlock: sponsorAt(180_000, 210_000), SponsorBlockBaseURL: testSponsorBase})
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	it := r.ingest(watch, noteOver(nil), StageMetadata)
	r.run()
	cues := []subtitle.Cue{
		{Index: 1, StartMS: 5_000, EndMS: 10_000, Text: "Welcome back.", Timing: subtitle.TimingSource},
		{Index: 2, StartMS: 20_000, EndMS: 25_000, Text: "Toolchain setup.", Timing: subtitle.TimingSource},
		{Index: 3, StartMS: 180_000, EndMS: 185_000, Text: "Sponsored by NordWidget.", Timing: subtitle.TimingSource},
		{Index: 4, StartMS: 300_000, EndMS: 305_000, Text: "The boot loader.", Timing: subtitle.TimingSource},
	}
	ctx := context.Background()
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantCorrected, Lang: "en", Cues: cues}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSponsorBlock, Until: StageFrames}); err != nil {
		t.Fatal(err)
	}
	r.run()
	return r, it
}

// seedNoteInputs 寫入兩個章節與兩張畫面（第 1 張選中）。
func seedNoteInputs(t *testing.T, r *rig, it store.Item) {
	t.Helper()
	ctx := context.Background()
	if err := r.p.SeedChapters(ctx, it.ID, []SeedChapter{{Title: "Boot", StartMS: 300_000, Cue: 4}, {Title: "Setup", StartMS: 0}}); err != nil {
		t.Fatal(err)
	}
	if err := r.p.SeedFrames(ctx, it.ID, []SeedFrame{
		{AtMS: 21_000, Selected: true, Caption: "Toolchain diagram", Color: "#336699"},
		{AtMS: 400_000, Caption: "unused"},
	}); err != nil {
		t.Fatal(err)
	}
}

func rerunNote(t *testing.T, r *rig, it store.Item, over map[string]any) store.StageRun {
	t.Helper()
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageNote, Until: StageNote, Overrides: over}); err != nil {
		t.Fatal(err)
	}
	r.run()
	return latestStage(t, r, it.ID, StageNote)
}

// NOTE-R1～R5、R8、R9、R13：一個請求寫出整份筆記；片段內的句子不送出、畫面只給 cue 與圖說；
// 標題帶時間連結、畫面嵌入、frontmatter 由模板產生；parents 依序是輸入、chapters、frames、sponsor_segments。
func TestNoteFromLLM(t *testing.T) {
	fake := &noteLLM{note: []string{goodNote}}
	r, it := newNoteRig(t, fake, 600)
	seedNoteInputs(t, r, it)
	st := rerunNote(t, r, it, nil)
	if st.Status != store.StatusDone || st.Executor != ExecutorAPI || st.ArtifactID == "" {
		t.Fatalf("note = %+v", st)
	}
	if n, m := fake.count(); n != 1 || m != 0 {
		t.Fatalf("calls = %d/%d", n, m)
	}
	req := fake.text(0)
	for _, want := range []string{"Toolchain setup.", "Toolchain diagram", "Setup", "Boot", "語言 zh-Hant-TW"} {
		if !strings.Contains(req, want) {
			t.Errorf("請求沒有 %q：%s", want, req)
		}
	}
	for _, unwanted := range []string{"NordWidget", "unused", "00:00:21"} {
		if strings.Contains(req, unwanted) {
			t.Errorf("請求不該有 %q：%s", unwanted, req)
		}
	}
	ctx := context.Background()
	v, err := r.p.ItemNote(ctx, it.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Lang != "zh-Hant-TW" || v.Stale || v.Frontmatter["title"] != "Writing a Toy Kernel" || v.Frontmatter["notesteme_status"] != notes.StatusComplete {
		t.Errorf("view = %+v", v)
	}
	for _, want := range []string{
		"## Toolchain · [00:20](https://www.youtube.com/watch?v=abc123xxxxx&t=20s)",
		"Install it [00:20](https://www.youtube.com/watch?v=abc123xxxxx&t=20s).",
		"| 畫質 | 720p（來源的最高畫質） |",
		"Writing a Toy Kernel - 00-21 - frame_0001.jpg",
		"Toolchain diagram",
		"## 自我檢驗",
	} {
		if !strings.Contains(v.Markdown, want) {
			t.Errorf("筆記沒有 %q：\n%s", want, v.Markdown)
		}
	}
	if !notes.Passed(v.Checks) || len(v.Checks) != len(notes.CheckIDs) {
		t.Errorf("checks = %+v", v.Checks)
	}
	art, err := r.st.Read().Artifact(ctx, st.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	chap, _, _ := r.p.currentChaptersArtifact(ctx, it.ID)
	frm, _, _, _ := r.p.currentFrames(ctx, it.ID)
	sb, _, _ := r.p.currentSponsorArtifact(ctx, it.ID)
	if len(art.Parents) != 4 || art.Parents[1] != chap.ID || art.Parents[2] != frm.ID || art.Parents[3] != sb.ID {
		t.Errorf("parents = %v，預期 [input %s %s %s]", art.Parents, chap.ID, frm.ID, sb.ID)
	}
	if art.Kind != KindNote || art.Lang != "zh-Hant-TW" {
		t.Errorf("artifact = %+v", art)
	}
	var prov map[string]any
	_ = json.Unmarshal(art.Provenance, &prov)
	if prov["engine"] != "pipeline" || prov["template"] != "video-notes@1" || prov["note_status"] != notes.StatusComplete ||
		prov["checks"].(map[string]any)["passed"] != true || prov["template_sha256"] == "" {
		t.Errorf("provenance = %v", prov)
	}
	// 同樣的輸入與設定：步驟層的快取命中，不再呼叫模型。
	rerunNote(t, r, it, nil)
	if n, _ := fake.count(); n != 1 {
		t.Errorf("快取應命中：%d", n)
	}
	// 匯入新的字幕後筆記標示過時（NOTE-R10）。
	cues := []subtitle.Cue{{Index: 1, StartMS: 5_000, EndMS: 10_000, Text: "Fixed.", Timing: subtitle.TimingSource}}
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantCorrected, Lang: "en", Cues: cues}); err != nil {
		t.Fatal(err)
	}
	if v, err := r.p.ItemNote(ctx, it.ID, ""); err != nil || !v.Stale || v.StaleReason != NoteStaleReason {
		t.Errorf("匯入後應過時：%+v %v", v, err)
	}
	if _, err := r.p.ItemNote(ctx, it.ID, "ja"); !errors.Is(err, ErrNoNote) {
		t.Errorf("沒有日文筆記：%v", err)
	}
}

// NOTE-R3：驗證失敗會帶著錯誤重試；重試用盡時 failed，訊息說明原因、動作是重新產生與開啟 Prompt。
func TestNoteValidationFailure(t *testing.T) {
	bad := `{"sections":[{"level":2,"title":"x","cue":3,"body":"","frames":[]}],"self_check":["q"]}`
	fake := &noteLLM{note: []string{bad}}
	r, it := newNoteRig(t, fake, 600)
	st := rerunNote(t, r, it, nil)
	want := "筆記的內容沒有通過驗證（sections[0].cue 3 不在輸入中）。"
	if st.Status != store.StatusFailed || st.Message != want || strings.Join(st.Actions, ",") != ActionRegenerateNote+","+actionOpenPrompt {
		t.Fatalf("note = %+v", st)
	}
	if n, _ := fake.count(); n < 2 || !strings.Contains(fake.text(1), "sections[0].cue 3 不在輸入中") {
		t.Errorf("重試沒有帶上錯誤：%d", n)
	}
	if _, err := r.p.ItemNote(context.Background(), it.ID, ""); !errors.Is(err, ErrNoNote) {
		t.Errorf("失敗時不寫筆記：%v", err)
	}
	// 第一次失敗、重試成功：done。
	fake2 := &noteLLM{note: []string{bad, `{"sections":[{"level":2,"title":"x","cue":1,"body":"","frames":[]}],"self_check":["q"]}`}}
	r2, it2 := newNoteRig(t, fake2, 600)
	if st := rerunNote(t, r2, it2, nil); st.Status != store.StatusDone {
		t.Errorf("重試成功 = %+v", st)
	}
}

// NOTE-R7、R11：自訂模板多了沒有時間的標題，自動檢查不通過：仍寫出筆記，但步驟 blocked（needs_action）。
// 模板的建立、修改、列出與預覽（NOTE-R12）也在這裡驗證。
func TestNoteTemplatesAndChecks(t *testing.T) {
	fake := &noteLLM{note: []string{goodNote}}
	r, it := newNoteRig(t, fake, 600)
	ctx := context.Background()
	if _, err := r.p.PreviewNoteTemplate(ctx, notes.BuiltinTemplateID, it.ID, ""); !errors.Is(err, ErrNoNote) {
		t.Errorf("沒有筆記時預覽：%v", err)
	}
	seedNoteInputs(t, r, it)
	rerunNote(t, r, it, nil)

	body := "{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n\n## 補充說明\n\n{{.SelfCheck}}\n"
	tpl, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: "extra-heading", Body: &body})
	if err != nil || tpl.Version != 1 || tpl.Builtin || tpl.SHA256 == "" {
		t.Fatalf("create = %+v %v", tpl, err)
	}
	st := rerunNote(t, r, it, noteOver(map[string]any{"template": "extra-heading"}))
	if st.Status != store.StatusBlocked || st.BlockReason != reasonNeedsAction ||
		st.Message != "筆記沒有通過自動檢查：標題沒有時間戳（補充說明）。" ||
		strings.Join(st.Actions, ",") != ActionRegenerateNote+","+actionOpenTemplate || st.ArtifactID == "" {
		t.Fatalf("note = %+v", st)
	}
	v, err := r.p.ItemNote(ctx, it.ID, "")
	if err != nil || notes.Passed(v.Checks) || v.Checks[1].ID != "heading_timestamps" || v.Checks[1].OK {
		t.Fatalf("checks = %+v %v", v.Checks, err)
	}
	art, _ := r.st.Read().Artifact(ctx, st.ArtifactID)
	var prov map[string]any
	_ = json.Unmarshal(art.Provenance, &prov)
	if prov["template"] != "extra-heading@1" || prov["checks"].(map[string]any)["passed"] != false {
		t.Errorf("provenance = %v", prov)
	}

	// 預覽：用現行筆記的 sections 套用內建模板，不呼叫模型、不建立產出。
	before, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, KindNote)
	calls, _ := fake.count()
	pv, err := r.p.PreviewNoteTemplate(ctx, notes.BuiltinTemplateID, "yt:abc123xxxxx", "")
	if err != nil || !notes.Passed(pv.Checks) || strings.Contains(pv.Markdown, "補充說明") || !strings.Contains(pv.Markdown, "Toolchain diagram") {
		t.Fatalf("preview = %+v %v", pv, err)
	}
	after, _ := r.st.Read().CurrentArtifacts(ctx, it.ID, KindNote)
	if n, _ := fake.count(); n != calls || len(after) != len(before) || after[0].ID != before[0].ID {
		t.Errorf("預覽不該呼叫模型或建立產出：calls %d→%d", calls, n)
	}
	if _, err := r.p.PreviewNoteTemplate(ctx, "nope", it.ID, ""); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("找不到模板：%v", err)
	}
	if _, err := r.p.PreviewNoteTemplate(ctx, notes.BuiltinTemplateID, "yt:zzz999xxxxx", ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("找不到影音：%v", err)
	}

	// 修改建立新版本；列出時只列最新版本，內建在前；指定舊版本仍讀得到。
	fixed := "{{.Frontmatter}}\n# {{.Title}}\n\n{{.Sections}}\n\n{{.SelfCheck}}\n"
	name := " Extra "
	v2, err := r.p.UpdateNoteTemplate(ctx, "extra-heading", NoteTemplateInput{Body: &fixed, Name: &name})
	if err != nil || v2.Version != 2 || v2.Body != fixed || v2.Name != "Extra" {
		t.Fatalf("update = %+v %v", v2, err)
	}
	list, err := r.p.NoteTemplates(ctx)
	if err != nil || len(list) != 2 || !list[0].Builtin || list[1].ID != "extra-heading" || list[1].Version != 2 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if old, err := r.p.NoteTemplateByRef(ctx, "extra-heading@1"); err != nil || old.Body != body {
		t.Errorf("舊版本 = %+v %v", old, err)
	}
	for _, ref := range []string{"extra-heading@9", "extra-heading@x", "nope"} {
		if _, err := r.p.NoteTemplateByRef(ctx, ref); !errors.Is(err, ErrTemplateNotFound) {
			t.Errorf("%s：%v", ref, err)
		}
	}
	if b, err := r.p.NoteTemplateByRef(ctx, "video-notes@1"); err != nil || !b.Builtin || b.Body != notes.BuiltinTemplateBody {
		t.Errorf("內建 = %+v %v", b, err)
	}
	st = rerunNote(t, r, it, noteOver(map[string]any{"template": "extra-heading"}))
	if st.Status != store.StatusDone {
		t.Errorf("修正模板後 = %+v", st)
	}
	st = rerunNote(t, r, it, noteOver(map[string]any{"template": "missing"}))
	if st.Status != store.StatusFailed || !strings.Contains(st.Message, "找不到筆記模板「missing」") {
		t.Errorf("找不到模板 = %+v", st)
	}
}

// NOTE-R11：模板的錯誤。
func TestNoteTemplateErrors(t *testing.T) {
	r, _ := newNoteRig(t, &noteLLM{note: []string{goodNote}}, 600)
	ctx := context.Background()
	str := func(s string) *string { return &s }
	fieldErr := func(err error) string {
		var inv *InvalidError
		if !errors.As(err, &inv) || len(inv.Fields) == 0 {
			return "不是 InvalidError：" + errString(err)
		}
		return inv.Fields[0].Path + "：" + inv.Fields[0].Message
	}
	cases := []struct {
		name string
		in   NoteTemplateInput
		want string
	}{
		{"未知的變數", NoteTemplateInput{ID: "leaky", Body: str("{{.Frontmatter}}\n{{.Secret}}\n")}, "body：不認識的變數 .Secret。"},
		{"沒有 body 也沒有 from", NoteTemplateInput{ID: "empty"}, "body：需要 body 或 from。"},
		{"不正確的 id", NoteTemplateInput{ID: "Bad_ID", Body: str("x")}, "id："},
		{"找不到 from", NoteTemplateInput{ID: "copy", From: "nope"}, "from：找不到筆記模板「nope」。"},
		{"不認識的檢查", NoteTemplateInput{ID: "chk", From: "video-notes", Checks: &[]string{"frontmatter", "spelling"}}, "checks[1]："},
		{"按鈕太長", NoteTemplateInput{ID: "btn", From: "video-notes", ReadButton: str(strings.Repeat("a", maxReadButton+1))}, "read_button："},
		{"名稱太長", NoteTemplateInput{ID: "nm", From: "video-notes", Name: str(strings.Repeat("名", 121))}, "name："},
	}
	for _, c := range cases {
		_, err := r.p.CreateNoteTemplate(ctx, c.in)
		if got := fieldErr(err); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s：%s，預期 %s", c.name, got, c.want)
		}
	}
	if _, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: notes.BuiltinTemplateID, Body: str("x")}); !errors.Is(err, ErrBuiltinTemplate) {
		t.Errorf("內建 id：%v", err)
	}
	cp, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: "my-notes", From: "video-notes", Checks: &[]string{}})
	if err != nil || cp.Body != notes.BuiltinTemplateBody || cp.Version != 1 || cp.Checks == nil || len(cp.Checks) != 0 {
		t.Fatalf("複製 = %+v %v", cp, err)
	}
	if _, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: "my-notes", From: "video-notes"}); !errors.Is(err, ErrTemplateExists) {
		t.Errorf("重複的 id：%v", err)
	}
	if _, err := r.p.UpdateNoteTemplate(ctx, notes.BuiltinTemplateID, NoteTemplateInput{Body: str("x")}); !errors.Is(err, ErrBuiltinTemplate) {
		t.Errorf("修改內建：%v", err)
	}
	if _, err := r.p.UpdateNoteTemplate(ctx, "nope", NoteTemplateInput{Body: str("x")}); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("修改不存在的：%v", err)
	}
	if _, err := r.p.UpdateNoteTemplate(ctx, "my-notes@1", NoteTemplateInput{Body: str("x")}); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("不能修改指定版本：%v", err)
	}
	if _, err := r.p.UpdateNoteTemplate(ctx, "my-notes", NoteTemplateInput{Body: str("{{template \"x\"}}")}); !strings.HasPrefix(fieldErr(err), "body：") {
		t.Errorf("修改成不合法的模板：%v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// NOTE-R6：片長超過門檻時 map-reduce：每個章節一段 map（note_map），最後一個 reduce 看到各段的要點；
// 任務種類依序是 note_map 與 note；來源紀錄記下 map 的段數。
func TestNoteMapReduce(t *testing.T) {
	// map 的輸出一律沒有要點（不通過驗證）。
	fake := &noteLLM{note: []string{goodNote}, mapOut: `{"points":[]}`}
	r, it := newNoteRig(t, fake, 600)
	ctx := context.Background()
	if err := r.p.SeedChapters(ctx, it.ID, []SeedChapter{{Title: "A", StartMS: 0, Cue: 1}, {Title: "B", StartMS: 300_000, Cue: 4}}); err != nil {
		t.Fatal(err)
	}
	// map 的輸出不正確：reduce 不送出，步驟 failed。
	st := rerunNote(t, r, it, noteOver(map[string]any{"map_reduce_after": "1m"}))
	if n, m := fake.count(); st.Status != store.StatusFailed || n != 0 || m < 2 {
		t.Fatalf("map 失敗 = %+v，calls %d/%d", st, n, m)
	}
	// 訊息是第一個退回的原因（map 的驗證錯誤）。
	if st.Message != "筆記的內容沒有通過驗證（points 至少要有 1 項）。" || strings.Join(st.Actions, ",") != ActionRegenerateNote+","+actionOpenPrompt {
		t.Errorf("message = %q", st.Message)
	}

	fake2 := &mapLLM{}
	r2, it2 := newNoteRigWith(t, fake2, 600)
	if err := r2.p.SeedChapters(ctx, it2.ID, []SeedChapter{{Title: "A", StartMS: 0, Cue: 1}, {Title: "B", StartMS: 300_000, Cue: 4}}); err != nil {
		t.Fatal(err)
	}
	st = rerunNote(t, r2, it2, noteOver(map[string]any{"map_reduce_after": "1m"}))
	if st.Status != store.StatusDone {
		t.Fatalf("map-reduce = %+v", st)
	}
	if got := fake2.stages(); strings.Join(got, ",") != "note_map,note_map,note" {
		t.Fatalf("請求的順序 = %v", got)
	}
	if d := fake2.data[2]; !strings.Contains(d, "point for 1") || !strings.Contains(d, "point for 4") || strings.Contains(d, "Toolchain setup.") {
		t.Errorf("reduce 的資料應是要點：%s", d)
	}
	// map 並行送出，順序不固定：依 chunk 編號找。
	for _, d := range fake2.data[:2] {
		first := strings.Contains(d, `"chunk":1`)
		if first != strings.Contains(d, "Welcome back.") || first == strings.Contains(d, "The boot loader.") || !strings.Contains(d, `"chunks":2`) {
			t.Errorf("每段 map 只有自己的章節：%s", d)
		}
	}
	art, _ := r2.st.Read().Artifact(ctx, st.ArtifactID)
	var prov map[string]any
	_ = json.Unmarshal(art.Provenance, &prov)
	if mr, _ := prov["map_reduce"].(map[string]any); mr["maps"] != float64(2) {
		t.Errorf("provenance = %v", prov)
	}
	v, _ := r2.p.ItemNote(ctx, it2.ID, "")
	if !strings.Contains(v.Markdown, "[05:00](https://www.youtube.com/watch?v=abc123xxxxx&t=300s)") {
		t.Errorf("reduce 的 cue 引用換成時間：\n%s", v.Markdown)
	}
}

// mapLLM 的 map 回答這一段第一句的要點，reduce 回答固定的筆記。
type mapLLM struct {
	mu    sync.Mutex
	specs []string
	data  []string
}

func (f *mapLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, req.Spec.Stage)
	f.data = append(f.data, req.Data)
	if req.Spec.Stage == llm.StageNoteMap {
		cue := "1"
		if strings.Contains(req.Data, "The boot loader.") {
			cue = "4"
		}
		return llm.Response{Output: json.RawMessage(`{"points":[{"cue":` + cue + `,"text":"point for ` + cue + `"}]}`)}, nil
	}
	return llm.Response{Output: json.RawMessage(`{"sections":[{"level":2,"title":"All","cue":1,"body":"see [[cue:4]]","frames":[]}],"self_check":["q"]}`)}, nil
}

func (f *mapLLM) stages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.specs...)
}

// newNoteRigWith 同 newNoteRig，但用任意的假 provider。
func newNoteRigWith(t *testing.T, c llm.Client, durS int) (*rig, store.Item) {
	t.Helper()
	fake := &noteLLM{note: []string{goodNote}}
	r, it := newNoteRig(t, fake, durS)
	r.p.llm.NewClient = func(llm.ProviderConfig, string) llm.Client { return c }
	return r, it
}

// NOTE-R1、N8：agent 時一般的筆記是 note 任務；map-reduce 的 map 是 note_map，全部提交之後才出現 reduce 的 note 任務。
func TestNoteAgent(t *testing.T) {
	fake := &noteLLM{note: []string{goodNote}}
	r, it := newNoteRig(t, fake, 600)
	ctx := context.Background()
	agent := noteOver(map[string]any{"executor": "agent", "map_reduce_after": "1m"})
	if err := r.p.SeedChapters(ctx, it.ID, []SeedChapter{{Title: "A", StartMS: 0, Cue: 1}, {Title: "B", StartMS: 300_000, Cue: 4}}); err != nil {
		t.Fatal(err)
	}
	if st := rerunNote(t, r, it, agent); st.Status != store.StatusAwaitingAgent {
		t.Fatalf("note = %+v", st)
	}
	if got, _ := r.p.ClaimAgentTasks(ctx, "a1", []string{StageNote}, 0); len(got) != 0 {
		t.Fatalf("map 還沒完成時沒有 note 任務：%+v", got)
	}
	maps, err := r.p.ClaimAgentTasks(ctx, "a1", []string{TaskNoteMap}, 0)
	if err != nil || len(maps) != 2 || maps[0].Kind != TaskNoteMap {
		t.Fatalf("note_map = %+v %v", maps, err)
	}
	for _, task := range maps {
		cue := "1"
		if strings.Contains(task.Prompt.User, "The boot loader.") {
			cue = "4"
		}
		out := json.RawMessage(`{"points":[{"cue":` + cue + `,"text":"agent point ` + cue + `"}]}`)
		if err := r.p.SubmitAgentTask(ctx, "a1", task.TaskID, task.LeaseToken, AgentSubmission{Output: out}); err != nil {
			t.Fatal(err)
		}
	}
	r.run()
	reduce, err := r.p.ClaimAgentTasks(ctx, "a1", []string{StageNote}, 0)
	if err != nil || len(reduce) != 1 || reduce[0].Kind != StageNote || !strings.Contains(reduce[0].Prompt.User, "agent point 4") {
		t.Fatalf("reduce = %+v %v", reduce, err)
	}
	out := json.RawMessage(`{"sections":[{"level":2,"title":"All","cue":1,"body":"","frames":[]}],"self_check":["q"]}`)
	if err := r.p.SubmitAgentTask(ctx, "a1", reduce[0].TaskID, reduce[0].LeaseToken, AgentSubmission{Output: out}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if st := latestStage(t, r, it.ID, StageNote); st.Status != store.StatusDone || st.Executor != ExecutorAgent {
		t.Fatalf("note = %+v", st)
	}
	if n, m := fake.count(); n+m != 0 {
		t.Errorf("agent 不呼叫 provider：%d/%d", n, m)
	}
}

// NOTE-R2、R5：筆記語言、只在要求時附上譯文；SponsorBlock 查詢失敗的結果讓筆記標成 partial。
func TestNoteLangTranslationAndPartial(t *testing.T) {
	fake := &noteLLM{note: []string{`{"sections":[{"level":2,"title":"x","cue":1,"body":"","frames":[]}],"self_check":["q"]}`}}
	r, it := newNoteRig(t, fake, 600)
	ctx := context.Background()
	tr := []subtitle.Cue{
		{Index: 1, StartMS: 5_000, EndMS: 10_000, Text: "歡迎回來。", Timing: subtitle.TimingSource},
		{Index: 2, StartMS: 20_000, EndMS: 25_000, Text: "設定工具鏈。", Timing: subtitle.TimingSource},
		{Index: 3, StartMS: 180_000, EndMS: 185_000, Text: "業配。", Timing: subtitle.TimingSource},
		{Index: 4, StartMS: 300_000, EndMS: 305_000, Text: "開機載入器。", Timing: subtitle.TimingSource},
	}
	if err := r.p.SeedTranscript(ctx, SeedRequest{ItemID: it.ID, Variant: subtitle.VariantTranslated, Lang: "zh-Hant-TW", Cues: tr}); err != nil {
		t.Fatal(err)
	}
	st := rerunNote(t, r, it, noteOver(map[string]any{"lang": "zh-Hant-TW"}))
	if st.Status != store.StatusDone || strings.Contains(fake.text(0), "設定工具鏈。") || !strings.Contains(fake.text(0), "語言 zh-Hant-TW") {
		t.Fatalf("沒有要求時不附譯文：%+v\n%s", st, fake.text(0))
	}
	rerunNote(t, r, it, noteOver(map[string]any{"lang": "zh-Hant-TW", "include_translation": true}))
	last := fake.text(len(fake.reqs) - 1)
	if !strings.Contains(last, "設定工具鏈。") || strings.Contains(last, "業配。") {
		t.Errorf("要求時附上譯文（不含片段內的句子）：%s", last)
	}
	v, err := r.p.ItemNote(ctx, it.ID, "zh-hant-tw")
	if err != nil || v.Lang != "zh-Hant-TW" {
		t.Fatalf("view = %+v %v", v, err)
	}
	if v.Frontmatter["notesteme_status"] != notes.StatusComplete {
		t.Errorf("status = %q", v.Frontmatter["notesteme_status"])
	}
	// SponsorBlock 查詢失敗：步驟 done 但有訊息，筆記標成 partial。
	r.p.sponsor = &fakeSponsor{res: sponsorblock.Result{Outcome: sponsorblock.OutcomeError, Reason: "HTTP 503", Segments: []sponsorblock.Segment{}}}
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageSponsorBlock, Until: StageNote, Overrides: noteOver(nil)}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if sb := latestStage(t, r, it.ID, StageSponsorBlock); sb.Status != store.StatusDone || sb.Message == "" {
		t.Fatalf("SponsorBlock 失敗應是 done 並留下訊息：%+v", sb)
	}
	v, _ = r.p.ItemNote(ctx, it.ID, "")
	if v.Frontmatter["notesteme_status"] != notes.StatusPartial {
		t.Errorf("降級的結果應是 partial：%v", v.Frontmatter)
	}
}

// 純函式的邊界（NOTE-R5、R6、N13）。
func TestNoteHelpers(t *testing.T) {
	eff := mustEffective(t, map[string]any{"language": map[string]any{"targets": []any{"ja"}}})
	if got := noteLangOf(eff, noteSettings{Lang: "fr"}, "en"); got != "fr" {
		t.Errorf("stages.note.lang 優先：%s", got)
	}
	if got := noteLangOf(eff, noteSettings{}, "en"); got != "ja" {
		t.Errorf("其次是第一個目標語言：%s", got)
	}
	if got := noteLangOf(Effective{}, noteSettings{}, "en"); got != "en" {
		t.Errorf("最後是原文語言：%s", got)
	}
	for in, want := range map[string]time.Duration{"": defaultMapReduceAfter, "90m": 90 * time.Minute, "1h30m": 90 * time.Minute} {
		if got, err := parseNoteDuration(in); err != nil || got != want {
			t.Errorf("%q = %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"0s", "-5m", "soon"} {
		if _, err := parseNoteDuration(bad); err == nil {
			t.Errorf("%q 應該不正確", bad)
		}
	}
	tr := subtitle.Transcript{Cues: []subtitle.Cue{{StartMS: 1000}, {StartMS: 5000}, {StartMS: 9000}, {StartMS: 12000}}}
	set := map[int]bool{1: true, 2: true, 4: true}
	for ms, want := range map[int64]int{0: 1, 1000: 1, 4999: 1, 5000: 2, 9500: 2, 99_000: 4} {
		if got := cueAtOrBefore(tr, set, ms); got != want {
			t.Errorf("cueAtOrBefore(%d) = %d，預期 %d", ms, got, want)
		}
	}
	if got := cueAtOrBefore(tr, map[int]bool{}, 5000); got != 0 {
		t.Errorf("沒有可用的句子：%d", got)
	}
	// 沒有章節時每 20 分鐘一段，沒有句子的視窗略過。
	job := &llmJob{tr: subtitle.Transcript{Cues: []subtitle.Cue{{StartMS: 0}, {StartMS: 60_000}, {StartMS: 50 * 60_000}, {StartMS: 70 * 60_000}}}}
	for i := range job.tr.Cues {
		job.cues = append(job.cues, llm.Cue{I: i + 1})
	}
	got := (&Pipeline{}).noteMapChunks(job, &llm.Metadata{})
	want := []llm.Chunk{{Index: 1, From: 1, To: 2}, {Index: 2, From: 3, To: 3}, {Index: 3, From: 4, To: 4}}
	if len(got) != len(want) {
		t.Fatalf("chunks = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v，預期 %+v", i, got[i], want[i])
		}
	}
	// 有章節時每章一段，第一章從 0 開始。
	got = (&Pipeline{}).noteMapChunks(job, &llm.Metadata{NoteChapters: []llm.NoteChapter{{Cue: 2}, {Cue: 4}}})
	if len(got) != 2 || got[0] != (llm.Chunk{Index: 1, From: 1, To: 3}) || got[1] != (llm.Chunk{Index: 2, From: 4, To: 4}) {
		t.Errorf("依章節 = %+v", got)
	}
	for platform, want := range map[string]string{"youtube": "YouTube", "bilibili": "Bilibili", "podcast": "Podcast", "local": ""} {
		if got := platformLabel(platform); got != want {
			t.Errorf("platformLabel(%s) = %q", platform, got)
		}
	}
	if got := noteFailureMessage(nil); got != "筆記的內容沒有通過驗證（格式不正確）。" {
		t.Errorf("沒有紀錄：%s", got)
	}
	if got := noteFailureMessage([]fallbackRecord{{Reason: "schema"}}); got != "筆記的內容沒有通過驗證（schema）。" {
		t.Errorf("只有原因：%s", got)
	}
	// 本機檔案沒有網址：source 用影音代號，時間是純文字（N13）。
	in := (&Pipeline{}).noteInputFor(store.Item{ID: "i1", Platform: "local", ExternalID: "abcd", Title: "Local", Metadata: json.RawMessage(`broken`)},
		"en", notes.StatusComplete, subtitle.Transcript{Cues: []subtitle.Cue{{StartMS: 61_000}}}, map[int]bool{1: true, 7: true}, nil, nil)
	if in.Front.Source != "local:abcd" || in.CueStart[1] != 61_000 || len(in.CueStart) != 1 || in.Channel != "" {
		t.Errorf("input = %+v", in)
	}
	if _, err := seedJPEG(4, 4, "#12345"); err == nil {
		t.Error("顏色長度不對應該失敗")
	}
	if _, err := seedJPEG(4, 4, "#zzzzzz"); err == nil {
		t.Error("顏色不是十六進位應該失敗")
	}
	if b, err := seedJPEG(4, 4, ""); err != nil || len(b) == 0 {
		t.Errorf("灰色：%v", err)
	}
	// noteSponsor 只在實際排除句子時才算 parent。
	sb := &store.Artifact{ID: "sb"}
	if noteSponsor(&llmJob{sponsor: sb, cues: []llm.Cue{{I: 1}}, tr: subtitle.Transcript{Cues: []subtitle.Cue{{}}}}) != nil {
		t.Error("沒有排除句子時不列 sponsor")
	}
	if noteSponsor(&llmJob{sponsor: sb, cues: []llm.Cue{{I: 1}}, tr: subtitle.Transcript{Cues: []subtitle.Cue{{}, {}}}}) != sb {
		t.Error("排除句子時列 sponsor")
	}
}

// NOTE-R1：每一句都在片段內時沒有輸入，步驟以 permanent 失敗並說明原因。
func TestNoteAllCuesInSponsor(t *testing.T) {
	fake := &noteLLM{note: []string{goodNote}}
	r, it := newNoteRig(t, fake, 600)
	r.p.sponsor = sponsorAt(0, 600_000)
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageSponsorBlock, Until: StageNote, Overrides: noteOver(nil)}); err != nil {
		t.Fatal(err)
	}
	r.run()
	st := latestStage(t, r, it.ID, StageNote)
	if st.Status != store.StatusFailed || !strings.Contains(st.Message, "SponsorBlock 片段內") {
		t.Fatalf("note = %+v", st)
	}
	if n, m := fake.count(); n+m != 0 {
		t.Errorf("沒有輸入時不呼叫模型：%d/%d", n, m)
	}
}

// SeedChapters、SeedFrames 寫入現行的產出：平台章節（沒有句號）是 platform，畫面有圖片與縮圖。
func TestSeedChaptersAndFrames(t *testing.T) {
	r, it := newNoteRig(t, &noteLLM{note: []string{goodNote}}, 600)
	ctx := context.Background()
	if err := r.p.SeedChapters(ctx, it.ID, []SeedChapter{{Title: "B", StartMS: 300_000}, {Title: "A", StartMS: 0}}); err != nil {
		t.Fatal(err)
	}
	doc, ok, err := r.p.ItemChapters(ctx, it.ID)
	if err != nil || !ok || doc.Origin != ChapterOriginPlatform || doc.Chapters[0].Title != "A" || doc.Chapters[1].EndMS != 600_000 {
		t.Fatalf("chapters = %+v %v %v", doc, ok, err)
	}
	if err := r.p.SeedChapters(ctx, "nope", nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("沒有影音：%v", err)
	}
	if err := r.p.SeedFrames(ctx, it.ID, []SeedFrame{{AtMS: 1000, Selected: true, Caption: "c"}, {AtMS: 2000}}); err != nil {
		t.Fatal(err)
	}
	v, err := r.p.Frames(ctx, it.ID)
	if err != nil || len(v.Frames) != 2 || !v.Frames[0].Selected || v.Frames[0].Caption != "c" || v.Frames[1].Selected || v.Target != 1 {
		t.Fatalf("frames = %+v %v", v, err)
	}
	f, _, _, err := r.p.FrameImage(ctx, it.ID, 1, true)
	if err != nil {
		t.Fatalf("縮圖：%v", err)
	}
	_ = f.Close()
	if err := r.p.SeedFrames(ctx, it.ID, []SeedFrame{{Color: "red"}}); err == nil {
		t.Error("不正確的顏色應該失敗")
	}
	if err := r.p.SeedFrames(ctx, "nope", nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("沒有影音：%v", err)
	}
}

// ItemNote 的錯誤：沒有影音、垃圾桶中的影音、產出檔被改動。
func TestItemNoteErrors(t *testing.T) {
	fake := &noteLLM{note: []string{`{"sections":[{"level":2,"title":"x","cue":1,"body":"","frames":[]}],"self_check":["q"]}`}}
	r, it := newNoteRig(t, fake, 600)
	ctx := context.Background()
	if _, err := r.p.ItemNote(ctx, "nope", ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("沒有影音：%v", err)
	}
	if _, err := r.p.ItemNote(ctx, it.ID, ""); !errors.Is(err, ErrNoNote) {
		t.Errorf("沒有筆記：%v", err)
	}
	st := rerunNote(t, r, it, nil)
	art, err := r.st.Read().Artifact(ctx, st.ArtifactID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, filepath.FromSlash(art.Path)), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.ItemNote(ctx, it.ID, ""); !errors.Is(err, ErrCorruptArtifact) {
		t.Errorf("產出檔被改動：%v", err)
	}
	r.trashDirect(it.ID, r.clk.Now())
	if _, err := r.p.ItemNote(ctx, it.ID, ""); !errors.Is(err, ErrInTrash) {
		t.Errorf("垃圾桶中：%v", err)
	}
	if _, err := r.p.PreviewNoteTemplate(ctx, notes.BuiltinTemplateID, it.ID, ""); !errors.Is(err, ErrInTrash) {
		t.Errorf("預覽垃圾桶中的影音：%v", err)
	}
}

// shaPrompts 同 notePrompts，但 note-map-default 的內文與 sha256 可以改（模擬內建 prompt 改版）。
type shaPrompts struct{ mapSHA string }

func (s shaPrompts) ResolvePrompt(ctx context.Context, ref string) (ResolvedPrompt, error) {
	rp, err := notePrompts{}.ResolvePrompt(ctx, ref)
	if rp.Kind == TaskNoteMap {
		rp.SHA256, rp.Body = s.mapSHA, "列出要點（"+s.mapSHA+"）。"
	}
	return rp, err
}

// 步驟層的快取鍵包含模板內文與 map 的 prompt：模板改版後重新套用（模型的輸出沿用 chunk 快取），
// map 的 prompt 改版後重新送出 map。
func TestNoteCacheFollowsTemplateAndMapPrompt(t *testing.T) {
	fake := &mapLLM{}
	r, it := newNoteRigWith(t, fake, 600)
	ctx := context.Background()
	body := "{{.Frontmatter}}\n# {{.Title}}\n\n版本一\n\n{{.Sections}}\n\n{{.SelfCheck}}\n"
	if _, err := r.p.CreateNoteTemplate(ctx, NoteTemplateInput{ID: "mine", Body: &body}); err != nil {
		t.Fatal(err)
	}
	over := noteOver(map[string]any{"template": "mine"})
	rerunNote(t, r, it, over)
	body2 := strings.Replace(body, "版本一", "版本二", 1)
	if _, err := r.p.UpdateNoteTemplate(ctx, "mine", NoteTemplateInput{Body: &body2}); err != nil {
		t.Fatal(err)
	}
	st := rerunNote(t, r, it, over)
	if v, _ := r.p.ItemNote(ctx, it.ID, ""); st.Status != store.StatusDone || !strings.Contains(v.Markdown, "版本二") {
		t.Fatalf("模板改版後應重新套用：%+v\n%s", st, v.Markdown)
	}

	mr := noteOver(map[string]any{"map_reduce_after": "1m"})
	if err := r.p.SeedChapters(ctx, it.ID, []SeedChapter{{Title: "A", StartMS: 0, Cue: 1}, {Title: "B", StartMS: 300_000, Cue: 4}}); err != nil {
		t.Fatal(err)
	}
	r.p.prompts = shaPrompts{mapSHA: "sha-map-1"}
	rerunNote(t, r, it, mr)
	maps := func() int {
		n := 0
		for _, s := range fake.stages() {
			if s == llm.StageNoteMap {
				n++
			}
		}
		return n
	}
	if maps() != 2 {
		t.Fatalf("map = %d", maps())
	}
	rerunNote(t, r, it, mr)
	if maps() != 2 {
		t.Errorf("prompt 相同時快取命中：%d", maps())
	}
	r.p.prompts = shaPrompts{mapSHA: "sha-map-2"}
	rerunNote(t, r, it, mr)
	if maps() != 4 {
		t.Errorf("map 的 prompt 改版後應重新送出：%d", maps())
	}
}
