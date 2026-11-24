package api

import (
	"context"
	"testing"
)

func stageOf(t *testing.T, d ItemDetail, stage string) Stage {
	t.Helper()
	for _, s := range d.Stages {
		if s.Stage == stage {
			return s
		}
	}
	t.Fatalf("詳情沒有步驟 %s：%+v", stage, d.Stages)
	return Stage{}
}

// 單一任務的取消與重試（POST /api/tasks/:id/cancel、retry）：回應是影音的最新詳情；
// 被取消的步驟出現在佇列的 failed 清單，用它的 id 重試後回到 pending 並從清單消失。
func TestTaskCancelAndRetryRoutes(t *testing.T) {
	r := newItemsRig(t)
	if rec := r.owner("POST", "/api/items", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`); rec.Code != 201 {
		t.Fatalf("新增：%d %s", rec.Code, rec.Body)
	}
	q := queueOf(t, r)
	if len(q.Tasks) != 1 || len(q.Failed) != 0 {
		t.Fatalf("佇列 = %+v", q)
	}
	task := q.Tasks[0]

	rec := r.owner("POST", "/api/tasks/"+task.ID+"/cancel", "")
	if rec.Code != 200 {
		t.Fatalf("cancel = %d %s", rec.Code, rec.Body)
	}
	if st := stageOf(t, decode[ItemDetail](t, rec), "metadata"); st.Status != "skipped" || st.BlockReason != "cancelled" {
		t.Errorf("取消後 metadata = %+v", st)
	}
	q = queueOf(t, r)
	if len(q.Tasks) != 0 || len(q.Failed) != 1 || q.Failed[0].ID != task.ID || q.Failed[0].Status != "skipped" ||
		q.Failed[0].BlockReason != "cancelled" || q.Failed[0].Item != "yt:abc123xxxxx" || q.Failed[0].Stage != "metadata" || q.Failed[0].FinishedAt == nil {
		t.Fatalf("取消後的佇列 = %+v", q)
	}
	// 冪等。
	if rec := r.owner("POST", "/api/tasks/"+task.ID+"/cancel", ""); rec.Code != 200 {
		t.Errorf("再取消一次 = %d %s", rec.Code, rec.Body)
	}

	rec = r.owner("POST", "/api/tasks/"+q.Failed[0].ID+"/retry", "")
	if rec.Code != 200 {
		t.Fatalf("retry = %d %s", rec.Code, rec.Body)
	}
	if st := stageOf(t, decode[ItemDetail](t, rec), "metadata"); st.Status != "pending" {
		t.Errorf("重試後 metadata = %+v", st)
	}
	q = queueOf(t, r)
	if len(q.Failed) != 0 || len(q.Tasks) != 1 || q.Tasks[0].ID == task.ID || q.Tasks[0].Attempts != 0 {
		t.Fatalf("重試後的佇列 = %+v", q)
	}
	// 已經在等待：409。
	rec = r.owner("POST", "/api/tasks/"+task.ID+"/retry", "")
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 409 || e.Code != "task_not_retryable" || e.Message == "" {
		t.Errorf("重複重試 = %d %s", rec.Code, rec.Body)
	}

	if _, err := r.worker.RunRound(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.worker.Wait(); err != nil {
		t.Fatal(err)
	}
	// 已經完成：不能取消。
	rec = r.owner("POST", "/api/tasks/"+q.Tasks[0].ID+"/cancel", "")
	if e := decodeError(t, rec.Body.Bytes()); rec.Code != 409 || e.Code != "task_not_cancellable" {
		t.Errorf("取消已完成的任務 = %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/tasks/no-such/retry", "/api/tasks/no-such/cancel"} {
		rec := r.owner("POST", path, "")
		if e := decodeError(t, rec.Body.Bytes()); rec.Code != 404 || e.Code != CodeNotFound || e.Message != "找不到這個任務。" {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body)
		}
	}
}
