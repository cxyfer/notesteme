package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

func openTest(t *testing.T) (*Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	s, err := Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, clk
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)

	if _, ok, err := s.GetSetting(ctx, "k"); err != nil || ok {
		t.Fatalf("GetSetting = %v, %v; want absent", ok, err)
	}
	if inserted, err := s.SetSettingIfAbsent(ctx, "k", "v1"); err != nil || !inserted {
		t.Fatalf("SetSettingIfAbsent = %v, %v", inserted, err)
	}
	if inserted, err := s.SetSettingIfAbsent(ctx, "k", "v2"); err != nil || inserted {
		t.Fatalf("second SetSettingIfAbsent = %v, %v; want not inserted", inserted, err)
	}
	if v, ok, _ := s.GetSetting(ctx, "k"); !ok || v != "v1" {
		t.Errorf("value = %q, %v", v, ok)
	}
	if err := s.SetSetting(ctx, "k", "v3"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.GetSetting(ctx, "k"); v != "v3" {
		t.Errorf("value = %q, want v3", v)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)

	sess, err := s.CreateSession(ctx, "hash-1", time.Hour, "127.0.0.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" || sess.ID[14] != '7' {
		t.Errorf("id = %q, want a UUIDv7", sess.ID)
	}
	got, err := s.SessionByHash(ctx, "hash-1")
	if err != nil || got.ID != sess.ID {
		t.Fatalf("SessionByHash = %+v, %v", got, err)
	}

	clk.Advance(2 * time.Hour)
	if _, err := s.SessionByHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session err = %v, want ErrNotFound", err)
	}
	if err := s.ExtendSession(ctx, sess.ID, clk.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, "hash-1"); err != nil {
		t.Fatalf("extended session err = %v", err)
	}
	if err := s.DeleteSessionByHash(ctx, "hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionByHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session err = %v, want ErrNotFound", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	ctx := context.Background()
	s, clk := openTest(t)
	_, _ = s.CreateSession(ctx, "a", time.Minute, "", "")
	_, _ = s.CreateSession(ctx, "b", time.Hour, "", "")
	clk.Advance(10 * time.Minute)
	n, err := s.DeleteExpiredSessions(ctx)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpiredSessions = %d, %v; want 1", n, err)
	}
}

func TestCheckWritable(t *testing.T) {
	s, _ := openTest(t)
	if err := s.CheckWritable(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestLockDir(t *testing.T) {
	dir := t.TempDir()
	l1, err := LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockDir(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second LockDir err = %v, want ErrLocked", err)
	}
	if err := l1.WriteInfo(LockInfo{Holder: "serve", PID: 42, Listen: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	info, err := ReadLockInfo(dir)
	if err != nil || info == nil || info.PID != 42 || info.Listen != "127.0.0.1:1" {
		t.Fatalf("ReadLockInfo = %+v, %v", info, err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	if info, _ := ReadLockInfo(dir); info != nil {
		t.Errorf("info after Release = %+v, want nil", info)
	}
	l2, err := LockDir(dir)
	if err != nil {
		t.Fatalf("LockDir after release: %v", err)
	}
	// Close 不清內容（模擬中斷）。
	_ = l2.WriteInfo(LockInfo{Holder: "serve", PID: 1})
	_ = l2.Close()
	if info, _ := ReadLockInfo(dir); info == nil {
		t.Error("Close should keep the lock info")
	}
}
