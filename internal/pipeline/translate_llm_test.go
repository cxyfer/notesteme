package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/worker"
)

// trnPrompts 是 PromptResolver 的替身：每個引用都解析成 translate 類型的第 1 版。
type trnPrompts struct{}

func (trnPrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	id, _ := splitPromptRef(ref)
	return ResolvedPrompt{ID: id, Kind: StageTranslate, Version: 1, SHA256: "sha-v1", Body: "翻譯成 {{target_lang}}。{{title}}"}, nil
}

// trnReq 是假 provider 收到的一個請求。
type trnReq struct {
	prefix string // 固定前綴（<video_metadata> 在這裡）
	target string
	chunk  int
	cues   []llm.Cue
	retry  string
}

// trnLLM 是 translate 的假 provider：answer 依請求產生每一句的譯文（nil 代表用預設的 echo）。
type trnLLM struct {
	mu     sync.Mutex
	reqs   []trnReq
	answer func(n int, r trnReq) []llm.Cue
}

var targetRE = regexp.MustCompile(`目標語言：([A-Za-z0-9-]*)。`)

func (f *trnLLM) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	var data struct {
		Chunk int       `json:"chunk"`
		Cues  []llm.Cue `json:"cues"`
	}
	raw := req.Data[len(llm.DataOpen) : len(req.Data)-len(llm.DataClose)]
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return llm.Response{}, err
	}
	r := trnReq{chunk: data.Chunk, cues: data.Cues, retry: req.Retry, prefix: req.Fixed.Prefix}
	if m := targetRE.FindStringSubmatch(req.Fixed.System); m != nil {
		r.target = m[1]
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	n, answer := len(f.reqs), f.answer
	f.mu.Unlock()
	var out []llm.Cue
	if answer != nil {
		out = answer(n, r)
	} else {
		out = echoTarget(r)
	}
	b, _ := json.Marshal(map[string]any{"cues": out})
	return llm.Response{Output: b}, nil
}

func (f *trnLLM) requests() []trnReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// echoTarget 為每個單位回答目標語言的譯文（句群的每一段各一句），文字帶著 chunk 與句號。
func echoTarget(r trnReq) []llm.Cue {
	word := "這是翻譯好的句子內容"
	if r.target == "ja" {
		word = "これは翻訳された文です"
	}
	out := make([]llm.Cue, len(r.cues))
	for k, c := range r.cues {
		n := strings.Count(c.Text, llm.SplitMarker) + 1
		parts := make([]string, n)
		for j := range parts {
			parts[j] = fmt.Sprintf("%s，第%d段第%d句。", word, r.chunk, c.I+j)
		}
		out[k] = llm.Cue{I: c.I, Text: strings.Join(parts, llm.SplitMarker)}
	}
	return out
}

// newTrnRig 建立 translate 走 api 的 rig，影音已有 corrected（en）。
func newTrnRig(t *testing.T, fake *trnLLM, chunk map[string]any, texts ...string) (*rig, store.Item) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	yt := &fakeYT{cues: 4, info: youtube.Info{ID: "abc123xxxxx", Title: "T", Language: "en", ChannelID: "UC1", Channel: "ch"}}
	q := queue.New(st, clk, "w1")
	q.Control().SetCap(queue.ResourceLLM, 1)
	reg := llm.NewRegistry(nil, clk)
	reg.Env = map[string]string{llm.KeyEnvVar("anthropic"): "k"}
	reg.NewClient = func(llm.ProviderConfig, string) llm.Client { return fake }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := New(Deps{Store: st, Clock: clk, DataDir: dir, Log: log, YouTube: yt, Wake: q.Wake, Version: "test", Prompts: trnPrompts{}, LLM: reg})
	r := &rig{t: t, p: p, st: st, clk: clk, w: worker.New(q, p, clk, log), yt: yt, dir: dir}
	it := r.ingest(watch, trnOver(chunk), "normalize")
	r.run()
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", texts...)
	return r, it
}

func trnOver(chunk map[string]any) map[string]any {
	l := map[string]any{"max_validation_retries": 1}
	if chunk != nil {
		l["chunk"] = chunk
	}
	return map[string]any{
		"stages": map[string]any{"translate": map[string]any{"executor": "api", "provider": "anthropic", "model": "m", "prompt": "translate-default"}},
		"llm":    l,
	}
}

