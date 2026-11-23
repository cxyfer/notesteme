package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/pipeline"
)

// CHAP-R7：沒有章節產出時沒有 chapters；沒有章節時是空陣列；每個章節帶 origin，時間是 HH:MM:SS.mmm。
func TestDetailChapters(t *testing.T) {
	enc := func(d pipeline.Detail) string {
		b, err := json.Marshal(toDetail(d))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := enc(pipeline.Detail{}); strings.Contains(got, `"chapters"`) {
		t.Errorf("沒有產出不該有欄位：%s", got)
	}
	if got := enc(pipeline.Detail{Chapters: &pipeline.Chapters{Origin: "llm"}}); !strings.Contains(got, `"chapters":[]`) {
		t.Errorf("沒有章節要是空陣列：%s", got)
	}
	got := enc(pipeline.Detail{Chapters: &pipeline.Chapters{Origin: "platform", Chapters: []pipeline.Chapter{{Title: "Intro", StartMS: 0, EndMS: 300000, Cue: 2}}}})
	if !strings.Contains(got, `"chapters":[{"title":"Intro","start":"00:00:00.000","end":"00:05:00.000","origin":"platform"}]`) {
		t.Errorf("章節：%s", got)
	}
}
