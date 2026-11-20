package app

import (
	"context"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

// 到期的垃圾桶影音仍有 pending 任務時，定期清理不能死結：housekeeping 在 worker 一輪之中執行，
// 永久刪除要結束殘留的任務（Worker.Cancel），Cancel 不可以等一個已經被這一輪持有的鎖。
func TestServerPurgeExpiredWithPendingTaskDoesNotDeadlock(t *testing.T) {
	ctx := context.Background()
	s := startPurgeServer(t, "")
	it := s.trashedItem("abc123xxxxx")
	err := s.store.Tx(ctx, func(r *store.Repo) error {
		snap, err := r.EnsureSnapshot(ctx, "sha", "{}")
		if err != nil {
			return err
		}
		sr, err := r.InsertStageRun(ctx, store.NewStageRun{ItemID: it.ID, Stage: "metadata", Status: store.StatusPending, ConfigSnapshotID: snap})
		if err != nil {
			return err
		}
		_, err = r.InsertTask(ctx, store.NewTask{StageRunID: sr.ID, Kind: "metadata", Executor: queue.ExecutorBuiltin,
			ResourceClass: queue.ResourceDownload, Priority: queue.PriorityManual})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.clk.Advance(31 * 24 * time.Hour)

	done := make(chan error, 1)
	go func() { done <- s.RunUntilIdle(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunUntilIdle 逾時：定期清理疑似死結")
	}
	if s.itemExists(it.ID) || s.hasFiles(it.ID) {
		t.Errorf("到期的影音應該永久刪除：影音存在 = %v，檔案存在 = %v", s.itemExists(it.ID), s.hasFiles(it.ID))
	}
}
