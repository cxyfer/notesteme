package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func segInput(texts ...string) ChunkInput {
	in := ChunkInput{Chunk: Chunk{Index: 1, From: 1, To: len(texts)}, Chunks: 1, Context: []Cue{}}
	for k, t := range texts {
		in.Cues = append(in.Cues, Cue{I: k + 1, Text: t})
	}
	return in
}

func segOut(t *testing.T, text string, breaks ...int) json.RawMessage {
	t.Helper()
	if breaks == nil {
		breaks = []int{}
	}
	b, err := json.Marshal(SegmentOutput{Text: text, Breaks: breaks})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSegmentSpecReady(t *testing.T) {
	if err := SegmentSpec().Ready(); err != nil {
		t.Fatal(err)
	}
}

func TestSegmentMarks(t *testing.T) {
	cases := []struct {
		name   string
		in     ChunkInput
		text   string
		breaks []int
		want   []CueMarks
		reason string
	}{
		{
			name: "keep every cue", in: segInput("so today we", "talk about", "Kafka."),
			text: "so today we talk about Kafka.", breaks: []int{12, 23},
			want: []CueMarks{{I: 1}, {I: 2}, {I: 3}},
		},
		{
			name: "merge everything", in: segInput("so today we", "talk about", "Kafka."),
			text: "so today we talk about Kafka.",
			want: []CueMarks{{I: 1}, {I: 2, Join: true}, {I: 3, Join: true}},
		},
		{
			name: "split inside a cue keeps the period on the left", in: segInput("We use Kafka. It is fast."),
			text: "We use Kafka. It is fast.", breaks: []int{14},
			want: []CueMarks{{I: 1, Cuts: []int{13}}},
		},
		{
			name: "break on the space itself", in: segInput("We use Kafka. It is fast."),
			text: "We use Kafka. It is fast.", breaks: []int{13},
			want: []CueMarks{{I: 1, Cuts: []int{13}}},
		},
		{
			name: "added commas still align", in: segInput("ok so first we install it"),
			text: "ok, so first, we install it", breaks: []int{14},
			want: []CueMarks{{I: 1, Cuts: []int{11}}},
		},
		{
			name: "changed spaces still align", in: segInput("a  b", "c"),
			text: "a b c", breaks: []int{4},
			want: []CueMarks{{I: 1}, {I: 2}},
		},
		{
			name: "cjk without spaces", in: segInput("大家好，今天想聊聊拆分服务。"),
			text: "大家好，今天想聊聊拆分服务。", breaks: []int{4},
			want: []CueMarks{{I: 1, Cuts: []int{4}}},
		},
		{
			name: "cjk between characters", in: segInput("今天想聊聊"),
			text: "今天想聊聊", breaks: []int{2},
			want: []CueMarks{{I: 1, Cuts: []int{2}}},
		},
		{
			name: "cue made only of punctuation joins the left", in: segInput("hello", "...", "world"),
			text: "hello ... world", breaks: []int{10},
			want: []CueMarks{{I: 1}, {I: 2, Join: true}, {I: 3}},
		},
		{name: "rewritten word", in: segInput("we use kafka"), text: "we use apache kafka", reason: ReasonTextChanged},
		{name: "dropped word", in: segInput("we use kafka"), text: "we kafka", reason: ReasonTextChanged},
		{name: "extra word at the end", in: segInput("we use kafka"), text: "we use kafka now", reason: ReasonTextChanged},
		{name: "case changed", in: segInput("we use kafka"), text: "We use kafka", reason: ReasonTextChanged},
		{name: "timestamp inside the text", in: segInput("hello world"), text: "00:00:01 hello world", reason: ReasonTextChanged},
		{name: "inside a word", in: segInput("Welcome everyone"), text: "Welcome everyone", breaks: []int{3}, reason: ReasonAlignment},
		{name: "inside a word with an apostrophe", in: segInput("don't go"), text: "don't go", breaks: []int{4}, reason: ReasonAlignment},
		{name: "negative", in: segInput("a b"), text: "a b", breaks: []int{-1}, reason: ReasonAlignment},
		{name: "zero", in: segInput("a b"), text: "a b", breaks: []int{0}, reason: ReasonAlignment},
		{name: "past the end", in: segInput("a b"), text: "a b", breaks: []int{3}, reason: ReasonAlignment},
		{name: "duplicate", in: segInput("a b c"), text: "a b c", breaks: []int{2, 2}, reason: ReasonAlignment},
		{name: "decreasing", in: segInput("a b c"), text: "a b c", breaks: []int{4, 2}, reason: ReasonAlignment},
		{name: "empty subtitle between two breaks", in: segInput("a b c"), text: "a b  c", breaks: []int{3, 4}, reason: ReasonAlignment},
		{name: "break before any word", in: segInput("a b"), text: " a b", breaks: []int{1}, reason: ReasonAlignment},
		{name: "break after the last word", in: segInput("a b"), text: "a b.", breaks: []int{3}, reason: ReasonAlignment},
		{name: "too many breaks", in: segInput("ab"), text: "ab", breaks: []int{1, 1}, reason: ReasonAlignment},
		{name: "text far too long", in: segInput("a"), text: strings.Repeat(" ", 400) + "a", reason: ReasonTextChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, verr := SegmentMarks(tc.in, segOut(t, tc.text, tc.breaks...))
			if tc.reason != "" {
				if verr == nil || verr.Reason != tc.reason {
					t.Fatalf("驗證結果 %v，預期 %s", verr, tc.reason)
				}
				return
			}
			if verr != nil {
				t.Fatalf("不應該失敗：%v", verr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("marks = %+v，預期 %+v", got, tc.want)
			}
		})
	}
}

// 時間欄位不在 schema 中，Validate 以 schema 拒絕（SEG-R2）。
func TestSegmentRejectsTimestamps(t *testing.T) {
	in := segInput("hello world")
	for _, out := range []string{
		`{"text":"hello world","breaks":[],"timestamps":[{"start":1,"end":4}]}`,
		`{"text":"hello world","breaks":[],"start":"00:00:01"}`,
	} {
		if _, verr := SegmentSpec().Validate(in, []byte(out)); verr == nil || verr.Reason != ReasonSchema {
			t.Fatalf("%s：驗證結果 %v，預期 schema", out, verr)
		}
	}
	if _, verr := SegmentSpec().Validate(in, []byte(`{"text":"hello world","breaks":[]}`)); verr != nil {
		t.Fatal(verr)
	}
}

// 錯誤說明引用模型的文字時要跳脫、限制長度：它會附在重試的請求中。
func TestSegmentMessageIsBounded(t *testing.T) {
	long := strings.Repeat("x", 500)
	_, verr := SegmentMarks(segInput("a "+long), segOut(t, "a\"\nY"+long))
	if verr == nil || verr.Reason != ReasonTextChanged {
		t.Fatalf("驗證結果 %v", verr)
	}
	if utf8.RuneCountInString(verr.Message) > 300 || strings.Contains(verr.Message, "\n") {
		t.Fatalf("錯誤說明太長或含換行：%q", verr.Message)
	}
}

func TestKeepMarks(t *testing.T) {
	got := KeepMarks([]Cue{{I: 4, Text: "a"}, {I: 5, Text: "b"}})
	if want := []CueMarks{{I: 4}, {I: 5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

// FuzzSegmentMarks：任意輸入與輸出都不會 panic；通過時斷點都在句子內，而且對回之後的文字與輸入相同。
func FuzzSegmentMarks(f *testing.F) {
	f.Add("We use Kafka. It is fast.", "next", `{"text":"We use Kafka. It is fast. next","breaks":[14,26]}`)
	f.Add("大家好，今天", "拆分", `{"text":"大家好，今天拆分","breaks":[3,6]}`)
	f.Add("a", "", `{"text":"","breaks":[-5,99999999999]}`)
	f.Fuzz(func(t *testing.T, a, b, out string) {
		in := segInput(a, b)
		marks, verr := SegmentMarks(in, []byte(out))
		if verr != nil {
			return
		}
		if len(marks) != 2 || marks[0].Join {
			t.Fatalf("marks = %+v", marks)
		}
		for j, m := range marks {
			n := utf8.RuneCountInString(in.Cues[j].Text)
			prev := 0
			for _, c := range m.Cuts {
				if c <= prev || c >= n {
					t.Fatalf("cue %d 的斷點 %v 超出範圍（%d 個字元）", j, m.Cuts, n)
				}
				prev = c
			}
		}
	})
}
