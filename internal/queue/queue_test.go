package queue

import (
	"context"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestRetryDelay(t *testing.T) {
	tests := []struct {
		attempts, max int
		want          time.Duration
		ok            bool
	}{
		{1, 3, 30 * time.Second, true},
		{2, 3, 2 * time.Minute, true},
		{3, 3, 0, false},
		{3, 4, 2 * time.Minute, true}, // 超過退避表時沿用最後一個
		{5, 6, 2 * time.Minute, true},
		{0, 3, 30 * time.Second, true},
	}
	for _, tt := range tests {
		d, ok := RetryDelay(tt.attempts, tt.max)
		if d != tt.want || ok != tt.ok {
			t.Errorf("RetryDelay(%d, %d) = %v, %v；預期 %v, %v", tt.attempts, tt.max, d, ok, tt.want, tt.ok)
		}
	}
}

func setup(t *testing.T) (*store.Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	s, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clk
}

func seedTask(t *testing.T, s *store.Store, ext string) store.Task {
	t.Helper()
	ctx := context.Background()
	var tk store.Task
	err := s.Tx(ctx, func(r *store.Repo) error {
		it, _, err := r.InsertItemIfAbsent(ctx, store.NewItem{Platform: "youtube", ExternalID: ext, URL: "u"})
		if err != nil {
			return err
		}
		snap, err := r.EnsureSnapshot(ctx, "sha", "{}")
		if err != nil {
			return err
		}
		sr, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: "metadata", Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			return err
		}
		tk, err = r.InsertTask(ctx, store.NewTask{StageRunID: sr.ID, Kind: "metadata", Executor: ExecutorBuiltin, ResourceClass: ResourceDownload, Priority: PriorityManual})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestWakeCoalesces(t *testing.T) {
	s, clk := setup(t)
	q := New(s, clk, "w")
	q.Wake()
	q.Wake()
	q.Wake()
	select {
	case <-q.Wakeups():
	default:
		t.Fatal("應收到通知")
	}
	select {
	case <-q.Wakeups():
		t.Fatal("重複的通知應合併成一個")
	default:
	}
}

func TestClaimAndRecoverAfterRestart(t *testing.T) {
	ctx := context.Background()
	s, clk := setup(t)
	tk := seedTask(t, s, "a")

	old := New(s, clk, "worker-1")
	got, err := old.Claim(ctx, 5)
	if err != nil || len(got) != 1 || got[0].ID != tk.ID {
		t.Fatalf("Claim = %v, %v", got, err)
	}
	if again, _ := old.Claim(ctx, 5); len(again) != 0 {
		t.Fatalf("已領取的任務不應再被領取：%v", again)
	}

	// 重新啟動：新的 worker ID 把舊的租約放回佇列，這次中斷不計入重試次數。
	next := New(s, clk, "worker-2")
	if n, err := next.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("RecoverStale = %d, %v", n, err)
	}
	got, _ = next.Claim(ctx, 5)
	if len(got) != 1 || got[0].Attempts != 1 {
		t.Fatalf("重新領取：%+v", got)
	}

	// 正常關閉：自己持有的租約放回。
	if n, err := next.ReleaseOwned(ctx); err != nil || n != 1 {
		t.Fatalf("ReleaseOwned = %d, %v", n, err)
	}
	if at, ok, _ := next.NextDue(ctx); !ok || !at.Equal(clk.Now()) {
		t.Errorf("NextDue = %v, %v", at, ok)
	}
}
