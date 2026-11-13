package app

import (
	"testing"
)

// 取消執行中的影音：回應 200 時任務已經結束、步驟為 skipped；再取消一次仍是 200；不存在的影音是 404（PIPE-R15）。
func TestCancelItemStopsRunningWork(t *testing.T) {
	_, srv, _, id := startBlocked(t)
	c := login(t, srv)

	var d detail
	if code := c.json("POST", "/api/items/"+id+"/cancel", "", &d); code != 200 {
		t.Fatalf("POST cancel = %d", code)
	}
	// 回應時執行中的任務已經結束：worker 不再有執行中的工作，佇列也沒有未結束的任務。
	if err := srv.WaitWorker(); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"metadata", "transcript", "normalize"} {
		if got := d.status(stage); got != "skipped" {
			t.Errorf("%s = %q，預期 skipped：%+v", stage, got, d)
		}
	}
	var q struct {
		Tasks []struct{ State string } `json:"tasks"`
	}
	if code := c.json("GET", "/api/queue", "", &q); code != 200 || len(q.Tasks) != 0 {
		t.Errorf("GET /api/queue = %d，任務 %+v，預期沒有未結束的任務", code, q.Tasks)
	}

	if code := c.json("POST", "/api/items/"+id+"/cancel", "", &d); code != 200 {
		t.Errorf("重複取消 = %d，預期 200（冪等）", code)
	}
	if code := c.json("POST", "/api/items/no-such-item/cancel", "", &d); code != 404 {
		t.Errorf("取消不存在的影音 = %d，預期 404", code)
	}
}
