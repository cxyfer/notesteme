package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func segCue(startMS, endMS int64, text string, words ...subtitle.Word) subtitle.Cue {
	return subtitle.Cue{StartMS: startMS, EndMS: endMS, Text: text, Words: words}
}

func w(text string, s, e int64) subtitle.Word { return subtitle.Word{Text: text, StartMS: s, EndMS: e} }

func newSegBuilder(split string, cues []subtitle.Cue, sils []media.Silence) *segBuilder {
	b := &segBuilder{ctx: context.Background(), cues: cues, split: split, rules: subtitle.DefaultRules(subtitle.ScriptLatin)}
	if sils != nil {
		b.silences = func(context.Context) ([]media.Silence, error) { return sils, nil }
	}
	return b
}

// marksFor 用 llm.SegmentMarks 從 (text, breaks) 算出斷點，和 production 的路徑相同。
func marksFor(t *testing.T, cues []subtitle.Cue, text string, breaks ...int) []llm.CueMarks {
	t.Helper()
	in := llm.ChunkInput{Chunk: llm.Chunk{Index: 1, From: 1, To: len(cues)}, Chunks: 1}
	for k, c := range cues {
		in.Cues = append(in.Cues, llm.Cue{I: k + 1, Text: c.Text})
	}
	if breaks == nil {
		breaks = []int{}
	}
	out, _ := json.Marshal(llm.SegmentOutput{Text: text, Breaks: breaks})
	m, verr := llm.SegmentMarks(in, out)
	if verr != nil {
		t.Fatal(verr)
	}
	return m
}

type wantCue struct {
	start, end int64
	text       string
	timing     subtitle.TimingOrigin
}

func checkCues(t *testing.T, got []subtitle.Cue, want []wantCue) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("有 %d 句，預期 %d 句：%+v", len(got), len(want), got)
	}
	for k, wc := range want {
		g := got[k]
		if g.Index != k+1 || g.StartMS != wc.start || g.EndMS != wc.end || g.Text != wc.text || g.Timing != wc.timing {
			t.Errorf("第 %d 句 = %d %d-%d %q %s，預期 %d-%d %q %s", k+1, g.Index, g.StartMS, g.EndMS, g.Text, g.Timing, wc.start, wc.end, wc.text, wc.timing)
		}
	}
}

func TestSegmentMergeKeepsOuterTimes(t *testing.T) {
	cues := []subtitle.Cue{segCue(1000, 2000, "so today we"), segCue(2000, 4000, "talk about"), segCue(4000, 5000, "Kafka.")}
	got, err := newSegBuilder(SplitWhenNeeded, cues, nil).build(marksFor(t, cues, "so today we talk about Kafka."))
	if err != nil {
		t.Fatal(err)
	}
	checkCues(t, got, []wantCue{{1000, 5000, "so today we talk about Kafka.", subtitle.TimingMerged}})
}

func TestSegmentWordTimedSplit(t *testing.T) {
	cues := []subtitle.Cue{segCue(1000, 6000, "We use Kafka. It is fast.",
		w("We", 1000, 1300), w("use", 1300, 1700), w("Kafka.", 1700, 2600), w("It", 3400, 3600), w("is", 3600, 3800), w("fast.", 3800, 4500))}
	// forbid 也照切：有 word 級時間時時間仍來自來源（SEG-R7 只管沒有 word 級時間的情況）。
	for _, split := range []string{SplitForbid, SplitWhenNeeded, SplitFree} {
		got, err := newSegBuilder(split, cues, nil).build(marksFor(t, cues, "We use Kafka. It is fast.", 14))
		if err != nil {
			t.Fatal(err)
		}
		checkCues(t, got, []wantCue{{1000, 2600, "We use Kafka.", subtitle.TimingSource}, {3400, 6000, "It is fast.", subtitle.TimingSource}})
		if len(got[0].Words) != 3 || len(got[1].Words) != 3 {
			t.Fatalf("%s：字詞沒有分到兩段：%+v", split, got)
		}
	}
}

