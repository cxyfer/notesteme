package pipeline

import (
	"context"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

func repeatTexts(cues []subtitle.Cue) []string {
	out := make([]string, len(cues))
	for i, c := range cues {
		out[i] = c.Text
	}
	return out
}

func textCues(texts ...string) []subtitle.Cue {
	out := make([]subtitle.Cue, len(texts))
	for i, t := range texts {
		out[i] = subtitle.Cue{Index: i + 1, StartMS: int64(i) * 1000, EndMS: int64(i+1) * 1000, Text: t}
	}
	return out
}

// SRC-R13：連續 3 次以上完全相同的句子只留第一句；兩次保留；交錯不算連續。
func TestCollapseRepeats(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"空", nil, []string{}},
		{"兩次保留", []string{"a", "Thanks.", "Thanks.", "b"}, []string{"a", "Thanks.", "Thanks.", "b"}},
		{"三次留一", []string{"a", "Thanks.", "Thanks.", "Thanks.", "b"}, []string{"a", "Thanks.", "b"}},
		{"五次在結尾", []string{"a", "x", "x", "x", "x", "x"}, []string{"a", "x"}},
		{"前後空白視為相同", []string{"x", " x", "x "}, []string{"x"}},
		{"大小寫不同不算相同", []string{"x", "X", "x"}, []string{"x", "X", "x"}},
		{"交錯", []string{"A", "B", "A", "B", "A", "B"}, []string{"A", "B", "A", "B", "A", "B"}},
		{"兩段各自計算", []string{"x", "x", "x", "y", "x", "x"}, []string{"x", "y", "x", "x"}},
		{"中間夾不同句", []string{"x", "x", "y", "x", "x"}, []string{"x", "x", "y", "x", "x"}},
	}
	for _, c := range cases {
		got := collapseRepeats(textCues(c.in...))
		if !slices.Equal(repeatTexts(got), c.want) {
			t.Errorf("%s：= %q，預期 %q", c.name, repeatTexts(got), c.want)
		}
		for i, cue := range got {
			if cue.Index != i+1 {
				t.Errorf("%s：Index 沒有重新編號：%d 在位置 %d", c.name, cue.Index, i)
			}
		}
	}
	// 留下的是第一句，時間不變。
	got := collapseRepeats(textCues("x", "x", "x"))
	if got[0].StartMS != 0 || got[0].EndMS != 1000 {
		t.Errorf("留下的 cue = %+v，要是第一句原本的時間", got[0])
	}
}

// 轉錄結果（單檔路徑）也會合併連續重複。
func TestTranscribeASRCollapsesRepeats(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 1 * miB
	g := &fakeGroq{respond: func(int, string, string) (*groq.Result, error) {
		return &groq.Result{Lang: "en", Cues: textCues("hello", "Thank you.", "Thank you.", "Thank you.")}, nil
	}}
	res, err := transcribeASR(context.Background(), tool, g, in, dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hello", "Thank you."}; !slices.Equal(repeatTexts(res.Cues), want) {
		t.Errorf("cues = %q，預期 %q", repeatTexts(res.Cues), want)
	}
}
