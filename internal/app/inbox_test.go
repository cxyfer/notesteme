package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/inbox/fakemedia"
	"github.com/cxyfer/notesteme/internal/store"
)

// inboxEnv 在 testEnv 之上補 ffprobe（路徑解析用）與假的 Runner，並回傳 inbox 目錄。
func inboxEnv(t *testing.T) (Env, string) {
	t.Helper()
	env := testEnv(t)
	if err := os.WriteFile(filepath.Join(env.Vars["PATH"], "ffprobe"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.Runner = &fakemedia.Runner{}
	inbox := filepath.Join(env.Vars["NOTESTEME_DATA"], "inbox")
	return env, inbox
}

// putInbox 寫入 inbox 檔案，修改時間設為假時鐘「現在」的一分鐘前（第一次看到就穩定）。
func putInbox(t *testing.T, env Env, inbox, rel, content string) {
	t.Helper()
	p := filepath.Join(inbox, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := env.Clock.Now().Add(-time.Minute)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func itemExists(t *testing.T, s *Server, key string) bool {
	t.Helper()
	_, err := s.store.Read().ItemByKey(context.Background(), "local", key, 0)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true
}

// 手動模式：RunUntilIdle 的每一輪都掃描 inbox，新增穩定的影音。
func TestManualWorkerScansInbox(t *testing.T) {
	env, inbox := inboxEnv(t)
	env.Test.ManualWorker = true
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	putInbox(t, env, inbox, "talk.mp4", "VIDEO-a")
	putInbox(t, env, inbox, "notes.pdf", "text")
	if err := srv.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !itemExists(t, srv, "talk.mp4") {
		t.Fatal("RunUntilIdle 應該掃描 inbox 並新增 talk.mp4")
	}
	if itemExists(t, srv, "notes.pdf") {
		t.Error("不是影音的檔案不該新增")
	}
	if srv.watchCancel != nil {
		t.Error("手動模式不該啟動背景迴圈")
	}
}

// standalone（CLI）不啟動 inbox 監看。
func TestStandaloneDoesNotScanInbox(t *testing.T) {
	env, inbox := inboxEnv(t)
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	putInbox(t, env, inbox, "talk.mp4", "VIDEO-a")
	if err := sa.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	if itemExists(t, sa.core, "talk.mp4") {
		t.Error("standalone 不該自動掃描 inbox")
	}
}

// production：獨立的迴圈掃描 inbox；正常關閉時停止（draining 不再新增）。
func TestProductionWatcherLoopStopsOnShutdown(t *testing.T) {
	env, inbox := inboxEnv(t)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	if srv.watchCancel == nil {
		t.Fatal("production 應該啟動 inbox 迴圈")
	}
	putInbox(t, env, inbox, "talk.mp4", "VIDEO-a")
	// 迴圈每 5 秒（假時鐘）掃描一次：推進時鐘直到看到新增。
	deadline := time.Now().Add(10 * time.Second)
	for !itemExists(t, srv, "talk.mp4") {
		if time.Now().After(deadline) {
			t.Fatal("迴圈應該新增 talk.mp4")
		}
		env.Clock.(interface{ Advance(time.Duration) }).Advance(5 * time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-srv.watchDone:
	default:
		t.Error("Shutdown 之後迴圈應該已經結束")
	}
}
