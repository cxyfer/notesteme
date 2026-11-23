package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func noteIn() ChunkInput {
	return ChunkInput{Cues: []Cue{{I: 1, Text: "a"}, {I: 2, Text: "b"}, {I: 4, Text: "d"}, {I: 5, Text: "e"}}}
}

func TestNoteSpecsAreStrict(t *testing.T) {
	for _, s := range []*Spec{NoteSpec(NoteRefs{}), NoteMapSpec()} {
		if err := s.Ready(); err != nil {
			t.Fatal(err)
		}
	}
	if NoteSpec(NoteRefs{}).ToolName() != "notesteme_note" || NoteMapSpec().ToolName() != "notesteme_note_map" {
		t.Error("tool 名稱")
	}
}

func TestCheckNote(t *testing.T) {
	frames := map[int]bool{1: true, 3: true}
	ok := `{"sections":[{"level":2,"title":" A ","cue":2,"body":"x [[cue:5]]","frames":[1]},{"level":3,"title":"B","cue":4,"body":"","frames":[3]},{"level":2,"title":"C","cue":4,"body":"","frames":[]}],"self_check":["q"]}`
	o, verr := CheckNote(noteIn(), json.RawMessage(ok), NoteRefs{Frames: frames})
	if verr != nil || o.Sections[0].Title != "A" {
		t.Fatalf("%v %+v", verr, o)
	}
	cases := map[string]string{
		`{"sections":[],"self_check":["q"]}`:                                                      "sections 至少要有 1 節",
		`{"sections":[{"level":3,"title":"A","cue":2,"body":"","frames":[]}],"self_check":["q"]}`: "sections[0].level 必須是 2",
		`{"sections":[{"level":2,"title":"A","cue":2,"body":"","frames":[]},{"level":4,"title":"A","cue":2,"body":"","frames":[]}],"self_check":["q"]}`:   "sections[1].level 只能是 2 或 3",
		`{"sections":[{"level":2,"title":"A","cue":2,"body":"","frames":[]},{"level":2,"title":"B","cue":999,"body":"","frames":[]}],"self_check":["q"]}`: "sections[1].cue 999 不在輸入中",
		`{"sections":[{"level":2,"title":"A","cue":4,"body":"","frames":[]},{"level":2,"title":"B","cue":2,"body":"","frames":[]}],"self_check":["q"]}`:   "sections[1].cue 2 比同一層的前一節（4）早",
		`{"sections":[{"level":2,"title":" ","cue":2,"body":"","frames":[]}],"self_check":["q"]}`:                                                         "sections[0].title 是空的",
		`{"sections":[{"level":2,"title":"a\nb","cue":2,"body":"","frames":[]}],"self_check":["q"]}`:                                                      "sections[0].title 不能含換行或控制字元",
		`{"sections":[{"level":2,"title":"A","cue":2,"body":"[[cue:3]]","frames":[]}],"self_check":["q"]}`:                                                "sections[0].body 引用的 cue 3 不在輸入中",
		`{"sections":[{"level":2,"title":"A","cue":2,"body":"","frames":[2]}],"self_check":["q"]}`:                                                        "sections[0].frames[0] 2 不是選中的畫面",
		`{"sections":[{"level":2,"title":"A","cue":2,"body":"","frames":[1]},{"level":2,"title":"B","cue":4,"body":"","frames":[1]}],"self_check":["q"]}`: "sections[1].frames[0] 1 已經引用過",
		`{"sections":[{"level":2,"title":"A","cue":2,"body":"","frames":[]}],"self_check":[" "]}`:                                                         "self_check 至少要有 1 項",
		`{"sections":[{"level":2,"title":"` + strings.Repeat("x", 121) + `","cue":2,"body":"","frames":[]}],"self_check":["q"]}`:                          "sections[0].title 超過 120 字",
	}
	for out, want := range cases {
		_, verr := CheckNote(noteIn(), json.RawMessage(out), NoteRefs{Frames: frames})
		if verr == nil || verr.Message != want || verr.Detail["error"] != want {
			t.Errorf("%s\n  得到 %v\n  預期 %q", out, verr, want)
		}
	}
	// reduce：可以引用的句號來自原本的輸入，不是這次的 cues。
	refs := NoteRefs{Cues: map[int]bool{7: true}}
	if _, verr := CheckNote(noteIn(), json.RawMessage(`{"sections":[{"level":2,"title":"A","cue":7,"body":"","frames":[]}],"self_check":["q"]}`), refs); verr != nil {
		t.Errorf("reduce 的句號：%v", verr)
	}
}

func TestCheckNoteMap(t *testing.T) {
	if _, verr := CheckNoteMap(noteIn(), json.RawMessage(`{"points":[{"cue":1,"text":"x"}]}`)); verr != nil {
		t.Fatal(verr)
	}
	for out, want := range map[string]string{
		`{"points":[]}`:                     "points 至少要有 1 項",
		`{"points":[{"cue":3,"text":"x"}]}`: "points[0].cue 3 不在輸入中",
		`{"points":[{"cue":1,"text":" "}]}`: "points[0].text 是空的",
	} {
		if _, verr := CheckNoteMap(noteIn(), json.RawMessage(out)); verr == nil || verr.Message != want {
			t.Errorf("%s：%v", out, verr)
		}
	}
}

func TestBuildFixedNoteLang(t *testing.T) {
	f, err := BuildFixed(NoteSpec(NoteRefs{}), "語言：{{note_lang}}", TemplateVars{NoteLang: "zh-Hant-TW"},
		Metadata{Title: "t", Frames: []NoteFrame{{N: 1, Cue: 2, Caption: "Toolchain diagram"}}, NoteChapters: []NoteChapter{{Cue: 2, Title: "Setting up"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.System, "語言：zh-Hant-TW") || !strings.Contains(f.Prefix, `"frames":[{"n":1,"cue":2,"caption":"Toolchain diagram"}]`) {
		t.Errorf("%+v", f)
	}
	g, _ := BuildFixed(ChaptersSpec(0), "x", TemplateVars{}, Metadata{Title: "t"})
	if strings.Contains(g.Prefix, "frames") || strings.Contains(g.Prefix, "note_chapters") || strings.Contains(g.Prefix, "translations") {
		t.Errorf("其他步驟的 <video_metadata> 不應有筆記的欄位：%s", g.Prefix)
	}
}
