package app

import (
	"context"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/scheduler"
)

// 排程器接在 worker 的 periodics 上（ADR-0021 第 1 點）：登記的 kind 到期時，RunUntilIdle 會執行它；
// 註冊表是空的時候（M4-S0 的預設）不影響 worker 的喚醒時間。
func TestSchedulerRunsInWorkerRounds(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	clk := env.Clock.(*clock.Fake)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	if got := srv.scheduler.Kinds(); len(got) != 0 {
		t.Fatalf("M4-S0 的註冊表應該是空的：%v", got)
	}
	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := srv.scheduler.NextAt(t.Context()); ok || err != nil {
		t.Fatalf("空的註冊表不應排定喚醒：%v %v", ok, err)
	}

	// 另一個 server：在第一輪之前登記一個測試用的 kind。
	env = testEnv(t)
	env.Test.ManualWorker = true
	clk = env.Clock.(*clock.Fake)
	srv, err = StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()

	var ran []time.Time
	if err := srv.scheduler.Register(scheduler.Kind{Name: "test.tick", Interval: time.Hour,
		Run: func(_ context.Context, _ scheduler.Job, now time.Time) (scheduler.Result, error) {
			ran = append(ran, now)
			return scheduler.Result{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Write().ScheduleJob(t.Context(), "test.tick", "k", clk.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := srv.RunUntilIdle(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(ran) != 1 {
		t.Fatalf("到期的工作執行了 %d 次，預期 1 次", len(ran))
	}
	if at, ok, err := srv.scheduler.NextAt(t.Context()); err != nil || !ok || !at.Equal(clk.Now().Add(time.Hour)) {
		t.Errorf("NextAt = %v %v %v", at, ok, err)
	}
	clk.Advance(time.Hour)
	if err := srv.RunUntilIdle(t.Context()); err != nil || len(ran) != 2 {
		t.Errorf("一小時後：%d 次，%v", len(ran), err)
	}
}
