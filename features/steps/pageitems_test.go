package steps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// itemsServer 回傳一個依 cursor 查表的 GET /api/items 假伺服器。
// pages 的 key 是 cursor（第一頁為空字串），value 是該頁的影音 ref 與 next_cursor。
type fakePage struct {
	refs []string
	next string
}

func itemsServer(t *testing.T, total int, pages map[string]fakePage) (get func(string, any) error, calls *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n > 20 {
			http.Error(w, "請求次數過多：游標循環沒有被擋下", http.StatusInternalServerError)
			return
		}
		p, ok := pages[r.URL.Query().Get("cursor")]
		if !ok {
			http.Error(w, "未知的游標", http.StatusUnprocessableEntity)
			return
		}
		items := make([]map[string]string, 0, len(p.refs))
		for _, ref := range p.refs {
			items = append(items, map[string]string{"id": "id-" + ref, "ref": ref})
		}
		body := map[string]any{"items": items, "total": total}
		if p.next != "" {
			body["next_cursor"] = p.next
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return func(path string, out any) error {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}, &n
}

func refsOfItems(items []itemJSON) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Ref)
	}
	return strings.Join(out, ",")
}

func TestPageItemsReadsEveryPage(t *testing.T) {
	get, calls := itemsServer(t, 5, map[string]fakePage{
		"":   {refs: []string{"a", "b"}, next: "c1"},
		"c1": {refs: []string{"c", "d"}, next: "c2"},
		"c2": {refs: []string{"e"}},
	})
	items, err := pageItems(get)
	if err != nil {
		t.Fatal(err)
	}
	if got := refsOfItems(items); got != "a,b,c,d,e" || *calls != 3 {
		t.Errorf("items = %s，請求 %d 次", got, *calls)
	}
}

func TestPageItemsSinglePage(t *testing.T) {
	get, _ := itemsServer(t, 1, map[string]fakePage{"": {refs: []string{"a"}}})
	items, err := pageItems(get)
	if err != nil || refsOfItems(items) != "a" {
		t.Errorf("items = %v，err = %v", items, err)
	}
	empty, _ := itemsServer(t, 0, map[string]fakePage{"": {}})
	if items, err := pageItems(empty); err != nil || len(items) != 0 {
		t.Errorf("空列表：items = %v，err = %v", items, err)
	}
}

func TestPageItemsRejectsCursorLoops(t *testing.T) {
	tests := []struct {
		name  string
		pages map[string]fakePage
	}{
		{"A→A", map[string]fakePage{"": {refs: []string{"a"}, next: "A"}, "A": {refs: []string{"b"}, next: "A"}}},
		{"A→B→A", map[string]fakePage{
			"":  {refs: []string{"a"}, next: "A"},
			"A": {refs: []string{"b"}, next: "B"},
			"B": {refs: []string{"c"}, next: "A"},
		}},
	}
	for _, tt := range tests {
		get, calls := itemsServer(t, 100, tt.pages)
		if _, err := pageItems(get); err == nil || !strings.Contains(err.Error(), "next_cursor") {
			t.Errorf("%s：應該因游標循環失敗：%v", tt.name, err)
		}
		if *calls > 5 {
			t.Errorf("%s：循環要在幾次請求內被擋下：%d", tt.name, *calls)
		}
	}
}

func TestPageItemsRejectsMoreItemsThanTotal(t *testing.T) {
	get, _ := itemsServer(t, 2, map[string]fakePage{
		"":   {refs: []string{"a", "b"}, next: "c1"},
		"c1": {refs: []string{"c"}},
	})
	if _, err := pageItems(get); err == nil || !strings.Contains(err.Error(), "total") {
		t.Errorf("累積筆數超過 total 應該失敗：%v", err)
	}
}
