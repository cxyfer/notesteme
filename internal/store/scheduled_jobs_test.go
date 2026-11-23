package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestScheduledJobsRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	now := clk.Now()
	kinds := []string{"a", "b"}

	if _, ok, err := s.Read().NextScheduledJobAt(ctx, kinds); err != nil || ok {
		t.Fatalf("沒有工作時 ok = %v，%v", ok, err)
	}
	// 同一個時間到期：依建立順序（rowid）。
	for _, k := range []struct{ kind, key string }{{"b", "2"}, {"a", "1"}, {"c", "x"}} {
		if err := w.ScheduleJob(ctx, k.kind, k.key, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.ScheduleJob(ctx, "a", "later", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	due, err := s.Read().DueScheduledJobs(ctx, kinds, now, 10)
	if err != nil || len(due) != 2 || due[0].Key != "2" || due[1].Key != "1" {
		t.Fatalf("到期的工作 = %+v，%v（不含未登記的 kind c 與還沒到期的）", due, err)
	}
	if due, _ := s.Read().DueScheduledJobs(ctx, kinds, now, 1); len(due) != 1 {
		t.Errorf("max = 1：%+v", due)
	}
	if due, _ := s.Read().DueScheduledJobs(ctx, nil, now, 10); len(due) != 0 {
		t.Errorf("沒有登記任何 kind：%+v", due)
	}
	if at, ok, err := s.Read().NextScheduledJobAt(ctx, kinds); err != nil || !ok || !at.Equal(now) {
		t.Errorf("NextScheduledJobAt = %v %v %v", at, ok, err)
	}
	if at, ok, _ := s.Read().NextScheduledJobAt(ctx, []string{"c"}); !ok || !at.Equal(now) {
		t.Errorf("c：%v %v", at, ok)
	}

	// 寫回結果；再次排定只改 next_run_at，保留紀錄。
	ok := now
	if err := w.RecordScheduledJobRun(ctx, "a", "1", ScheduledJobRun{NextRunAt: now.Add(time.Minute), LastRunAt: now, LastError: "boom", Failures: 2, LastOKAt: &ok}); err != nil {
		t.Fatal(err)
	}
	if created, err := w.ScheduleJobIfAbsent(ctx, "a", "1", now.Add(time.Hour)); err != nil || created {
		t.Errorf("已存在時不新增：%v %v", created, err)
	}
	clk.Advance(time.Second)
	if err := w.ScheduleJob(ctx, "a", "1", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	j, err := s.Read().ScheduledJob(ctx, "a", "1")
	if err != nil || !j.NextRunAt.Equal(now.Add(2*time.Minute)) || j.LastError != "boom" || j.Failures != 2 ||
		j.LastRunAt == nil || !j.LastRunAt.Equal(now) || j.LastOKAt == nil || !j.CreatedAt.Equal(now) {
		t.Fatalf("ScheduledJob = %+v，%v", j, err)
	}
	if err := w.RecordScheduledJobRun(ctx, "a", "1", ScheduledJobRun{NextRunAt: now, LastRunAt: now}); err != nil {
		t.Fatal(err)
	}
	if j, _ := s.Read().ScheduledJob(ctx, "a", "1"); j.LastError != "" || j.LastOKAt != nil {
		t.Errorf("清空錯誤與成功時間：%+v", j)
	}

	if deleted, err := w.DeleteScheduledJob(ctx, "a", "1"); err != nil || !deleted {
		t.Fatalf("刪除：%v %v", deleted, err)
	}
	if _, err := s.Read().ScheduledJob(ctx, "a", "1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("刪除後 err = %v", err)
	}
	if err := w.RecordScheduledJobRun(ctx, "a", "1", ScheduledJobRun{NextRunAt: now, LastRunAt: now}); !errors.Is(err, ErrNotFound) {
		t.Errorf("寫回已刪除的工作：%v", err)
	}
}
