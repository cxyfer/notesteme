package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

func TestEventsRepo(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	w := s.Write()

	if n, err := s.Read().CountEvents(ctx); err != nil || n != 0 {
		t.Fatalf("空的 CountEvents = %d, %v", n, err)
	}

	old := clk.Now().Add(-100 * 24 * time.Hour)
	if err := w.InsertEvent(ctx, EventRecord{Type: "item.created", ItemID: "i1", At: old}); err != nil {
		t.Fatal(err)
	}
	// 沒給 At 時取目前時間；沒給 ID 與 payload 時自動補上。
	if err := w.InsertEvent(ctx, EventRecord{Type: "stage.status", PayloadJSON: `{"a":1}`}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Read().CountEvents(ctx); n != 2 {
		t.Fatalf("CountEvents = %d，預期 2", n)
	}

	// 剛好等於 cutoff 的事件要保留。
	n, err := w.DeleteEventsBefore(ctx, old)
	if err != nil || n != 0 {
		t.Fatalf("DeleteEventsBefore(old) = %d, %v，預期 0", n, err)
	}
	n, err = w.DeleteEventsBefore(ctx, clk.Now().Add(-90*24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("DeleteEventsBefore(90d) = %d, %v，預期 1", n, err)
	}
	if n, _ := s.Read().CountEvents(ctx); n != 1 {
		t.Fatalf("清理後 CountEvents = %d，預期 1", n)
	}
}

func TestOpenOptionsMigrations(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))

	// 只提供版本 1 的 migration：後面的表（例如 events）不應該存在。
	init1, err := fs.ReadFile(MigrationsFS(), "00001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, t.TempDir(), clk, Options{Migrations: fstest.MapFS{"00001_init.sql": {Data: init1}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Write().CountEvents(ctx); err == nil {
		t.Fatal("Options.Migrations 沒有生效：events 表不該存在")
	}

	// 沒給 Options 時用內建的全部 migration。
	s2, _ := openTest(t)
	if _, err := s2.Write().CountEvents(ctx); err != nil {
		t.Fatalf("預設 Open 應有 events 表：%v", err)
	}
}
