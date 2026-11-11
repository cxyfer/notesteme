//go:build unix

package toolexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func sh(t *testing.T) string {
	t.Helper()
	p, err := LookPath("sh", "/bin:/usr/bin")
	if err != nil {
		t.Skip("找不到 sh")
	}
	return p
}

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	r := New(map[string]string{})
	res, err := r.Run(context.Background(), Cmd{Path: sh(t), Args: []string{"-c", "echo out; echo err >&2; exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" || res.ExitCode != 3 {
		t.Errorf("result = %+v", res)
	}
}

func TestRunOnlyPassesAllowlistedEnv(t *testing.T) {
	vars := map[string]string{
		"PATH": "/usr/bin:/bin", "HOME": "/tmp/h", "LANG": "C",
		"NOTESTEME_MASTER_KEY": "secret-master-key", "NOTESTEME_PASSWORD": "secret-pass",
	}
	res, err := New(vars).Run(context.Background(), Cmd{Path: sh(t), Args: []string{"-c", "env"}})
	if err != nil {
		t.Fatal(err)
	}
	out := string(res.Stdout)
	for _, want := range []string{"PATH=/usr/bin:/bin", "HOME=/tmp/h", "LANG=C"} {
		if !strings.Contains(out, want) {
			t.Errorf("環境變數缺少 %s：\n%s", want, out)
		}
	}
	for _, leak := range []string{"secret-master-key", "secret-pass", "NOTESTEME_"} {
		if strings.Contains(out, leak) {
			t.Errorf("子程序不應看到 %q：\n%s", leak, out)
		}
	}
}

func TestRunDoesNotInheritProcessEnv(t *testing.T) {
	t.Setenv("NST_TEST_LEAK", "leaked")
	res, err := New(map[string]string{}).Run(context.Background(), Cmd{Path: sh(t), Args: []string{"-c", "echo [$NST_TEST_LEAK]"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(res.Stdout)) != "[]" {
		t.Errorf("stdout = %q，不應繼承程序的環境", res.Stdout)
	}
}

func TestRunKillsWholeProcessGroupOnCancel(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// 孫程序 sleep 在背景執行；取消時它也必須被結束。
		_, err := New(map[string]string{}).Run(ctx, Cmd{Path: sh(t), Args: []string{"-c", "sleep 60 & echo $! > " + pidFile + "; wait"}})
		done <- err
	}()
	var pid int
	for i := 0; i < 500; i++ {
		if b, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(b)) != "" {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("孫程序沒有啟動")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v，預期 context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("取消後 Run 沒有結束")
	}
	// 孫程序要在短時間內消失。
	for i := 0; i < 200; i++ {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("孫程序 %d 仍然存在", pid)
}

func TestRunLimitsOutput(t *testing.T) {
	r := New(map[string]string{}).WithLimits(1024, 1024)
	_, err := r.Run(context.Background(), Cmd{Path: sh(t), Args: []string{"-c", "yes | head -c 1000000"}})
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Errorf("err = %v，預期 ErrOutputTooLarge", err)
	}
}

func TestRunTimeout(t *testing.T) {
	_, err := New(map[string]string{}).Run(context.Background(), Cmd{Path: sh(t), Args: []string{"-c", "sleep 60"}, Timeout: 100 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v，預期 DeadlineExceeded", err)
	}
}

func TestRunStartFailure(t *testing.T) {
	if _, err := New(nil).Run(context.Background(), Cmd{Path: "/nonexistent/tool"}); err == nil {
		t.Error("啟動失敗應回傳錯誤")
	}
	if _, err := New(nil).Run(context.Background(), Cmd{}); err == nil {
		t.Error("沒有路徑應回傳錯誤")
	}
}