func rerunTrn(t *testing.T, r *rig, it store.Item, targets ...string) {
	t.Helper()
	over := withMap(langOver(targets, ""), trnOver(nil))
	delete(over, "llm")
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranslate, Overrides: over}); err != nil {
		t.Fatalf("Rerun：%v", err)
	}
	r.run()
}

// rerunTrnChunks 同 rerunTrn，但只重新送出指定的 chunk（全域編號）。
func rerunTrnChunks(t *testing.T, r *rig, it store.Item, chunks []int, targets ...string) {
	t.Helper()
	over := withMap(langOver(targets, ""), trnOver(nil))
	delete(over, "llm")
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageTranslate, Overrides: over, Chunks: chunks}); err != nil {
		t.Fatalf("Rerun：%v", err)
	}
	r.run()
}

func cueTexts(tr subtitle.Transcript) []string {
	out := make([]string, len(tr.Cues))
	for i, c := range tr.Cues {
		out[i] = c.Text
	}
	return out
}

// TRN-R4：標記數不符的句群改成逐 cue 翻譯，剛好多送一次請求；補送不是驗證重試（沒有 retry note）。
func TestTranslateMissingMarkerFallsBackToPerCue(t *testing.T) {
	fake := &trnLLM{answer: func(n int, r trnReq) []llm.Cue {
		if n == 1 {
			return []llm.Cue{{I: 1, Text: "我們之所以改用 Kafka，是因為舊的佇列跟不上。"}}
		}
		return []llm.Cue{{I: 1, Text: "我們當初之所以決定改用 Kafka，"}, {I: 2, Text: "是因為舊的佇列已經跟不上了。"}}
	}}
	r, it := newTrnRig(t, fake, nil, "The reason we moved to Kafka was that", "our old queue could not keep up.")
	rerunTrn(t, r, it, "zh-Hant-TW")
	reqs := fake.requests()
	if len(reqs) != 2 {
		t.Fatalf("請求數 = %d，預期 2：%+v", len(reqs), reqs)
	}
	if len(reqs[0].cues) != 1 || !strings.Contains(reqs[0].cues[0].Text, llm.SplitMarker) {
		t.Errorf("第一個請求是含標記的句群：%+v", reqs[0].cues)
	}
	if len(reqs[1].cues) != 2 || reqs[1].cues[1].I != 2 || reqs[1].retry != "" {
		t.Errorf("第二個請求逐 cue、不帶 retry note：%+v", reqs[1])
	}
	_, tr := translated(t, r, it.ID, "zh-Hant-TW")
	if got := cueTexts(tr); !slices.Equal(got, []string{"我們當初之所以決定改用 Kafka，", "是因為舊的佇列已經跟不上了。"}) {
		t.Errorf("譯文 = %q", got)
	}
}

// TRN-R7：重試用盡時只有沒通過驗證的那一句退回原文，另一句保留譯文。
func TestTranslatePartialFallback(t *testing.T) {
	fake := &trnLLM{answer: func(_ int, r trnReq) []llm.Cue {
		return []llm.Cue{{I: 1, Text: "我們把事件儲存在日誌裡。"}, {I: 2, Text: ""}}
	}}
	r, it := newTrnRig(t, fake, nil, "We store events in a log.", "Then we read them back.")
	rerunTrn(t, r, it, "zh-Hant-TW")
	if n := len(fake.requests()); n != 2 {
		t.Fatalf("請求數 = %d，預期 1 + 1 次重試", n)
	}
	if !strings.Contains(fake.requests()[1].retry, "cue 2: empty") {
		t.Errorf("retry note = %q", fake.requests()[1].retry)
	}
	art, tr := translated(t, r, it.ID, "zh-Hant-TW")
	if got := cueTexts(tr); !slices.Equal(got, []string{"我們把事件儲存在日誌裡。", "Then we read them back."}) {
		t.Errorf("譯文 = %q", got)
	}
	var prov struct {
		Fallbacks []trnRecord `json:"fallbacks"`
		CacheKey  string      `json:"cache_key"`
	}
	_ = json.Unmarshal(art.Provenance, &prov)
	if len(prov.Fallbacks) != 1 || prov.Fallbacks[0].Cue != 2 || prov.Fallbacks[0].Reason != llm.ReasonEmpty || prov.CacheKey != "" {
		t.Errorf("來源紀錄 = %+v", prov)
	}
}