func TestSegmentSplitPolicyWithoutWords(t *testing.T) {
	short := []subtitle.Cue{segCue(1000, 6000, "We use Kafka. It is fast.")}
	long := []subtitle.Cue{segCue(1000, 12000, "this sentence is far too long to show on screen at once so we split it")}
	longText := long[0].Text
	at := strings.Index(longText, "so we")
	cases := []struct {
		name  string
		split string
		cues  []subtitle.Cue
		text  string
		brk   int
		sils  []media.Silence
		want  []wantCue
	}{
		{name: "forbid ignores the split", split: SplitForbid, cues: short, text: short[0].Text, brk: 14,
			want: []wantCue{{1000, 6000, "We use Kafka. It is fast.", subtitle.TimingSource}}},
		{name: "when_needed ignores a short cue", split: SplitWhenNeeded, cues: short, text: short[0].Text, brk: 14,
			want: []wantCue{{1000, 6000, "We use Kafka. It is fast.", subtitle.TimingSource}}},
		{name: "free interpolates", split: SplitFree, cues: short, text: short[0].Text, brk: 14,
			want: []wantCue{{1000, 3750, "We use Kafka.", subtitle.TimingInterpolated}, {3750, 6000, "It is fast.", subtitle.TimingInterpolated}}},
		{name: "when_needed splits a long cue at a silence", split: SplitWhenNeeded, cues: long, text: longText, brk: at,
			sils: []media.Silence{{StartS: 0.2, EndS: 0.5}, {StartS: 6.2, EndS: 6.5}, {StartS: 11.9, EndS: 12.5}},
			want: []wantCue{{1000, 6200, "this sentence is far too long to show on screen at once", subtitle.TimingSilence}, {6500, 12000, "so we split it", subtitle.TimingSilence}}},
		{name: "when_needed interpolates without silences", split: SplitWhenNeeded, cues: long, text: longText, brk: at, sils: []media.Silence{},
			want: []wantCue{{1000, 9800, "this sentence is far too long to show on screen at once", subtitle.TimingInterpolated}, {9800, 12000, "so we split it", subtitle.TimingInterpolated}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := newSegBuilder(tc.split, tc.cues, tc.sils).build(marksFor(t, tc.cues, tc.text, tc.brk))
			if err != nil {
				t.Fatal(err)
			}
			checkCues(t, got, tc.want)
		})
	}
}

// SEG-R10：超過上限的一整句由程式切開；forbid 時不切。LLM 切出的片段仍超過上限時只記 warnings（不再切）。
func TestSegmentEnforcesRules(t *testing.T) {
	text := "First we install the broker, then we create a topic, and finally we start producing records to it."
	cues := []subtitle.Cue{segCue(0, 12000, text)}
	got, err := newSegBuilder(SplitWhenNeeded, cues, []media.Silence{}).build(marksFor(t, cues, text))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 {
		t.Fatalf("超過上限的句子沒有被切開：%+v", got)
	}
	rules := subtitle.DefaultRules(subtitle.ScriptLatin)
	for _, c := range got {
		if rules.TooLong(c) || c.Timing != subtitle.TimingInterpolated {
			t.Fatalf("切開後仍超過上限或時間來源不對：%+v", c)
		}
	}
	if !strings.HasSuffix(got[0].Text, ",") {
		t.Errorf("應該優先切在標點之後：%q", got[0].Text)
	}
	if !sameSegmentKeys(cues, got) {
		t.Fatal("切開後文字不同")
	}

	forbid, err := newSegBuilder(SplitForbid, cues, nil).build(marksFor(t, cues, text))
	if err != nil {
		t.Fatal(err)
	}
	checkCues(t, forbid, []wantCue{{0, 12000, text, subtitle.TimingSource}})
}

