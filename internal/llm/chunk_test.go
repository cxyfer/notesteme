package llm

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 1},
		{"abcd", 2},               // 4 個字元 1 + 每句 1
		{"abcde", 3},              // 5 個字元進位成 2
		{"微服務", 4},                // 漢字一字 1
		{"かなカナ", 5},               // 假名一字 1
		{"한국어", 4},                // 諺文一字 1
		{"用 Kafka 處理", 3 + 2 + 1}, // 3 個漢字 + 「 Kafka 」7 個字元進位成 2 + 1
	}
	for _, c := range cases {
		if got := EstimateTokens(c.text); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d，預期 %d", c.text, got, c.want)
		}
	}
	// fixture yt-long01xxxxx 的每一句都剛好 20 tokens（LLM 待決問題 L2 的場景前提）。
	for _, n := range []int{1, 9, 10, 99, 100, 400} {
		s := fmt.Sprintf("Line %d of the long talk about slow and steady systems that scale to users", n)
		if got := EstimateTokens(s); got != 20 {
			t.Errorf("long01 第 %d 句 = %d tokens，預期 20", n, got)
		}
	}
}

// cuesOf 產生 n 句，每句的文字讓 EstimateTokens 剛好是 tokens（tokens >= 2）。
func cuesOf(n, tokens int, step time.Duration) []PlanCue {
	text := strings.Repeat("a", (tokens-1)*4)
	out := make([]PlanCue, n)
	for i := range out {
		out[i] = PlanCue{Start: time.Duration(i) * step, Text: text}
	}
	return out
}

func rangeOf(c Chunk) string {
	ctx := "none"
	if c.HasContext() {
		ctx = fmt.Sprintf("%d-%d", c.ContextFrom, c.ContextTo)
	}
	return fmt.Sprintf("%d:%d-%d/%d ctx %s", c.Index, c.From, c.To, c.Overlap, ctx)
}

func checkChunks(t *testing.T, got []Chunk, want ...string) {
	t.Helper()
	var g []string
	for _, c := range got {
		g = append(g, rangeOf(c))
	}
	if strings.Join(g, "; ") != strings.Join(want, "; ") {
		t.Errorf("chunks =\n  %s\n預期\n  %s", strings.Join(g, "; "), strings.Join(want, "; "))
	}
}

// LLM-R1 場景：400 句、每句 20 tokens、上限 4000、重疊 3、前文 5。
func TestPlanChunksByTokens(t *testing.T) {
	got := PlanChunks(cuesOf(400, 20, 3*time.Second), nil, ChunkParams{MaxTokens: 4000, OverlapCues: 3, ContextCues: 5})
	checkChunks(t, got, "1:1-200/0 ctx none", "2:198-400/3 ctx 193-197")
}

// LLM-R1 場景：章節 00:00、05:00、12:00；章節之間不重疊，前文仍是開頭之前的 5 句。
func TestPlanChunksByChapters(t *testing.T) {
	var cues []PlanCue
	add := func(n int, start, step time.Duration) {
		for i := 0; i < n; i++ {
			cues = append(cues, PlanCue{Start: start + time.Duration(i)*step, Text: "x"})
		}
	}
	add(40, 0, 7500*time.Millisecond)
	add(70, 300*time.Second, 6*time.Second)
	add(40, 720*time.Second, 6*time.Second)
	got := PlanChunks(cues, []time.Duration{0, 5 * time.Minute, 12 * time.Minute}, ChunkParams{MaxTokens: 4000, OverlapCues: 3, ContextCues: 5})
	checkChunks(t, got, "1:1-40/0 ctx none", "2:41-110/0 ctx 36-40", "3:111-150/0 ctx 106-110")
}

// 章節超過 max_tokens 時在章節內再依 token 切，重疊不跨章節；第一個章節之前的句子歸第一章。
func TestPlanChunksLongChapterAndLeadingCues(t *testing.T) {
	cues := cuesOf(12, 10, 10*time.Second) // 0s～110s
	got := PlanChunks(cues, []time.Duration{20 * time.Second, 80 * time.Second}, ChunkParams{MaxTokens: 30, OverlapCues: 1, ContextCues: 2})
	// 第一章：第 1～8 句（0s～70s，前兩句在第一個章節之前）；第二章：第 9～12 句。
	checkChunks(t, got,
		"1:1-3/0 ctx none", "2:3-6/1 ctx 1-2", "3:6-8/1 ctx 4-5",
		"4:9-11/0 ctx 7-8", "5:11-12/1 ctx 9-10")
}

// 每句都超過上限時一個 chunk 一句；重疊不會越過前一個 chunk 的開頭，chunk 一定往前推進。
func TestPlanChunksTinyBudget(t *testing.T) {
	got := PlanChunks(cuesOf(4, 50, time.Second), nil, ChunkParams{MaxTokens: 10, OverlapCues: 3, ContextCues: 0})
	checkChunks(t, got, "1:1-1/0 ctx none", "2:2-2/0 ctx none", "3:3-3/0 ctx none", "4:4-4/0 ctx none")
	got = PlanChunks(cuesOf(6, 10, time.Second), nil, ChunkParams{MaxTokens: 20, OverlapCues: 5, ContextCues: 1})
	checkChunks(t, got, "1:1-2/0 ctx none", "2:2-4/1 ctx 1-1", "3:3-6/2 ctx 2-2")
}

func TestPlanChunksEdges(t *testing.T) {
	if got := PlanChunks(nil, nil, DefaultChunkParams()); got != nil {
		t.Errorf("沒有句子時應該沒有 chunk：%v", got)
	}
	// max_tokens 為 0 時用預設值；重疊與前文為負數時當成 0。
	got := PlanChunks(cuesOf(3, 20, time.Second), nil, ChunkParams{MaxTokens: 0, OverlapCues: -1, ContextCues: -1})
	checkChunks(t, got, "1:1-3/0 ctx none")
}

func TestMergeCuesOverlapKeepsPrevious(t *testing.T) {
	chunks := []Chunk{{Index: 1, From: 1, To: 3}, {Index: 2, From: 2, To: 5, Overlap: 2}}
	out, err := MergeCues(chunks, [][]Cue{
		{{1, "a1"}, {2, "a2"}, {3, "a3"}},
		{{2, "b2"}, {3, "b3"}, {4, "b4"}, {5, "b5"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "1:a1 2:a2 3:a3 4:b4 5:b5"
	var g []string
	for _, c := range out {
		g = append(g, fmt.Sprintf("%d:%s", c.I, c.Text))
	}
	if strings.Join(g, " ") != want {
		t.Errorf("MergeCues = %v，預期 %s", g, want)
	}
	if _, err := MergeCues(chunks, [][]Cue{{{1, "a"}}, nil}); err == nil {
		t.Error("句數不符時應該失敗")
	}
	if _, err := MergeCues([]Chunk{{Index: 1, From: 2, To: 2}}, [][]Cue{{{2, "x"}}}); err == nil {
		t.Error("缺少開頭的句子時應該失敗")
	}
}
