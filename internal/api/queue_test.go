package api

import (
	"context"
	"testing"
)

func queueOf(t *testing.T, r *itemsRig) QueueStatus {
	t.Helper()
	rec := r.owner("GET", "/api/queue", "")
	if rec.Code != 200 {
		t.Fatalf("GET /api/queue = %d %s", rec.Code, rec.Body)
	}
	return decode[QueueStatus](t, rec)
}

func classOf(t *testing.T, q QueueStatus, class string) QueueClass {
	t.Helper()
	for _, c := range q.Classes {
		if c.Class == class {
			return c
		}
	}
	t.Fatalf("佇列狀態沒有資源類別 %s：%+v", class, q.Classes)
	return QueueClass{}
}

func TestQueueLimitsPatchOnlyChangesGivenClasses(t *testing.T) {
	r := newItemsRig(t)
	q := queueOf(t, r)
	if c := classOf(t, q, "download"); c.Limit != 2 || c.Paused || q.Paused {
		t.Fatalf("預設狀態：%+v", q)
	}

	rec := r.owner("PATCH", "/api/queue/limits", `{"download": 5}`)
	if rec.Code != 200 {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	q = queueOf(t, r)
	if classOf(t, q, "download").Limit != 5 || classOf(t, q, "asr").Limit != 3 || classOf(t, q, "llm").Limit != 4 {
		t.Errorf("只應改 download：%+v", q.Classes)
	}
}

func TestQueueLimitsRejectBadInput(t *testing.T) {
	r := newItemsRig(t)
	for name, body := range map[string]string{
		"不認識的類別":    `{"gpu": 2}`,
		"local 不開放": `{"local": 2}`,
		"零":         `{"download": 0}`,
		"負數":        `{"download": -3}`,
		"不是整數":      `{"download": "many"}`,
	} {
		rec := r.owner("PATCH", "/api/queue/limits", body)
		if rec.Code != 422 {
			t.Errorf("%s：status = %d %s", name, rec.Code, rec.Body)
		}
	}
	if c := classOf(t, queueOf(t, r), "download"); c.Limit != 2 {
		t.Errorf("被拒絕的請求不應改動設定：%+v", c)
	}
}

func TestQueuePauseAndResume(t *testing.T) {
	r := newItemsRig(t)

	// 沒有 body：整個佇列。
	if rec := r.owner("POST", "/api/queue/pause", ""); rec.Code != 200 {
		t.Fatalf("pause = %d %s", rec.Code, rec.Body)
	}
	if q := queueOf(t, r); !q.Paused {
		t.Fatalf("整個佇列應暫停：%+v", q)
	}
	if rec := r.owner("POST", "/api/queue/resume", ""); rec.Code != 200 {
		t.Fatalf("resume = %d %s", rec.Code, rec.Body)
	}
	if q := queueOf(t, r); q.Paused {
		t.Fatalf("整個佇列應恢復：%+v", q)
	}

	// 指定類別：只作用在該類別。
	if rec := r.owner("POST", "/api/queue/pause", `{"class":"asr"}`); rec.Code != 200 {
		t.Fatalf("pause asr = %d %s", rec.Code, rec.Body)
	}
	q := queueOf(t, r)
	if q.Paused || !classOf(t, q, "asr").Paused || classOf(t, q, "download").Paused {
		t.Fatalf("只應暫停 asr：%+v", q)
	}
	if rec := r.owner("POST", "/api/queue/resume", `{"class":"asr"}`); rec.Code != 200 {
		t.Fatalf("resume asr = %d %s", rec.Code, rec.Body)
	}
	if q := queueOf(t, r); classOf(t, q, "asr").Paused {
		t.Fatalf("asr 應恢復：%+v", q)
	}

	// 不認識的類別：422。
	for _, path := range []string{"/api/queue/pause", "/api/queue/resume"} {
		if rec := r.owner("POST", path, `{"class":"gpu"}`); rec.Code != 422 {
			t.Errorf("%s 不認識的類別 = %d %s", path, rec.Code, rec.Body)
		}
	}
}

// 狀態頁列出任務與影音代號；pending 的第一筆就是下一個會被領取的任務。
func TestQueueListsTasksWithItemsAndCounts(t *testing.T) {
	r := newItemsRig(t)
	for _, id := range []string{"abc123", "def456"} {
		if rec := r.owner("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=`+id+`"}`); rec.Code != 201 {
			t.Fatalf("新增 %s：%d %s", id, rec.Code, rec.Body)
		}
	}
	q := queueOf(t, r)
	if len(q.Tasks) != 2 || q.Tasks[0].Item != "yt:abc123" || q.Tasks[0].State != "pending" ||
		q.Tasks[0].Stage != "metadata" || q.Tasks[0].ResourceClass != "download" || q.Tasks[0].ItemID == "" {
		t.Fatalf("tasks = %+v", q.Tasks)
	}
	if c := classOf(t, q, "download"); c.Pending != 2 || c.Running != 0 {
		t.Errorf("download = %+v", c)
	}

	n, err := r.worker.RunRound(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("RunRound = %d, %v", n, err)
	}
	if err := r.worker.Wait(); err != nil {
		t.Fatal(err)
	}
	// 下游任務（transcript 等）建立之後也在清單中。
	for _, task := range queueOf(t, r).Tasks {
		if task.State != "pending" || task.Stage == "metadata" {
			t.Errorf("metadata 做完之後只剩下游的 pending 任務：%+v", task)
		}
	}
}

func TestItemDetailShowsAttempts(t *testing.T) {
	r := newItemsRig(t)
	rec := r.owner("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123"}`)
	id := decode[itemsBody](t, rec).Items[0].ID
	detail := decode[ItemDetail](t, r.owner("GET", "/api/items/"+id, ""))
	if detail.Stages[0].Attempts != 0 {
		t.Errorf("還沒領取：attempts = %d", detail.Stages[0].Attempts)
	}
	if err := r.worker.RunUntilIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail = decode[ItemDetail](t, r.owner("GET", "/api/items/"+id, ""))
	if detail.Stages[0].Attempts != 1 {
		t.Errorf("做完一次：attempts = %d，預期 1", detail.Stages[0].Attempts)
	}
}
