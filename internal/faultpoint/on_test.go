//go:build faultinject && unix

package faultpoint

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestHitKillsAtTheNthHit：子程序命中指定的點第 2 次時被 SIGKILL 結束，trace 記下之前的命中與 KILL 那一行。
func TestHitKillsAtTheNthHit(t *testing.T) {
	if os.Getenv("FAULTPOINT_CHILD") == "1" {
		for i := 0; i < 5; i++ {
			Hit(StageStart, "metadata")
			Hit(StageCommit, "transcript")
		}
		os.Exit(0) // 沒被殺就是失敗
	}
	tracePath := filepath.Join(t.TempDir(), "trace")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHitKillsAtTheNthHit$")
	cmd.Env = append(os.Environ(), "FAULTPOINT_CHILD=1", EnvFault+"=stage.commit/transcript:2", EnvTrace+"="+tracePath)
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("子程序應該被殺：%v", err)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("子程序應該被 SIGKILL 結束：%v", err)
	}
	b, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	want := "stage.start/metadata\nstage.commit/transcript\nstage.start/metadata\nstage.commit/transcript\nKILL stage.commit/transcript 2\n"
	if string(b) != want {
		t.Errorf("trace =\n%s\n應該是\n%s", b, want)
	}
}

// TestHitWithoutEnvIsNoop：沒有設定環境變數時 Hit 什麼都不做（faultinject 建置的測試程序本身也會走到這裡）。
func TestHitWithoutEnvIsNoop(t *testing.T) {
	if os.Getenv(EnvFault) != "" || os.Getenv(EnvTrace) != "" {
		t.Skip("環境已設定故障注入")
	}
	Hit(StageStart, "x")
	if !Enabled {
		t.Fatal("faultinject 建置的 Enabled 應該為 true")
	}
	if strings.TrimSpace(target) != "" {
		t.Fatal("沒有設定 NOTESTEME_FAULT 時不應該有目標")
	}
}
