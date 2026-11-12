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
			name: "Rolling auto captions are de-duplicated",
			in: []row{
				{start: "00:00:01", end: "00:00:03", text: "so today we"},
				{start: "00:00:02", end: "00:00:04", text: "so today we talk about"},
				{start: "00:00:03", end: "00:00:06", text: "so today we talk about Kafka"},
			},
			want: []row{
				{start: "00:00:01", end: "00:00:06", text: "so today we talk about Kafka"},
			},
		},
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
// 合併成一句靠 NRM-R1（前一句文字是後一句的開頭）；另測 R2 合併時字詞時間的保留與去重（NRM-R6）。
func TestNormalizeWordTimingsSurviveMerge(t *testing.T) {
	t.Run("rolling merge (scenario input)", func(t *testing.T) {
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
			{start: "00:00:01", end: "00:00:02", text: "say hello", words: "hello@00:00:01.000-00:00:01.800"},
			{start: "00:00:02", end: "00:00:02.200", text: "hello world", words: "hello@00:00:01.000-00:00:01.800 world@00:00:02.100-00:00:02.200"},
		})
		got := Normalize(in)
		checkCues(t, got, []row{
			{start: "00:00:01", end: "00:00:02.200", text: "say hello hello world", timing: "merged",
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
	t.Run("identical consecutive text is merged", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:03", text: "same"},
			{start: "00:00:03", end: "00:00:05", text: "same"},
		}))
		checkCues(t, got, []row{{start: "00:00:01", end: "00:00:05", text: "same", timing: "merged"}})
	})

	t.Run("text that does not start with the previous one is kept", func(t *testing.T) {
		got := Normalize(cuesOf(t, []row{
			{start: "00:00:01", end: "00:00:03", text: "hello world"},
			{start: "00:00:03", end: "00:00:05", text: "hello"},
		}))
		checkCues(t, got, []row{{text: "hello world"}, {text: "hello"}})
	})

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

	t.Run("only real formatting tags are removed", func(t *testing.T) {
		for in, want := range map[string]string{
			"if a<b and b>c then x <3 y":          "if a<b and b>c then x <3 y",
			"<i>x</i> <b>y</b> <u>z</u> <s>w</s>": "x y z w",
			`<font color="#fff" size=2>a</font>`:  "a",
			"<c.colorE5E5E5>a</c><v Bob>b</v>":    "ab",
			"<ruby>漢<rt>kan</rt></ruby>":          "漢kan",
			`<lang en>hi</lang>`:                  "hi",
			"a<00:00:01.000><c> b</c>":            "a b",
			"1 <2 and 3> 4":                       "1 <2 and 3> 4",
			"use <br> or <div>x</div> literally":  "use <br> or <div>x</div> literally",
			"<I>caps</I>":                         "caps",
		} {
			got := Normalize([]Cue{{StartMS: 0, EndMS: 1000, Text: in}})
			if len(got) != 1 || got[0].Text != want {
				t.Errorf("%q -> %+v, want %q", in, got, want)
			}
		}
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

func TestNormalizeRollingBoundaries(t *testing.T) {
	tests := []struct {
		name string
		in   []row
		want []row
	}{
		{
			name: "prefix inside a word is not merged",
			in: []row{
				{start: "00:00:01", end: "00:00:02", text: "Go"},
				{start: "00:00:02", end: "00:00:04", text: "Gone fishing"},
			},
			want: []row{{text: "Go"}, {text: "Gone fishing"}},
		},
		{
			name: "same line ten minutes apart is kept twice",
			in: []row{
				{start: "00:00:01", end: "00:00:02", text: "Yes."},
				{start: "00:10:00", end: "00:10:01", text: "Yes."},
			},
			want: []row{{start: "00:00:01", text: "Yes."}, {start: "00:10:00", text: "Yes."}},
		},
		{
			name: "chinese prefix is merged without spaces",
			in: []row{
				{start: "00:00:01", end: "00:00:03", text: "今天我們"},
				{start: "00:00:02", end: "00:00:05", text: "今天我們來談 Kafka"},
			},
			want: []row{{start: "00:00:01", end: "00:00:05", text: "今天我們來談 Kafka"}},
		},
		{
			name: "prefix followed by punctuation is merged",
			in: []row{
				{start: "00:00:01", end: "00:00:03", text: "Hello"},
				{start: "00:00:02", end: "00:00:05", text: "Hello, world"},
			},
			want: []row{{start: "00:00:01", end: "00:00:05", text: "Hello, world"}},
		},
		{
			name: "gap of exactly one second is merged",
			in: []row{
				{start: "00:00:01", end: "00:00:03", text: "so today"},
				{start: "00:00:04", end: "00:00:06", text: "so today we"},
			},
			want: []row{{start: "00:00:01", end: "00:00:06", text: "so today we"}},
		},
		{
			name: "gap just over one second is not merged",
			in: []row{
				{start: "00:00:01", end: "00:00:03", text: "so today"},
				{start: "00:00:04.001", end: "00:00:06", text: "so today we"},
			},
			want: []row{{text: "so today"}, {text: "so today we"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkCues(t, Normalize(cuesOf(t, tt.in)), tt.want)
		})
	}
}

func TestEndsAtWordBoundary(t *testing.T) {
	tests := []struct {
		prefix, rest string
		want         bool
	}{
		{"Go", "", true},
		{"Go", " fishing", true},
		{"Go", "!", true},
		{"Go", "ne fishing", false},
		{"今天我們", "來談", true},
		{"Kafka", "是", true},
		{"今天", " Kafka", true},
		{"今天 Kafka", "s", false},
		{"こんにちは", "世界", true},
		{"カタカナ", "x", true},
		{"안녕", "하세요", true},
	}
	for _, tt := range tests {
		if got := endsAtWordBoundary(tt.prefix, tt.rest); got != tt.want {
			t.Errorf("endsAtWordBoundary(%q, %q) = %v, want %v", tt.prefix, tt.rest, got, tt.want)
		}
	}
}
