package harness

import (
	"context"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
)

func TestEventDBHelpers(t *testing.T) {
	w := NewT(t)
	// 資料庫還不存在：寫入要失敗，而不是默默建立一個空的資料庫。
	if err := w.InsertEventAt(time.Unix(100, 0)); err == nil {
		t.Fatal("資料庫不存在時 InsertEventAt 應該失敗")
	}

	srv, err := app.StartServer(context.Background(), w.AppEnv())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()

	old := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	// server 執行中也能寫入與讀取。
	for _, at := range []time.Time{old, recent} {
		if err := w.InsertEventAt(at); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := w.EventCount(); err != nil || n != 2 {
		t.Fatalf("EventCount = %d, %v；預期 2", n, err)
	}
	cutoff := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	if n, err := w.EventsBefore(cutoff); err != nil || n != 1 {
		t.Fatalf("EventsBefore = %d, %v；預期 1", n, err)
	}
	// 等於 cutoff 的不算「早於」。
	if n, err := w.EventsBefore(old); err != nil || n != 0 {
		t.Fatalf("EventsBefore(= old) = %d, %v；預期 0", n, err)
	}
}
