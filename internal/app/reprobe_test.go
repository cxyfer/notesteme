package app

import (
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// source.reprobe 登記在排程器上：影音不存在時結束工作（刪除那一列）。
func TestReprobeJobEndsForMissingItem(t *testing.T) {
	env := testEnv(t)
	env.Test.ManualWorker = true
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	w := srv.store.Write()
	if err := w.ScheduleJob(t.Context(), store.ReprobeKind, "missing", env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ScheduledJob(t.Context(), store.ReprobeKind, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("工作應已結束：%v", err)
	}
}
