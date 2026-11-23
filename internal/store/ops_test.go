package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestAlertStatesLifecycle(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	t0 := clk.Now()

	if err := w.ActivateAlert(ctx, "disk_usage", "/data", "93%", t0); err != nil {
		t.Fatal(err)
	}
	if err := w.MarkAlertNotified(ctx, "disk_usage", "/data", t0); err != nil {
		t.Fatal(err)
	}
	if err := w.SetAlertMessage(ctx, "disk_usage", "/data", "95%"); err != nil {
		t.Fatal(err)
	}
	if err := w.ActivateAlert(ctx, "stuck_task", "item-1", "stuck", t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().AlertStates(ctx)
	if err != nil || len(got) != 2 || got[0].Kind != "disk_usage" || got[0].Message != "95%" ||
		got[0].NotifiedAt == nil || !got[0].Active() || !got[0].ActiveSince.Equal(t0) {
		t.Fatalf("AlertStates = %+v，%v", got, err)
	}

	at := t0.Add(time.Hour)
	if ok, err := w.ResolveAlert(ctx, "disk_usage", "/data", at); err != nil || !ok {
		t.Fatalf("ResolveAlert = %v，%v", ok, err)
	}
	if ok, _ := w.ResolveAlert(ctx, "disk_usage", "/data", at); ok {
		t.Error("已解除的告警不能再解除一次")
	}
	if ok, _ := w.ResolveAlert(ctx, "nope", "x", at); ok {
		t.Error("不存在的告警不能解除")
	}
	got, _ = s.Read().AlertStates(ctx)
	if len(got) != 2 || got[0].Kind != "stuck_task" || got[1].Active() || got[1].ResolvedAt == nil {
		t.Fatalf("解除後成立中的排在前面：%+v", got)
	}

	// 重新成立：清掉通知與解除的時間，從新的時間開始。
	again := t0.Add(2 * time.Hour)
	if err := w.ActivateAlert(ctx, "disk_usage", "/data", "91%", again); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Read().AlertStates(ctx)
	i := slices.IndexFunc(got, func(a AlertState) bool { return a.Kind == "disk_usage" })
	if a := got[i]; !a.Active() || a.NotifiedAt != nil || !a.ActiveSince.Equal(again) || a.Message != "91%" {
		t.Fatalf("重新成立 = %+v", a)
	}
}

func TestToolCallStats(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	now := clk.Now().Truncate(ToolCallBucket)

	for i, failed := range []bool{true, false, true} {
		if err := w.AddToolCall(ctx, "yt-dlp", now.Add(time.Duration(i)*time.Minute), failed); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddToolCall(ctx, "ffmpeg", now, true); err != nil {
		t.Fatal(err)
	}
	if err := w.SetToolCalls(ctx, "yt-dlp", now.Add(-time.Hour), 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := w.SetToolCalls(ctx, "yt-dlp", now.Add(-time.Hour), 4, 2); err != nil {
		t.Fatal(err)
	}
	calls, fails, err := s.Read().ToolCallTotals(ctx, "yt-dlp", now)
	if err != nil || calls != 3 || fails != 2 {
		t.Fatalf("最近一格 = %d/%d，%v", fails, calls, err)
	}
	calls, fails, _ = s.Read().ToolCallTotals(ctx, "yt-dlp", now.Add(-time.Hour))
	if calls != 7 || fails != 4 {
		t.Fatalf("含一小時前（SetToolCalls 取代）= %d/%d", fails, calls)
	}
	if n, err := w.PruneToolCallStats(ctx, now); err != nil || n != 1 {
		t.Fatalf("PruneToolCallStats = %d，%v", n, err)
	}
	if calls, _, _ := s.Read().ToolCallTotals(ctx, "yt-dlp", time.Time{}); calls != 3 {
		t.Errorf("清理後剩 %d 次", calls)
	}
}

func TestSecretFreshness(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	if err := w.UpsertSecret(ctx, "cookie", "youtube", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	if err := w.UpsertSecret(ctx, "service", "groq", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().SecretsFreshness(ctx, "cookie")
	if err != nil || len(got) != 1 || got[0].Name != "youtube" || got[0].LastOKAt != nil || !got[0].UpdatedAt.Equal(clk.Now()) {
		t.Fatalf("SecretsFreshness = %+v，%v", got, err)
	}
	ok := clk.Now().Add(time.Hour)
	if err := w.MarkSecretOK(ctx, "cookie", "youtube", ok); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Read().SecretsFreshness(ctx, "cookie")
	if got[0].LastOKAt == nil || !got[0].LastOKAt.Equal(ok) {
		t.Fatalf("MarkSecretOK 之後 = %+v", got)
	}
	old := clk.Now().Add(-30 * 24 * time.Hour)
	if err := w.SetSecretTimes(ctx, "cookie", "youtube", old); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Read().SecretsFreshness(ctx, "cookie")
	if !got[0].UpdatedAt.Equal(old) || !got[0].LastOKAt.Equal(old) {
		t.Fatalf("SetSecretTimes 之後 = %+v", got)
	}
	if err := w.SetSecretTimes(ctx, "cookie", "bilibili", old); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 cookie：%v", err)
	}
	// 重新上傳只改 updated_at，last_ok_at 保留。
	if err := w.UpsertSecret(ctx, "cookie", "youtube", []byte("n2"), []byte("c2")); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Read().SecretsFreshness(ctx, "cookie")
	if !got[0].UpdatedAt.Equal(clk.Now()) || !got[0].LastOKAt.Equal(old) {
		t.Fatalf("重新上傳之後 = %+v", got)
	}
}

func TestFailingScheduledJobs(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()
	now := clk.Now()
	for _, k := range []string{"backup.daily", "alerts"} {
		if err := w.ScheduleJob(ctx, k, "x", now); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.RecordScheduledJobRun(ctx, "backup.daily", "x", ScheduledJobRun{NextRunAt: now, LastRunAt: now, LastError: "boom", Failures: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read().FailingScheduledJobs(ctx)
	if err != nil || len(got) != 1 || got[0].Kind != "backup.daily" || got[0].LastError != "boom" {
		t.Fatalf("FailingScheduledJobs = %+v，%v", got, err)
	}
}

func TestActiveStageRuns(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	it, sr := seedItem(t, s, "aaa")
	_, done := seedItem(t, s, "bbb")
	_, noTask := seedItem(t, s, "ccc")
	nb := clk.Now().Add(time.Hour)
	newTask(t, s, sr, 1, nil)
	newTask(t, s, sr, 1, &nb)
	tk := newTask(t, s, done, 1, nil)
	_ = noTask

	// 完成的任務不算。
	got := claim(t, s, claimOpts("w1", 10))
	for _, c := range got {
		if c.ID == tk.ID {
			if err := s.Write().CompleteTask(ctx, c.ID, c.LeaseToken); err != nil {
				t.Fatal(err)
			}
		}
	}
	runs, err := s.Read().ActiveStageRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ItemID != it.ID || runs[0].Status != StatusRunning || runs[0].StartedAt == nil ||
		runs[0].TaskNotBefore == nil || !runs[0].TaskNotBefore.Equal(nb) || !slices.Equal(runs[0].Classes, []string{"download"}) {
		t.Fatalf("ActiveStageRuns = %+v", runs)
	}
}