// 合併後超過上限的句子，在原本的句子邊界切回去，時間仍來自來源。
func TestSegmentRegroupsLongMerges(t *testing.T) {
	var cues []subtitle.Cue
	var parts []string
	for k := range 4 {
		txt := "this is a fairly long sentence number " + string(rune('a'+k))
		cues = append(cues, segCue(int64(k)*3000, int64(k)*3000+3000, txt))
		parts = append(parts, txt)
	}
	got, err := newSegBuilder(SplitWhenNeeded, cues, nil).build(marksFor(t, cues, strings.Join(parts, " ")))
	if err != nil {
		t.Fatal(err)
	}
	checkCues(t, got, []wantCue{
		{0, 6000, parts[0] + " " + parts[1], subtitle.TimingMerged},
		{6000, 12000, parts[2] + " " + parts[3], subtitle.TimingMerged},
	})
}

func TestSegmentJoinsCJKWithoutSpaces(t *testing.T) {
	cues := []subtitle.Cue{segCue(1000, 2000, "大家好，"), segCue(2000, 3000, "今天想聊聊")}
	b := newSegBuilder(SplitWhenNeeded, cues, nil)
	b.rules = subtitle.DefaultRules(subtitle.ScriptCJK)
	got, err := b.build(marksFor(t, cues, "大家好，今天想聊聊"))
	if err != nil {
		t.Fatal(err)
	}
	checkCues(t, got, []wantCue{{1000, 3000, "大家好，今天想聊聊", subtitle.TimingMerged}})
}

// 一句字幕的時間來源保留輸入的值（空的視為 source），合併時取比較不可靠的。
func TestSegmentKeepsInputTiming(t *testing.T) {
	cues := []subtitle.Cue{segCue(1000, 2000, "a"), segCue(2000, 3000, "b"), segCue(3000, 4000, "c")}
	cues[1].Timing = subtitle.TimingInterpolated
	got, err := newSegBuilder(SplitWhenNeeded, cues, nil).build(marksFor(t, cues, "a b c", 2, 4))
	if err != nil {
		t.Fatal(err)
	}
	checkCues(t, got, []wantCue{{1000, 2000, "a", subtitle.TimingSource}, {2000, 3000, "b", subtitle.TimingInterpolated}, {3000, 4000, "c", subtitle.TimingSource}})
}

// 字詞與文字對不上時當成沒有 word 級時間（不能用錯位的時間）。
func TestSegmentIgnoresMismatchedWords(t *testing.T) {
	cues := []subtitle.Cue{segCue(1000, 6000, "We use Kafka. It is fast.", w("totally", 1000, 2000), w("different", 2000, 3000))}
	got, err := newSegBuilder(SplitForbid, cues, nil).build(marksFor(t, cues, cues[0].Text, 14))
	if err != nil {
		t.Fatal(err)
	}
	checkCues(t, got, []wantCue{{1000, 6000, "We use Kafka. It is fast.", subtitle.TimingSource}})
}

