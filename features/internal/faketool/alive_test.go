//go:build unix

package faketool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("自己應該存活")
	}
	if pidAlive(0) || pidAlive(-1) {
		t.Error("無效的 pid 不算存活")
	}

	// 已結束並被回收的程序。
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if pidAlive(cmd.Process.Pid) {
		t.Error("已回收的程序不算存活")
	}
}

// 已結束但還沒被回收的殭屍程序，kill(pid, 0) 仍成功；pidAlive 要把它當成已結束。
func TestPidAliveTreatsZombieAsDead(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("需要 /proc 才能辨識殭屍程序")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := false
	defer func() {
		if !reaped {
			_ = cmd.Wait()
		}
	}()
	// 子程序結束之後、Wait 之前就是殭屍；以真實時間輪詢，只當防卡死的上限。
	deadline := time.Now().Add(10 * time.Second)
	for pidAlive(cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("子程序結束後 pidAlive 仍為 true")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = cmd.Wait()
	reaped = true
}

func TestGateAliveCountsOnlyLiveProcesses(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(dir, "gate")

	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{os.Getpid(), os.Getppid(), dead.Process.Pid} {
		if err := os.WriteFile(fmt.Sprintf("%s%s.%d", gate, GateWaitingSuffix, pid), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 無法解析的標記不算。
	if err := os.WriteFile(gate+GateWaitingSuffix+".junk", nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if marks, _ := GateWaiting(gate); len(marks) != 4 {
		t.Fatalf("標記總數 = %d，預期 4", len(marks))
	}
	n, err := GateAlive(gate)
	if err != nil || n != 2 {
		t.Fatalf("GateAlive = %d, %v；預期 2（已結束的與無法解析的不算）", n, err)
	}
}
