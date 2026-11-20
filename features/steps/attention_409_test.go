package steps

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cucumber/godog"
)

// 新增被 409 in_trash 擋下之後，「shows the message」與「offers the actions」比對那個 409 回應，
// 不是另外查詳情的 404（兩者內容相同是 DEL-R10 的要求，但比對的對象要是回應本身）。
func TestAttentionStageUsesTheIngestConflictResponse(t *testing.T) {
	const ref = "yt:abc123xxxxx"
	s := newTestScenario(t)
	post := func() {
		t.Helper()
		if err := s.ownerSendsWith("POST /api/items", &godog.DocString{Content: `{"url":"https://www.youtube.com/watch?v=abc123xxxxx","until":"metadata"}`}); err != nil {
			t.Fatal(err)
		}
	}
	post()
	if err := s.ownerSends("DELETE /api/items/" + ref); err != nil {
		t.Fatal(err)
	}
	post()
	if s.last.status != http.StatusConflict {
		t.Fatalf("新增垃圾桶中的影音 = %d %s，預期 409", s.last.status, s.last.body)
	}
	var body map[string]any
	if err := json.Unmarshal(s.last.body, &body); err != nil {
		t.Fatal(err)
	}

	// 回應的內容改成另一個訊息與動作：斷言要看到它，代表比對的是這個回應。
	body["message"], body["actions"] = "custom", []string{"還原", "x"}
	s.last.body, _ = json.Marshal(body)
	if err := s.itemShowsMessage(ref, "custom"); err != nil {
		t.Errorf("應該比對 409 回應的訊息：%v", err)
	}
	if err := s.itemOffersActions(ref, "還原, x"); err != nil {
		t.Errorf("應該比對 409 回應的動作：%v", err)
	}

	// 409 是別部影音的：不採用，回到詳情的 404。
	body["item_id"] = "some-other-item"
	s.last.body, _ = json.Marshal(body)
	if err := s.itemShowsMessage(ref, "這部影音在垃圾桶中。"); err != nil {
		t.Errorf("別部影音的 409 不該採用：%v", err)
	}
	if err := s.itemOffersActions(ref, "還原"); err != nil {
		t.Errorf("別部影音的 409 不該採用：%v", err)
	}

	// 不是 in_trash 的 409 不採用。
	body["item_id"], body["code"] = s.World.Refs[ref], "conflict"
	s.last.body, _ = json.Marshal(body)
	if err := s.itemShowsMessage(ref, "這部影音在垃圾桶中。"); err != nil {
		t.Errorf("其他 409 不該採用：%v", err)
	}
}
