package subtitle

import (
	"reflect"
	"testing"
)

// tc 解析測試用的時間碼，失敗直接結束。
func tc(t *testing.T, s string) int64 {
	t.Helper()
	ms, err := ParseTimecode(s)
	if err != nil {
		t.Fatalf("ParseTimecode(%q): %v", s, err)
	}
	return ms
}

// row 對應 feature 表格的一列，欄位寫法與場景相同。
type row struct {
	start, end, text string
	words            string
	timing           TimingOrigin
}

func (r row) cue(t *testing.T) Cue {
	t.Helper()
	c := Cue{StartMS: tc(t, r.start), EndMS: tc(t, r.end), Text: r.text, Timing: r.timing}
	if r.words != "" {
		w, err := ParseWords(r.words)
		if err != nil {
			t.Fatal(err)
		}
		c.Words = w
	}
	return c
}

func cuesOf(t *testing.T, rows []row) []Cue {
	t.Helper()
	var out []Cue
	for i, r := range rows {
		c := r.cue(t)
		c.Index = i + 1
		out = append(out, c)
	}
	return out
}

// checkCues 只比對期望列中有填的欄位（與步驟「只比對表格中出現的欄位」一致），
// 並一律檢查編號從 1 起算、start 不早於前一句的 end。
func checkCues(t *testing.T, got []Cue, want []row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("cue count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Index != i+1 {
			t.Errorf("cue %d: index = %d", i+1, g.Index)
		}
		if i > 0 && g.StartMS < got[i-1].EndMS {
			t.Errorf("cue %d: start %d earlier than previous end %d", i+1, g.StartMS, got[i-1].EndMS)
		}
		if w.text != "" && g.Text != w.text {
			t.Errorf("cue %d: text = %q, want %q", i+1, g.Text, w.text)
		}
		if w.start != "" && g.StartMS != tc(t, w.start) {
			t.Errorf("cue %d: start = %s, want %s", i+1, FormatDisplayTime(g.StartMS), w.start)
		}
		if w.end != "" && g.EndMS != tc(t, w.end) {
			t.Errorf("cue %d: end = %s, want %s", i+1, FormatDisplayTime(g.EndMS), w.end)
		}
		if w.timing != "" && g.Timing != w.timing {
			t.Errorf("cue %d: timing = %q, want %q", i+1, g.Timing, w.timing)
		}
		if w.words != "" {
			if got := FormatWords(g.Words); got != FormatWords(mustWords(t, w.words)) {
				t.Errorf("cue %d: words = %s, want %s", i+1, got, w.words)
			}
		}
	}
}

func mustWords(t *testing.T, s string) []Word {
	t.Helper()
	w, err := ParseWords(s)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// 場景表格直接抄自 features/NRM-normalize.feature。
func TestNormalizeScenarios(t *testing.T) {
	tests := []struct {
		name string // 場景名稱
		in   []row
		want []row
	}{
		{
			name: "A tiny fragment is merged into the previous cue",
			in: []row{
				{start: "00:00:01.000", end: "00:00:03.000", text: "this is the end"},
				{start: "00:00:03.000", end: "00:00:03.200", text: "."},
			},
			want: []row{
				{start: "00:00:01.000", end: "00:00:03.200", text: "this is the end.", timing: "merged"},
			},
		},
		{
			name: "Formatting tags and empty cues are removed but music markers stay",
			in: []row{
				{start: "00:00:01", end: "00:00:03", text: `{\an8}<i>Hello  there</i>`},
				{start: "00:00:03", end: "00:00:04", text: ""},
				{start: "00:00:04", end: "00:00:07", text: "[Music]"},
			},
			want: []row{
				{text: "Hello there"},
				{text: "[Music]"},
			},
		},
		{
			name: "Overlapping cues are trimmed so timings always move forward",
			in: []row{
				{start: "00:00:01.000", end: "00:00:04.000", text: "first line"},
				{start: "00:00:03.500", end: "00:00:06.000", text: "second line"},
			},
			want: []row{
				{start: "00:00:01.000", end: "00:00:03.460", text: "first line"},
				{start: "00:00:03.500", end: "00:00:06.000", text: "second line"},
			},
		},
		{
			name: "Out-of-order cues are sorted by start time",
			in: []row{
				{start: "00:00:05", end: "00:00:07", text: "later"},
				{start: "00:00:01", end: "00:00:03", text: "sooner"},
			},
			want: []row{
				{text: "sooner"},
				{text: "later"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := cuesOf(t, tt.in)
			orig := cuesOf(t, tt.in)
			got := Normalize(in)
			checkCues(t, got, tt.want)
			if !reflect.DeepEqual(in, orig) {
				t.Errorf("Normalize modified its input")
			}
		})
	}
}

// 場景 "Word timings survive a merge"：兩句都不短、也沒有重疊，
// 合併成一句只能靠 NRM-R1（前一句文字是後一句的開頭）。R1 待使用者決定，
// 所以場景輸入先跳過；下面另測 R2、R4 合併時字詞時間的保留與去重（NRM-R6）。
func TestNormalizeWordTimingsSurviveMerge(t *testing.T) {
	t.Run("scenario input needs NRM-R1", func(t *testing.T) {
		t.Skip("depends on NRM-R1 (rolling caption de-duplication), pending owner decision; see docs/specs/NRM-normalize.md")
		in := cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:02", text: "hello", words: "hello@00:00:01.000-00:00:01.800"},
			{start: "00:00:02", end: "00:00:03", text: "hello world", words: "hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.900"},
		})
		checkCues(t, Normalize(in), []row{
			{text: "hello world", words: "hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.900"},
		})
	})

	t.Run("fragment merge keeps word timings and drops exact duplicates", func(t *testing.T) {
		in := cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:02", text: "hello", words: "hello@00:00:01.000-00:00:01.800"},
			{start: "00:00:02", end: "00:00:02.200", text: "hello world", words: "hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.200"},
		})
		got := Normalize(in)
		checkCues(t, got, []row{
			{start: "00:00:01", end: "00:00:02.200", text: "hello hello world", timing: "merged",
				words: "hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.200"},
		})
	})

	t.Run("overlap trimming leaves word timings untouched", func(t *testing.T) {
		in := cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:04", text: "one two", words: "one@00:00:01.000-00:00:01.500 two@00:00:02.000-00:00:03.800"},
			{start: "00:00:03.500", end: "00:00:06", text: "three"},
		})
		got := Normalize(in)
		checkCues(t, got, []row{
			{end: "00:00:03.460", words: "one@00:00:01.000-00:00:01.500 two@00:00:02.000-00:00:03.800"},
			{start: "00:00:03.500"},
		})
	})
}

