package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// chapInput 是句號 1、2、4、5 的 chunk（3 在 SponsorBlock 片段內，不在輸入中）。
func chapInput() ChunkInput {
	return ChunkInput{Chunks: 1, Cues: []Cue{{I: 1, Text: "a"}, {I: 2, Text: "b"}, {I: 4, Text: "c"}, {I: 5, Text: "d"}}}
}

func TestChaptersSpecReady(t *testing.T) {
	s := ChaptersSpec(0)
	if err := s.Ready(); err != nil || s.Stage != "chapters" {
		t.Fatalf("Ready = %v, Stage = %q", err, s.Stage)
	}
	if strings.Contains(s.OutputRules, "start") {
		t.Error("輸出規則不應要求任何時間")
	}
}

func TestCheckChapters(t *testing.T) {
	long := strings.Repeat("字", MaxChapterTitle+1)
	exact := strings.Repeat("字", MaxChapterTitle)
	cases := []struct {
		name, out, err string
		n              int
	}{
		{"ok", `{"chapters":[{"cue":2,"title":"  Toolchain "},{"cue":4,"title":"Boot"}]}`, "", 2},
		{"80 字剛好", `{"chapters":[{"cue":1,"title":"` + exact + `"}]}`, "", 1},
		{"cue 不在輸入", `{"chapters":[{"cue":2,"title":"a"},{"cue":99,"title":"b"}]}`, "chapters[1].cue 99 不在輸入中", 0},
		{"cue 在片段內", `{"chapters":[{"cue":2,"title":"a"},{"cue":3,"title":"b"}]}`, "chapters[1].cue 3 不在輸入中", 0},
		{"沒有遞增", `{"chapters":[{"cue":4,"title":"a"},{"cue":2,"title":"b"}]}`, "chapters[1].cue 必須遞增", 0},
		{"重複", `{"chapters":[{"cue":2,"title":"a"},{"cue":2,"title":"b"}]}`, "chapters[1].cue 必須遞增", 0},
		{"標題太長", `{"chapters":[{"cue":2,"title":"` + long + `"}]}`, "chapters[0].title 超過 80 字", 0},
		{"空標題", `{"chapters":[{"cue":2,"title":"   "}]}`, "chapters[0].title 是空的", 0},
		{"換行", `{"chapters":[{"cue":2,"title":"a\nb"}]}`, "chapters[0].title 不能含換行或控制字元", 0},
		{"雙向覆寫", `{"chapters":[{"cue":2,"title":"a‮b"}]}`, "chapters[0].title 不能含換行或控制字元", 0},
		{"沒有章節", `{"chapters":[]}`, "chapters 至少要有 1 個", 0},
		{"太多", `{"chapters":[{"cue":1,"title":"a"},{"cue":2,"title":"b"},{"cue":4,"title":"c"}]}`, "chapters 有 3 個，最多 2 個", 0},
	}
	for _, c := range cases {
		got, verr := CheckChapters(chapInput(), json.RawMessage(c.out), 2)
		if c.err == "" {
			if verr != nil || len(got) != c.n {
				t.Errorf("%s：%v %v", c.name, got, verr)
			}
			continue
		}
		if verr == nil || verr.Message != c.err || verr.Detail["error"] != c.err {
			t.Errorf("%s：verr = %+v，預期 %q", c.name, verr, c.err)
		}
	}
	got, _ := CheckChapters(chapInput(), json.RawMessage(`{"chapters":[{"cue":2,"title":"  Toolchain "}]}`), 20)
	if got[0].Title != "Toolchain" {
		t.Errorf("標題沒有去掉頭尾空白：%q", got[0].Title)
	}
}

// Validate 走 schema 與 Check：多出時間欄位是 schema 錯誤（ADR-0003）。
func TestChaptersValidateRejectsTimes(t *testing.T) {
	s := ChaptersSpec(20)
	if _, verr := s.Validate(chapInput(), []byte(`{"chapters":[{"cue":2,"title":"a","start":"00:05:00"}]}`)); verr == nil || verr.Reason != ReasonSchema {
		t.Errorf("verr = %+v", verr)
	}
	if _, verr := s.Validate(chapInput(), []byte(`{"chapters":[{"cue":2,"title":"a"}]}`)); verr != nil {
		t.Errorf("verr = %+v", verr)
	}
}

func FuzzCheckChapters(f *testing.F) {
	f.Add(`{"chapters":[{"cue":2,"title":"a"}]}`)
	f.Add(`{"chapters":[{"cue":-1,"title":"\u0000"}]}`)
	f.Add(`{"chapters":null}`)
	f.Fuzz(func(t *testing.T, out string) {
		got, verr := CheckChapters(chapInput(), json.RawMessage(out), 20)
		if verr != nil {
			return
		}
		last := 0
		for _, c := range got {
			if c.Cue <= last || strings.ContainsFunc(c.Title, isTitleControl) || c.Title == "" {
				t.Fatalf("通過驗證的章節不正確：%+v", got)
			}
			last = c.Cue
		}
	})
}
