package store

import (
	"context"
	"errors"
	"testing"
)

func TestNoteTemplateVersions(t *testing.T) {
	st, _ := openTest(t)
	ctx := context.Background()
	r := st.Write()
	if _, err := r.LatestNoteTemplate(ctx, "mine"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在：%v", err)
	}
	if _, err := r.AddNoteTemplateVersion(ctx, false, NoteTemplate{ID: "mine", Body: "x", RulesJSON: "{}", SHA256: "s"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("修改不存在的模板：%v", err)
	}
	v1, err := r.AddNoteTemplateVersion(ctx, true, NoteTemplate{ID: "mine", Name: "Mine", Body: "a", RulesJSON: "{}", SHA256: "s1"})
	if err != nil || v1.Version != 1 || v1.Body != "a" || v1.Name != "Mine" {
		t.Fatalf("v1 = %+v, %v", v1, err)
	}
	if _, err := r.AddNoteTemplateVersion(ctx, true, NoteTemplate{ID: "mine", Body: "b", RulesJSON: "{}", SHA256: "s"}); !errors.Is(err, ErrNoteTemplateExists) {
		t.Fatalf("重複建立：%v", err)
	}
	v2, err := r.AddNoteTemplateVersion(ctx, false, NoteTemplate{ID: "mine", Body: "b", RulesJSON: "{}", SHA256: "s2"})
	if err != nil || v2.Version != 2 {
		t.Fatalf("v2 = %+v, %v", v2, err)
	}
	if _, err := r.AddNoteTemplateVersion(ctx, true, NoteTemplate{ID: "other", Body: "o", RulesJSON: "{}", SHA256: "o"}); err != nil {
		t.Fatal(err)
	}
	list, err := r.NoteTemplates(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "mine" || list[0].Version != 2 || list[1].ID != "other" {
		t.Fatalf("列表 = %+v, %v", list, err)
	}
	if old, err := r.NoteTemplateVersion(ctx, "mine", 1); err != nil || old.Body != "a" {
		t.Fatalf("舊版本 = %+v, %v", old, err)
	}
}