func TestMergeSegmentMarksOverlap(t *testing.T) {
	cues := []llm.Cue{{I: 1, Text: "a"}, {I: 2, Text: "b"}, {I: 3, Text: "c"}, {I: 4, Text: "d"}}
	job := &llmJob{cues: cues, chunks: []llm.Chunk{{Index: 1, From: 1, To: 3}, {Index: 2, From: 3, To: 4, Overlap: 1}}}
	// chunk 1 合併 2、3；chunk 2 想把 3 與 4 分開（3 是重疊區，以 chunk 1 為準），4 接在 3 後面合併。
	out1, _ := json.Marshal(llm.SegmentOutput{Text: "a b c", Breaks: []int{2}})
	out2, _ := json.Marshal(llm.SegmentOutput{Text: "c d", Breaks: []int{}})
	got, err := mergeSegmentMarks(job, []json.RawMessage{out1, out2})
	if err != nil {
		t.Fatal(err)
	}
	joins := []bool{got[0].Join, got[1].Join, got[2].Join, got[3].Join}
	if want := []bool{false, false, true, true}; !equalBools(joins, want) {
		t.Fatalf("join = %v，預期 %v", joins, want)
	}
	// 退回的 chunk 保留原本的切法。
	got, err = mergeSegmentMarks(job, []json.RawMessage{out1, nil})
	if err != nil {
		t.Fatal(err)
	}
	if got[3].Join {
		t.Fatalf("退回的 chunk 應該保留原本的切法：%+v", got)
	}
	if _, err := mergeSegmentMarks(job, []json.RawMessage{out1}); err == nil {
		t.Fatal("輸出數與 chunk 數不同時要失敗")
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTimingAndSubtitleRules(t *testing.T) {
	ts, err := Effective{}.Timing()
	if err != nil || ts.SplitTiming != SplitWhenNeeded || ts.SplitTimingAudio != SplitAudioAuto {
		t.Fatalf("%+v %v", ts, err)
	}
	ts, err = Effective{"timing": map[string]any{"split_timing": "forbid", "split_timing_audio": "never"}}.Timing()
	if err != nil || ts.SplitTiming != SplitForbid || ts.SplitTimingAudio != SplitAudioNever {
		t.Fatalf("%+v %v", ts, err)
	}
	if _, err := (Effective{"timing": map[string]any{"split_timing": "sometimes"}}).Timing(); err == nil {
		t.Fatal("不認識的 split_timing 要失敗")
	}
	r, err := Effective{"subtitle_rules": map[string]any{"cjk": map[string]any{"max_chars_per_line": float64(16), "max_duration": "6s"}}}.SubtitleRules(subtitle.ScriptCJK)
	if err != nil || r.MaxCharsPerLine != 16 || r.MaxDurationMS != 6000 || r.MaxLines != 2 || r.MinDurationMS != 800 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := (Effective{"subtitle_rules": map[string]any{"latin": map[string]any{"max_lines": 1.5}}}).SubtitleRules(subtitle.ScriptLatin); err == nil {
		t.Fatal("不是整數的 max_lines 要失敗")
	}
}

// timing 與 subtitle_rules 會影響 segment 的產出，要進 params_sha256：換了處理方案的重跑不能命中舊的快取（SEG-R7、R9）。
func TestSegmentParamsDependOnTiming(t *testing.T) {
	a, err := segmentParams(Effective{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := segmentParams(Effective{"timing": map[string]any{"split_timing": "forbid"}})
	c, _ := segmentParams(Effective{"subtitle_rules": map[string]any{"latin": map[string]any{"max_lines": float64(1)}}})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	jc, _ := json.Marshal(c)
	if string(ja) == string(jb) || string(ja) == string(jc) {
		t.Fatalf("params 沒有反映 timing 或 subtitle_rules：%s %s %s", ja, jb, jc)
	}
	if llmStages[StageCorrect].params != nil {
		t.Fatal("correct 不應該有 segment 的 params（會改變它的快取鍵）")
	}
}

func TestResolveSegmentManualCaptions(t *testing.T) {
	eff := Effective{"stages": map[string]any{StageSegment: map[string]any{"executor": "api", "provider": "openai", "model": "m", "prompt": "segment-default"}}}
	res, err := ResolveExecutor(eff, StageSegment, StageContext{ManualCaptions: true})
	if err != nil || !res.Skip || res.Reason != SkipManualCaptions {
		t.Fatalf("%+v %v", res, err)
	}
	res, _ = ResolveExecutor(eff, StageSegment, StageContext{})
	if res.Skip {
		t.Fatalf("不是人工字幕時不略過：%+v", res)
	}
	eff["stages"].(map[string]any)[StageSegment].(map[string]any)["on_manual"] = OnManualRun
	res, _ = ResolveExecutor(eff, StageSegment, StageContext{ManualCaptions: true})
	if res.Skip {
		t.Fatalf("on_manual: run 時要執行：%+v", res)
	}
	// correct 不受影響。
	effC := Effective{"stages": map[string]any{StageCorrect: map[string]any{"executor": "api", "provider": "openai", "model": "m", "prompt": "correct-default"}}}
	if res, _ := ResolveExecutor(effC, StageCorrect, StageContext{ManualCaptions: true}); res.Skip {
		t.Fatalf("correct 不應該因為人工字幕略過：%+v", res)
	}
}

func TestDegradable(t *testing.T) {
	if !degradable(&errclass.Error{Class: errclass.ClassPermanent}) || !degradable(&errclass.Error{Class: errclass.ClassNeedsAction}) {
		t.Fatal("permanent、needs_action 改用插值")
	}
	if degradable(&errclass.Error{Class: errclass.ClassRetryable}) || degradable(errors.New("io")) {
		t.Fatal("retryable 與不明的錯誤要重試")
	}
}

// FuzzSegmentBuild：任意的文字、字詞與斷點都不會 panic；產出的文字與輸入相同（SEG-R4），時間單調且在輸入的範圍內。
func FuzzSegmentBuild(f *testing.F) {
	f.Add("We use Kafka. It is fast.", "next one", "We use Kafka. It is fast. next one", 14, 26, "free", true)
	f.Add("大家好，今天", "拆分服务", "大家好，今天拆分服务", 3, 6, "when_needed", false)
	f.Fuzz(func(t *testing.T, a, b, text string, b1, b2 int, split string, withWords bool) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) || len(a)+len(b) > 400 {
			return
		}
		if split != SplitForbid && split != SplitFree {
			split = SplitWhenNeeded
		}
		cues := []subtitle.Cue{segCue(0, 9000, a), segCue(9000, 20000, b)}
		if withWords {
			for k, f := range strings.Fields(a) {
				cues[0].Words = append(cues[0].Words, w(f, int64(k)*100, int64(k)*100+90))
			}
		}
		in := llm.ChunkInput{Chunk: llm.Chunk{Index: 1, From: 1, To: 2}, Chunks: 1, Cues: []llm.Cue{{I: 1, Text: a}, {I: 2, Text: b}}}
		out, _ := json.Marshal(llm.SegmentOutput{Text: text, Breaks: []int{b1, b2}})
		marks, verr := llm.SegmentMarks(in, out)
		if verr != nil {
			marks = llm.KeepMarks(in.Cues)
		}
		got, err := newSegBuilder(split, cues, []media.Silence{{StartS: 4, EndS: 4.5}}).build(marks)
		if err != nil {
			t.Fatal(err)
		}
		if !sameSegmentKeys(cues, got) {
			t.Fatalf("文字不同：%+v", got)
		}
		var prevEnd int64
		for _, c := range got {
			if c.StartMS < prevEnd || c.EndMS < c.StartMS || c.StartMS < 0 || c.EndMS > 20000 {
				t.Fatalf("時間不單調或超出範圍：%+v", got)
			}
			prevEnd = c.EndMS
		}
	})
}

// 不可信的輸出把很長的 chunk 合併成一句時，合併與重新分組仍是線性時間（不能 O(n²) 拖垮 assemble）。
func TestSegmentLargeMergeIsFast(t *testing.T) {
	const n = 20000
	cues := make([]subtitle.Cue, n)
	marks := make([]llm.CueMarks, n)
	for k := range cues {
		cues[k] = segCue(int64(k)*1000, int64(k)*1000+900, "short words here")
		marks[k] = llm.CueMarks{I: k + 1, Join: k > 0}
	}
	start := time.Now()
	got, err := newSegBuilder(SplitWhenNeeded, cues, nil).build(marks)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("合併 %d 句花了 %s", n, d)
	}
	if len(got) < n/10 || !sameSegmentKeys(cues, got) {
		t.Fatalf("重新分組後有 %d 句", len(got))
	}
}
