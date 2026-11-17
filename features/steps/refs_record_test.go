package steps

import (
	"net/http"
	"testing"

	"github.com/cucumber/godog"
)

// 以 `the owner sends "POST /api/items"` 新增影音時也要記下代號與 ID（World.Refs）：
// 永久刪除之後再新增同一部影音，影音換了新的 ID，Refs 不能還停在舊的。
func TestOwnerSendsRecordsRefsOfIngestedItems(t *testing.T) {
	const ref = "yt:abc123xxxxx"
	s := newTestScenario(t)
	add := func() {
		t.Helper()
		body := &godog.DocString{Content: `{"url":"https://www.youtube.com/watch?v=abc123xxxxx","until":"metadata"}`}
		if err := s.ownerSendsWith("POST /api/items", body); err != nil {
			t.Fatal(err)
		}
		if s.last.status != http.StatusCreated {
			t.Fatalf("新增 = %d %s", s.last.status, s.last.body)
		}
	}

	add()
	first := s.World.Refs[ref]
	if first == "" {
		t.Fatal("新增之後 World.Refs 沒有記下代號")
	}
	for _, spec := range []string{"DELETE /api/items/" + ref, "DELETE /api/trash/" + ref} {
		if err := s.ownerSends(spec); err != nil {
			t.Fatal(err)
		}
		if s.last.status != http.StatusOK {
			t.Fatalf("%s = %d %s", spec, s.last.status, s.last.body)
		}
	}

	add()
	second := s.World.Refs[ref]
	if second == "" || second == first {
		t.Errorf("永久刪除後再新增，World.Refs[%q] = %q，預期換成新的 ID（舊的 %q）", ref, second, first)
	}
	if id, err := s.itemID(ref); err != nil || id != second {
		t.Errorf("itemID = %q, %v，預期 %q", id, err, second)
	}

	// 失敗的新增（例如 409 垃圾桶中）不更新對照表。
	if err := s.ownerSends("DELETE /api/items/" + ref); err != nil {
		t.Fatal(err)
	}
	if err := s.ownerSendsWith("POST /api/items", &godog.DocString{Content: `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`}); err != nil {
		t.Fatal(err)
	}
	if s.last.status != http.StatusConflict || s.World.Refs[ref] != second {
		t.Errorf("409 之後 status = %d，Refs = %q，預期不變 %q", s.last.status, s.World.Refs[ref], second)
	}
}
