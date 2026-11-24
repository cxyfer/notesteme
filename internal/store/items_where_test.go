package store

import (
	"os"
	"strings"
	"testing"
)

// ListItemsPage、CountItemsFiltered、ListItemKeysGrouped 的 WHERE 區塊必須完全相同：
// 否則 total 與列出的影音、分組與不分組的結果會不一致（LIB-R1、R4）。
func TestItemListWhereBlocksAreIdentical(t *testing.T) {
	b, err := os.ReadFile("queries/items.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	block := func(name, end string) string {
		t.Helper()
		i := strings.Index(sql, "-- name: "+name+" ")
		if i < 0 {
			t.Fatalf("沒有 %s", name)
		}
		q := sql[i:]
		w := strings.Index(q, "WHERE deleted_at IS NULL")
		e := strings.Index(q[w:], end)
		if w < 0 || e < 0 {
			t.Fatalf("%s 找不到 WHERE 區塊", name)
		}
		return q[w : w+e]
	}
	page := block("ListItemsPage", "\n  AND created_at <= ")
	count := block("CountItemsFiltered", ";")
	grouped := block("ListItemKeysGrouped", "\n)\nSELECT g.id")
	if len(page) < 500 {
		t.Fatalf("WHERE 區塊太短，擷取可能錯了：%q", page)
	}
	if page != count {
		t.Errorf("CountItemsFiltered 的 WHERE 與 ListItemsPage 不同")
	}
	if page != grouped {
		t.Errorf("ListItemKeysGrouped 的 WHERE 與 ListItemsPage 不同")
	}
}