func TestNormalizeExtra(t *testing.T) {
	t.Run("overlap leaving a short cue merges with the next", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:01.000", end: "00:00:03.000", text: "alpha"},
			{start: "00:00:01.200", end: "00:00:05.000", text: "beta"},
		}))
		// alpha 修正後為 1.000–1.160，短於 300ms，併入 beta。
		checkCues(t, got, []row{{start: "00:00:01.000", end: "00:00:05.000", text: "alpha beta", timing: "merged"}})
	})

	t.Run("chained overlaps keep start not earlier than previous end", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:00", end: "00:00:10", text: "a"},
			{start: "00:00:02", end: "00:00:12", text: "b"},
			{start: "00:00:04", end: "00:00:14", text: "c"},
		}))
		checkCues(t, got, []row{
			{start: "00:00:00", end: "00:00:01.960", text: "a"},
			{start: "00:00:02", end: "00:00:03.960", text: "b"},
			{start: "00:00:04", end: "00:00:14", text: "c"},
		})
	})

	t.Run("short cue is merged with a space unless punctuation", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:03", text: "hello"},
			{start: "00:00:03", end: "00:00:03.100", text: "yes"},
			{start: "00:00:04", end: "00:00:06", text: "bye"},
			{start: "00:00:06", end: "00:00:08", text: "…"},
		}))
		checkCues(t, got, []row{
			{text: "hello yes", end: "00:00:03.100", timing: "merged"},
			{text: "bye…", end: "00:00:08", timing: "merged"},
		})
	})

	t.Run("first fragment has nothing to merge into", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:01.100", text: "."},
			{start: "00:00:02", end: "00:00:04", text: "next"},
		}))
		checkCues(t, got, []row{{text: "."}, {text: "next"}})
	})

	t.Run("tags: timestamps, font and ASS codes", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:03", text: `<font color="#fff">a</font><00:00:02.000> b {\pos(1,2)}c`},
			{start: "00:00:04", end: "00:00:06", text: "x < y > z"},
			{start: "00:00:07", end: "00:00:09", text: "♪ la ♪"},
		}))
		checkCues(t, got, []row{{text: "a b c"}, {text: "x < y > z"}, {text: "♪ la ♪"}})
	})

	t.Run("invalid end and empty input", func(t *testing.T) {
		got := Normalize([]Cue{{StartMS: 5000, EndMS: 1000, Text: "x"}})
		if len(got) != 1 || got[0].EndMS != 5000 || got[0].Timing != TimingSource {
			t.Fatalf("got %+v", got)
		}
		if got := Normalize(nil); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("speaker and words are copied", func(t *testing.T) {
		sp := "A"
		in := []Cue{{StartMS: 0, EndMS: 1000, Text: "x", Speaker: &sp, Words: []Word{{"x", 0, 500}}}}
		got := Normalize(in)
		*got[0].Speaker = "B"
		got[0].Words[0].Text = "y"
		if sp != "A" || in[0].Words[0].Text != "x" {
			t.Fatal("input was modified through the output")
		}
	})

	t.Run("normalize is idempotent", func(t *testing.T) {
		in := cuesOf(t, []row{
			{start: "00:00:01.000", end: "00:00:04.000", text: "first line"},
			{start: "00:00:03.500", end: "00:00:06.000", text: "second line"},
			{start: "00:00:06.000", end: "00:00:06.100", text: "!"},
		})
		once := Normalize(in)
		if twice := Normalize(once); !reflect.DeepEqual(once, twice) {
			t.Fatalf("once=%+v twice=%+v", once, twice)
		}
	})
}
