package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/app"
)

// 兩頁的假 API：第一頁帶 next_cursor，第二頁沒有。
func pagedItemsHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/items" {
		writeJSON(w, 200, `{"id":"i3","ref":"yt:ccccccccccc","title":"C","stages":[]}`)
		return
	}
	switch r.URL.Query().Get("cursor") {
	case "":
		writeJSON(w, 200, `{"items":[{"id":"i1","ref":"yt:aaaaaaaaaaa","title":"A"},{"id":"i2","ref":"yt:bbbbbbbbbbb","title":"B"}],"total":3,"next_cursor":"CUR1"}`)
	case "CUR1":
		writeJSON(w, 200, `{"items":[{"id":"i3","ref":"yt:ccccccccccc","title":"C"}],"total":3}`)
	default:
		writeJSON(w, 422, `{"code":"invalid_request","message":"游標不正確或已失效，請從第一頁重新讀取。"}`)
	}
}

func TestItemsLsFollowsNextCursor(t *testing.T) {
	api, url := newFakeAPI(t, pagedItemsHandler)
	res, out, errOut := run(t, app.Env{}, "items", "ls", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	want := "yt:aaaaaaaaaaa\tA\nyt:bbbbbbbbbbb\tB\nyt:ccccccccccc\tC\n"
	if out != want {
		t.Errorf("輸出 = %q，預期 %q", out, want)
	}
	if len(api.requests) != 2 || api.requests[1] != "GET /api/items?cursor=CUR1&limit=200" {
		t.Errorf("請求 = %v", api.requests)
	}

	// --json：多頁時合併成同樣形狀的 JSON。
	res, out, errOut = run(t, app.Env{}, "items", "ls", "--json", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("--json：%+v %s", res, errOut)
	}
	const merged = `{"items":[{"id":"i1","ref":"yt:aaaaaaaaaaa","title":"A"},{"id":"i2","ref":"yt:bbbbbbbbbbb","title":"B"},{"id":"i3","ref":"yt:ccccccccccc","title":"C"}],"total":3}`
	if strings.TrimSpace(out) != merged {
		t.Errorf("--json 輸出 = %s", out)
	}
}

func TestItemsLsForwardsFilters(t *testing.T) {
	api, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"items":[],"total":0}`)
	})
	res, _, errOut := run(t, app.Env{}, "items", "ls", "--status", "normalize=done,transcript:failed", "--channel", "ch1", "--server", url, "--token", "t")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v %s", res, errOut)
	}
	// = 在送出前轉成 API 的 :。
	want := "GET /api/items?channel=ch1&limit=200&status=normalize%3Adone&status=transcript%3Afailed"
	if len(api.requests) != 1 || api.requests[0] != want {
		t.Errorf("請求 = %v，預期 %s", api.requests, want)
	}
}

// 代號在第二頁也找得到；每一頁都要讀，直到找到或沒有下一頁。
func TestResolveItemSearchesLaterPages(t *testing.T) {
	api, url := newFakeAPI(t, pagedItemsHandler)
	res, out, errOut := run(t, app.Env{}, "items", "show", "yt:ccccccccccc", "--json", "--server", url, "--token", "t")
	if res.Code != 0 || !strings.Contains(out, `"id":"i3"`) {
		t.Fatalf("結果 = %+v %q %s", res, out, errOut)
	}
	if len(api.requests) != 3 {
		t.Errorf("請求 = %v", api.requests)
	}
	res, _, _ = run(t, app.Env{}, "items", "show", "yt:zzzzzzzzzzz", "--server", url, "--token", "t")
	if res.Code != 1 || res.ErrorCode != "not_found" {
		t.Errorf("讀完所有頁面都沒有：%+v", res)
	}
}

// server 一直回傳同一個游標時要停下來，不能無窮迴圈。
func TestItemsLsStopsOnRepeatedCursor(t *testing.T) {
	_, url := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"items":[],"total":0,"next_cursor":"SAME"}`)
	})
	res, _, _ := run(t, app.Env{}, "items", "ls", "--server", url, "--token", "t")
	if res.Code == 0 {
		t.Errorf("重複的游標應該失敗：%+v", res)
	}
}
