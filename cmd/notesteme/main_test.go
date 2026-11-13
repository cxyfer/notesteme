package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildBinary 實際建置 notesteme：補上程序內執行跳過 main() 的部分（ADR-0015）。
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "notesteme")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build 失敗：%v\n%s", err, out)
	}
	return bin
}

// run 以乾淨的環境執行 binary：PATH 是空的資料夾，所以找不到任何外部工具。
func run(t *testing.T, bin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	empty := t.TempDir()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"PATH=" + empty, "HOME=" + empty}
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		t.Fatalf("執行失敗：%v", err)
	}
	return code, out.String(), errOut.String()
}

func TestBinarySmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("-short 不建置 binary")
	}
	bin := buildBinary(t)

	t.Run("--version", func(t *testing.T) {
		code, out, errOut := run(t, bin, "--version")
		if code != 0 || !strings.HasPrefix(out, "notesteme ") {
			t.Errorf("結束碼 %d，stdout %q，stderr %q", code, out, errOut)
		}
	})

	t.Run("doctor 找不到工具時結束碼是 1", func(t *testing.T) {
		code, out, errOut := run(t, bin, "doctor", "--json")
		if code != 1 {
			t.Fatalf("結束碼 = %d，預期 1；stdout %q，stderr %q", code, out, errOut)
		}
		var report struct {
			Checks []struct{ Name, Status string } `json:"checks"`
		}
		if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.Checks) == 0 || report.Checks[0].Name != "ffmpeg" || report.Checks[0].Status != "fail" {
			t.Errorf("輸出 = %q（%v）", out, err)
		}
	})

	t.Run("參數錯誤是 2，不會碰資料目錄", func(t *testing.T) {
		code, _, errOut := run(t, bin, "export", "yt:abc123xxxxx", "--format", "docx")
		if code != 2 || !strings.Contains(errOut, "--format") {
			t.Errorf("結束碼 %d，stderr %q", code, errOut)
		}
		if _, err := os.Stat("data"); err == nil {
			t.Error("不應該在目前目錄建立 data")
		}
	})
}

// 第一次訊號（ctx 取消）之後才呼叫 stop，讓第二次 Ctrl-C 回到預設行為。
func TestStopAfterFirstSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go stopAfterFirstSignal(ctx, func() { close(stopped) })
	select {
	case <-stopped:
		t.Fatal("還沒收到訊號就呼叫了 stop")
	case <-time.After(20 * time.Millisecond): // 只是確認「還沒」，不影響正確性
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("收到訊號後沒有呼叫 stop")
	}
}