// 多個目標、每個目標多個 chunk：chunk 依目標連續編號，重疊區以前一個 chunk 為準，再跑一次全部命中快取。
func TestTranslateTargetsShareChunkNumbering(t *testing.T) {
	fake := &trnLLM{}
	r, it := newTrnRig(t, fake, map[string]any{"max_tokens": 1, "overlap_cues": 1, "context_cues": 0}, "This is the first sentence.", "This is the second sentence.", "This is the third sentence.")
	rerunTrn(t, r, it, "zh-Hant-TW", "ja")
	run := latestStage(t, r, it.ID, StageTranslate)
	if run.Status != store.StatusDone {
		t.Fatalf("translate = %+v", run)
	}
	rows, err := r.st.Read().LLMChunks(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 每個目標的 chunk：最後一句由哪個 chunk 輸出（重疊區以前一個 chunk 為準）。
	byTarget := map[string][]trnChunkPlan{}
	for k, row := range rows {
		if row.Chunk != k+1 {
			t.Fatalf("第 %d 列的 chunk 是 %d：全域編號要連續", k+1, row.Chunk)
		}
		var plan trnChunkPlan
		if err := json.Unmarshal(row.Plan, &plan); err != nil {
			t.Fatal(err)
		}
		lang := plan.Targets[plan.Target].Lang
		if want := plan.Targets[plan.Target].Offset + plan.Index; want != row.Chunk {
			t.Errorf("chunk %d 的 offset+index = %d", row.Chunk, want)
		}
		byTarget[lang] = append(byTarget[lang], plan)
	}
	if len(byTarget["zh-Hant-TW"]) < 2 || len(byTarget["ja"]) != len(byTarget["zh-Hant-TW"]) {
		t.Fatalf("每個目標要切成相同的多個 chunk：%v", byTarget)
	}
	for lang, plans := range byTarget {
		_, tr := translated(t, r, it.ID, lang)
		for k := range tr.Cues {
			owner := 0
			for _, pl := range plans {
				if k+1 >= pl.From && k+1 <= pl.To {
					owner = pl.Index
					break
				}
			}
			if want := fmt.Sprintf("第%d段第%d句。", owner, k+1); !strings.HasSuffix(tr.Cues[k].Text, want) {
				t.Errorf("%s 第 %d 句 = %q，預期由 chunk %d 輸出", lang, k+1, tr.Cues[k].Text, owner)
			}
		}
	}
	n := len(fake.requests())
	if n != len(rows) {
		t.Errorf("請求數 = %d，chunk 數 = %d", n, len(rows))
	}
	for _, rq := range fake.requests() {
		if rq.target != "zh-Hant-TW" && rq.target != "ja" {
			t.Errorf("請求的目標語言 = %q", rq.target)
		}
	}
	rerunTrn(t, r, it, "zh-Hant-TW", "ja")
	if got := len(fake.requests()); got != n {
		t.Errorf("相同設定重跑不再呼叫 provider：%d → %d", n, got)
	}
	if v, _ := r.st.Read().ArtifactVersions(context.Background(), it.ID, "transcript", "translated", "ja"); v != 1 {
		t.Errorf("沿用現行譯文，不建立新版本：ja 有 %d 版", v)
	}
}

// S6b-Q3：translate 的 chunk 是各目標的全域編號，被沿用的目標不產生 chunk，編號有空洞。
// POST /rerun 的 chunks 要以實際存在的編號檢查：較大的合法編號不被「列數」擋下，空洞的與超出的編號 422。
func TestTranslateRerunChunksWithHoles(t *testing.T) {
	fake := &trnLLM{}
	r, it := newTrnRig(t, fake, map[string]any{"max_tokens": 1, "overlap_cues": 1, "context_cues": 0}, "This is the first sentence.", "This is the second sentence.", "This is the third sentence.")
	ctx := context.Background()
	rerunTrn(t, r, it, "zh-Hant-TW")
	first := latestStage(t, r, it.ID, StageTranslate)
	zhRows, err := r.st.Read().LLMChunks(ctx, first.ID)
	if err != nil || len(zhRows) < 2 {
		t.Fatalf("zh 要切成多個 chunk：%d 列，%v", len(zhRows), err)
	}
	n := len(zhRows)
	// zh 被沿用（現行譯文的快取鍵相同）、ja 是新目標：ja 的編號接在 zh 之後（n+1..2n），這次的 stage run 只有 ja 的列。
	rerunTrn(t, r, it, "zh-Hant-TW", "ja")
	run := latestStage(t, r, it.ID, StageTranslate)
	existing, err := r.st.Read().LatestLLMChunkNumbers(ctx, it.ID, StageTranslate)
	if err != nil {
		t.Fatal(err)
	}
	var want []int
	for g := n + 1; g <= 2*n; g++ {
		want = append(want, g)
	}
	if !slices.Equal(existing, want) {
		t.Fatalf("沿用 zh 之後現有的 chunk 編號 = %v，預期 %v（列數 %d 小於最大編號 %d）", existing, want, len(existing), 2*n)
	}
	reqs := len(fake.requests())
	rowsBefore, _ := r.st.Read().LLMChunks(ctx, run.ID)

	// 空洞的編號（zh 的 1..n）與超出的編號：422，指出是哪個編號，什麼都不寫、不送請求。
	for _, bad := range []int{1, n, 2*n + 1} {
		var inv *InvalidError
		_, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageTranslate, Chunks: []int{2 * n, bad}})
		if !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != "chunks[1]" ||
			!strings.HasPrefix(inv.Fields[0].Message, fmt.Sprintf("chunk %d 不存在於現行的切分", bad)) {
			t.Errorf("chunks [%d, %d]：err = %v，預期只有 chunks[1] 不存在", 2*n, bad, err)
		}
	}
	if latest := latestStage(t, r, it.ID, StageTranslate); latest.ID != run.ID || len(fake.requests()) != reqs {
		t.Errorf("422 之後不該有新的 stage run 或請求：run %s → %s、請求 %d → %d", run.ID, latest.ID, reqs, len(fake.requests()))
	}

	// 最大編號是合法的（舊的檢查以列數 n 為上限，會把它擋下）；只重新送出這一個 chunk。
	rerunTrnChunks(t, r, it, []int{2 * n}, "zh-Hant-TW", "ja")
	sent := fake.requests()[reqs:]
	if len(sent) != 1 || sent[0].target != "ja" || sent[0].chunk != n {
		t.Errorf("chunks [%d] 只重新送出 ja 的第 %d 個 chunk：%+v", 2*n, n, sent)
	}
	rowsAfter, _ := r.st.Read().LLMChunks(ctx, latestStage(t, r, it.ID, StageTranslate).ID)
	if len(rowsAfter) <= len(rowsBefore) {
		t.Errorf("指定 chunks 的重跑不沿用目標，列數應增加：%d → %d", len(rowsBefore), len(rowsAfter))
	}
}

