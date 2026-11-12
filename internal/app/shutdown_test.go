package app

import (
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// startBlocked 啟動 server，新增一部影音並讓它的 metadata 任務卡在 yt-dlp 裡。
func startBlocked(t *testing.T) (env Env, srv *Server, block *ytRunner, id string) {
	t.Helper()
	env = testEnv(t)
	env.Test.ManualWorker = true
	block = &ytRunner{block: make(chan struct{}), inProg: make(chan struct{})}
	env.Runner = block
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Kill)
	id = ingest(t, login(t, srv))
	if n, err := srv.RunRound(t.Context()); err != nil || n != 1 {
		t.Fatalf("RunRound = %d, %v", n, err)
	}
	waitClosed(t, block.inProg)
	return env, srv, block, id
}

// waitTimers 等到 fake clock 上有 n 個以上的計時器；Shutdown 進入寬限期就會建立一個。
func waitTimers(t *testing.T, clk *clock.Fake, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second) // 只是防止卡死的上限，不決定結果
	for clk.PendingTimers() < n {
		if time.Now().After(deadline) {
			t.Fatal("等待 Shutdown 進入寬限期逾時")
		}
		time.Sleep(time.Millisecond)
	}
}

// 寬限期內完成的任務正常寫回，不需要放回佇列（PIPE-R12）。
func TestShutdownLetsRunningTasksFinishWithinGrace(t *testing.T) {
	env, srv, block, id := startBlocked(t)
	clk := env.Clock.(*clock.Fake)

	done := make(chan error, 1)
	go func() { done <- srv.Shutdown(t.Context()) }()
	waitTimers(t, clk, 1)
	select {
	case err := <-done:
		t.Fatalf("寬限期內任務還在執行，Shutdown 不該結束：%v", err)
	default:
	}

	close(block.block) // 任務在寬限期內完成
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("任務完成後 Shutdown 沒有結束")
	}

	env.Runner = &ytRunner{}
	srv2, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Kill()
	var d detail
	login(t, srv2).json("GET", "/api/items/"+id, "", &d)
	if d.status("metadata") != "done" {
		t.Errorf("寬限期內完成的步驟應寫回為 done：%+v", d)
	}
}

// 寬限期到期後，剩下的任務被取消、租約放回佇列，重啟後可以完成（PIPE-R12）。
func TestShutdownCancelsRunningTasksWhenGraceExpires(t *testing.T) {
	env, srv, _, id := startBlocked(t)
	clk := env.Clock.(*clock.Fake)

	done := make(chan error, 1)
	go func() { done <- srv.Shutdown(t.Context()) }()
	waitTimers(t, clk, 1)
	clk.Advance(shutdownGrace)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("寬限期到期後 Shutdown 沒有結束")
	}

	env.Runner = &ytRunner{}
	srv2, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv2.Kill()
	c := login(t, srv2)
	var d detail
	c.json("GET", "/api/items/"+id, "", &d)
	if d.status("metadata") != "pending" {
		t.Fatalf("到期被取消的步驟應回到 pending：%+v", d)
	}
	if err := srv2.RunUntilIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	c.json("GET", "/api/items/"+id, "", &d)
	if d.status("normalize") != "done" {
		t.Errorf("重啟後應完成：%+v", d)
	}
}
