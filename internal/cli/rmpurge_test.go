package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// items rm --purge 給不帶分P 的 Bilibili 代號時，符合的是這個影片所有的分P，
// 不論它在媒體庫還是垃圾桶中：p3 在媒體庫，p1、p2 已在垃圾桶，三個都要永久刪除。
func TestRmPurgeBareBilibiliCoversLibraryAndTrash(t *testing.T) {
	api, url := delAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/items":
			writeJSON(w, 200, `{"items":[{"id":"l3","ref":"bili:BV2yy222y2y/p3","title":"L3"}],"total":1}`)
			return true
		case r.Method == "DELETE" && (r.URL.Path == "/api/items/t2" || r.URL.Path == "/api/items/t3"):
			writeJSON(w, 404, `{"code":"in_trash","message":"這部影音在垃圾桶中。"}`)
			return true
		}
		return false
	})
	res, out, errOut := run(t, app.Env{}, "items", "rm", "bili:BV2yy222y2y", "--purge", "--yes", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	eq(t, mutations(api), []string{
		"DELETE /api/items/l3", "DELETE /api/trash/l3",
		"DELETE /api/items/t2", "DELETE /api/trash/t2",
		"DELETE /api/items/t3", "DELETE /api/trash/t3",
	})
	for _, ref := range []string{"bili:BV2yy222y2y/p1", "bili:BV2yy222y2y/p2", "bili:BV2yy222y2y/p3"} {
		if !strings.Contains(out, "已永久刪除："+ref+"\n") {
			t.Errorf("輸出缺少 %s：%q", ref, out)
		}
	}
}