// 以 edited 為輸入：由現行輸入衍生的 edited 會被 translate 採用（來源紀錄指向它）；correct 的輸入選擇不採用 edited；
// 由舊的 corrected 衍生的 edited 不採用。
func TestTranslateUsesEditedDerivedFromInput(t *testing.T) {
	fake := &trnLLM{}
	r, it := newTrnRig(t, fake, nil, "We store events in a log.")
	ctx := context.Background()
	res, err := r.p.ImportSubtitle(ctx, ImportRequest{ItemID: it.ID, Lang: "en", Data: []byte("1\n00:00:00,000 --> 00:00:02,000\nWe keep events in a log.\n")})
	if err != nil {
		t.Fatal(err)
	}
	src, ok, err := translateSource(ctx, r.st.Read(), it.ID, "")
	if err != nil || !ok || src.ID != res.Artifact.ID {
		t.Fatalf("translateSource = %+v, %v, %v；預期 edited %s", src, ok, err, res.Artifact.ID)
	}
	if in, _, _ := translateInputFor(ctx, r.st.Read(), it.ID, StageCorrect); in.Variant == string(subtitle.VariantEdited) {
		t.Errorf("correct 的輸入不可以是 edited：%+v", in)
	}
	rerunTrn(t, r, it, "zh-Hant-TW")
	art, _ := translated(t, r, it.ID, "zh-Hant-TW")
	var prov struct {
		DerivedFrom []map[string]string `json:"derived_from"`
	}
	_ = json.Unmarshal(art.Provenance, &prov)
	if len(prov.DerivedFrom) != 1 || prov.DerivedFrom[0]["artifact"] != res.Artifact.ID || prov.DerivedFrom[0]["variant"] != "edited" {
		t.Errorf("derived_from = %+v", prov.DerivedFrom)
	}
	if !strings.Contains(fake.requests()[0].cues[0].Text, "keep") {
		t.Errorf("送出的是 edited 的文字：%+v", fake.requests()[0].cues)
	}

	// 新的 corrected：edited 衍生自舊版，不再採用。
	seedVariant(t, r, it, subtitle.VariantCorrected, "en", "We write events to a log.")
	src, _, _ = translateSource(ctx, r.st.Read(), it.ID, "")
	if src.Variant != string(subtitle.VariantCorrected) {
		t.Errorf("衍生自舊 corrected 的 edited 不採用：%+v", src)
	}
}

