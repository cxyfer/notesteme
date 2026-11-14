package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// venvBlockRunner 在建立 venv 時（`python3 -m venv <dir>`）建立目錄後一直等到 ctx 取消；其他指令什麼都不做。
type venvBlockRunner struct{ reached chan struct{} }

func (r venvBlockRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if len(c.Args) == 3 && c.Args[1] == "venv" {
		if err := os.MkdirAll(c.Args[2], 0o755); err != nil {
			return toolexec.Result{}, err
		}
		close(r.reached)
		<-ctx.Done()
		return toolexec.Result{}, ctx.Err()
	}
	return toolexec.Result{}, nil
}

// server 關閉（root 取消）時，進行中的 yt-dlp 更新要跟著取消並清掉新的 venv，
// 不能只綁在 HTTP 請求的 ctx 上（請求的 ctx 這時還沒有取消）。
func TestYtDlpUpdateIsCancelledWhenTheServerShutsDown(t *testing.T) {
	env := testEnv(t)
	if err := os.WriteFile(filepath.Join(env.Vars["PATH"], "python3"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := startBackupServer(t, env)
	runner := venvBlockRunner{reached: make(chan struct{})}
	srv.ytdlp = ytdlp.New(ytdlp.ToolsDir(srv.cfg.Data), runner, ToolResolver(env.Vars, srv.cfg), env.Clock, srv.log)

	reqCtx := context.Background() // 永遠不會取消的請求 ctx
	done := make(chan error, 1)
	go func() {
		_, err := srv.toolManager(env).UpdateYtDlp(reqCtx, ytdlp.UpdateRequest{})
		done <- err
	}()
	select {
	case <-runner.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("更新沒有開始建立 venv")
	}
	srv.cancel() // server 的 root ctx 取消，等同關閉
	select {
	case err := <-done:
		if err == nil {
			t.Error("被取消的更新應該回傳錯誤")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("root 取消之後，更新沒有結束")
	}
	entries, _ := os.ReadDir(ytdlp.VersionsDir(ytdlp.ToolsDir(srv.cfg.Data)))
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("取消之後不該留下 venv：%s", e.Name())
		}
	}
}
