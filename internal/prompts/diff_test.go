package prompts

import (
	"reflect"
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	got := Diff("a\nb\nc\n", "a\nB\nc\nd\n")
	want := []DiffLine{{DiffEqual, "a"}, {DiffDelete, "b"}, {DiffInsert, "B"}, {DiffEqual, "c"}, {DiffInsert, "d"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff = %v，預期 %v", got, want)
	}
	if got := Diff("same\n", "same\n"); len(got) != 1 || got[0].Op != DiffEqual {
		t.Errorf("內容相同 = %v", got)
	}
	if got := Diff("", ""); len(got) != 0 {
		t.Errorf("都是空的 = %v", got)
	}
	if got := Diff("", "x"); !reflect.DeepEqual(got, []DiffLine{{DiffInsert, "x"}}) {
		t.Errorf("從空到有 = %v", got)
	}
}

// 超過上限時不做 LCS，退成整段刪除再整段新增，而且仍然可以還原 a 與 b。
func TestDiffTooLargeFallsBack(t *testing.T) {
	a := strings.Repeat("x\n", maxDiffLines+1) + "tail-a\n"
	b := strings.Repeat("y\n", maxDiffLines+1) + "tail-b\n"
	checkReconstruct(t, a, b)
}

func checkReconstruct(t *testing.T, a, b string) {
	t.Helper()
	var ra, rb []string
	for _, l := range Diff(a, b) {
		switch l.Op {
		case DiffEqual:
			ra, rb = append(ra, l.Text), append(rb, l.Text)
		case DiffDelete:
			ra = append(ra, l.Text)
		case DiffInsert:
			rb = append(rb, l.Text)
		default:
			t.Fatalf("不認得的 op %q", l.Op)
		}
	}
	if !reflect.DeepEqual(ra, splitLines(a)) || !reflect.DeepEqual(rb, splitLines(b)) {
		t.Fatalf("diff 無法還原 a 與 b：a=%q b=%q", a, b)
	}
}

func FuzzDiff(f *testing.F) {
	f.Add("a\nb", "a\nc")
	f.Add("", "x\n\n")
	f.Add("{{glossary}}\n你好", "你好\n{{glossary}}")
	f.Fuzz(func(t *testing.T, a, b string) {
		if len(a) > 4000 || len(b) > 4000 {
			return
		}
		checkReconstruct(t, a, b)
	})
}