func TestAlignByOverlap(t *testing.T) {
	cue := func(s, e int64, text string) subtitle.Cue { return subtitle.Cue{StartMS: s, EndMS: e, Text: text} }
	orig := []subtitle.Cue{cue(0, 2000, "a"), cue(2000, 4000, "b"), cue(4000, 6000, "c")}
	tgt := []subtitle.Cue{cue(100, 1900, "甲"), cue(1800, 3900, "乙"), cue(2500, 3000, "丙"), cue(9000, 9500, "丁")}
	if got := alignByOverlap(orig, tgt, ""); !slices.Equal(got, []string{"甲", "乙丙", ""}) {
		t.Errorf("alignByOverlap = %q", got)
	}
	if got := alignByOverlap(orig, []subtitle.Cue{cue(0, 6000, "all"), cue(3000, 3000, "zero")}, " "); !slices.Equal(got, []string{"all", "", ""}) {
		t.Errorf("跨越多句時歸到最早的最大重疊：%q", got)
	}
}

func TestTrackLangMatches(t *testing.T) {
	cases := []struct {
		track, target string
		want          bool
	}{
		{"zh-Hant-TW", "zh-Hant-TW", true},
		{"zh-TW", "zh-Hant-TW", true},
		{"zh-Hant", "zh-Hant-TW", false},
		{"zh-HK", "zh-Hant-TW", false},
		{"en-orig", "en", true},
		{"ja", "zh-Hant-TW", false},
	}
	for _, c := range cases {
		if got := trackLangMatches(c.track, c.target); got != c.want {
			t.Errorf("trackLangMatches(%q, %q) = %v", c.track, c.target, got)
		}
	}
}

func FuzzAlignByOverlap(f *testing.F) {
	f.Add([]byte{0, 10, 10, 20}, []byte{5, 15, 0, 255})
	f.Fuzz(func(t *testing.T, a, b []byte) {
		var orig, tgt []subtitle.Cue
		var at int64
		for i := 0; i+1 < len(a) && len(orig) < 200; i += 2 {
			start := at + int64(a[i])
			at = start + int64(a[i+1])
			orig = append(orig, subtitle.Cue{StartMS: start, EndMS: at, Text: "x"})
		}
		for i := 0; i+1 < len(b) && len(tgt) < 200; i += 2 {
			start := int64(b[i]) * 7
			tgt = append(tgt, subtitle.Cue{StartMS: start, EndMS: start + int64(b[i+1]), Text: "y"})
		}
		got := alignByOverlap(orig, tgt, " ")
		if len(got) != len(orig) {
			t.Fatalf("回傳 %d 句，原文 %d 句", len(got), len(orig))
		}
		total := 0
		for _, s := range got {
			if s != "" {
				total += len(strings.Fields(s))
			}
		}
		if total > len(tgt) {
			t.Fatalf("對齊出 %d 句，平台字幕只有 %d 句", total, len(tgt))
		}
	})
}

// COR-R4 同樣適用於 translate：詞彙表合併全域與頻道的詞（不只處理方案），放進固定前綴的 <video_metadata>。
func TestTranslateGlossaryMergesGlobalAndChannel(t *testing.T) {
	fake := &trnLLM{}
	r, it := newTrnRig(t, fake, nil, "We use Kafka with KRaft.")
	ctx := context.Background()
	it, _ = r.st.Read().ItemByID(ctx, it.ID) // metadata 完成之後才有頻道
	if _, err := r.p.PutGlossary(ctx, "global", "default", []string{"Kafka"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.PutGlossary(ctx, "channel", it.ChannelID, []string{"KRaft"}); err != nil {
		t.Fatal(err)
	}
	rerunTrn(t, r, it, "zh-Hant-TW")
	reqs := fake.requests()
	if len(reqs) == 0 {
		t.Fatal("沒有請求")
	}
	if !strings.Contains(reqs[0].prefix, `"glossary":["Kafka","KRaft"]`) {
		t.Errorf("<video_metadata> 的詞彙表：%s", reqs[0].prefix)
	}
}
