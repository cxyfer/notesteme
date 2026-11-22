package subtitle

import (
	"reflect"
	"strings"
	"testing"
)

func TestLinesNeeded(t *testing.T) {
	cases := []struct {
		text string
		max  int
		want int
	}{
		{"", 42, 0},
		{"hello world", 42, 1},
		{"this sentence is far too long to show on screen at once so we split it", 42, 2},
		{"Welcome everyone, today we talk about running streaming pipelines at scale.", 42, 2},
		{"aaaa bbbb", 4, 2},
		{"aaaa bbbb", 9, 1},
		{strings.Repeat("x", 10), 4, 3}, // 單一字詞超過一行
		{"line one\nline two", 42, 2},
		{"大家好，今天想聊聊我们团队怎么拆分服务。", 22, 1},
		{"大家好，今天想聊聊我们团队怎么拆分服务。", 10, 2},
		{"用 Kafka 處理事件", 22, 1},
		{"一二三四五", 2, 3},
	}
	for _, tc := range cases {
		if got := LinesNeeded(tc.text, tc.max); got != tc.want {
			t.Errorf("LinesNeeded(%q, %d) = %d，預期 %d", tc.text, tc.max, got, tc.want)
		}
	}
}

func TestScriptOf(t *testing.T) {
	cases := map[string]Script{"zh-Hans": ScriptCJK, "ja": ScriptCJK, "ko-KR": ScriptCJK, "en": ScriptLatin, "fr-CA": ScriptLatin}
	for lang, want := range cases {
		if got := ScriptOf(lang); got != want {
			t.Errorf("ScriptOf(%q) = %s", lang, got)
		}
	}
	if ScriptOf("", "今天想聊聊") != ScriptCJK || ScriptOf("", "hello") != ScriptLatin || ScriptOf("") != ScriptLatin {
		t.Error("語言不明時依文字判斷")
	}
}

func TestRulesCheck(t *testing.T) {
	r := DefaultRules(ScriptLatin)
	ok := Cue{StartMS: 1000, EndMS: 5000, Text: "short"}
	if v := r.Check(ok); v != nil || r.TooLong(ok) {
		t.Fatalf("%+v", v)
	}
	long := Cue{StartMS: 1000, EndMS: 12000, Text: strings.Repeat("word ", 30)}
	got := r.Check(long)
	want := []Violation{{Rule: RuleMaxLines, Value: 4, Limit: 2}, {Rule: RuleMaxDuration, Value: 11000, Limit: 7000}}
	if !reflect.DeepEqual(got, want) || !r.TooLong(long) {
		t.Fatalf("%+v", got)
	}
	short := Cue{StartMS: 1000, EndMS: 1500, Text: "hi"}
	if v := r.Check(short); len(v) != 1 || v[0].Rule != RuleMinDuration || r.TooLong(short) {
		t.Fatalf("%+v", v)
	}
	if DefaultRules(ScriptCJK).MaxCharsPerLine != 22 {
		t.Fatal("CJK 每行字數的預設值")
	}
}

func FuzzLinesNeeded(f *testing.F) {
	f.Add("hello world", 3)
	f.Add("大家好，今天 Kafka", 2)
	f.Fuzz(func(t *testing.T, text string, max int) {
		if max <= 0 || max > 1000 {
			return
		}
		n := LinesNeeded(text, max)
		if n < 0 || n > len([]rune(text))+strings.Count(text, "\n")+1 {
			t.Fatalf("LinesNeeded(%q, %d) = %d", text, max, n)
		}
	})
}
