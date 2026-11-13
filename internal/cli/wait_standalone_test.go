package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// fakeClockOf 取出 testEnv 的 fake clock。
func fakeClockOf(t *testing.T, env app.Env) *clock.Fake {
	t.Helper()
	clk, ok := env.Clock.(*clock.Fake)
	if !ok {
		t.Fatalf("Env.Clock 不是 *clock.Fake：%T", env.Clock)
	}
	return clk
}

// installTools 在 PATH 放入空的 ffmpeg、yt-dlp：工具要找得到，才會走到 Runner（實際執行交給 env.Runner）。
func installTools(t *testing.T, env app.Env) {
	t.Helper()
	for _, name := range []string{"ffmpeg", "ffprobe", "yt-dlp"} {
		if err := os.WriteFile(filepath.Join(env.Vars["PATH"], name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// stuckRunner 是卡住的外部工具：Run 一律等到 ctx 取消才回傳，並通知測試它已經開始。
type stuckRunner struct {
	once    sync.Once
	started chan struct{}
}

func newStuckRunner() *stuckRunner { return &stuckRunner{started: make(chan struct{})} }

func (r *stuckRunner) Run(ctx context.Context, _ toolexec.Cmd) (toolexec.Result, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return toolexec.Result{}, ctx.Err()
}

// 獨立模式的 --wait --timeout 在任務執行期間也要生效（CLI-R7）：任務卡住時，時間到就取消它，
// 結束碼 4（不是錯誤），租約放回佇列。
func TestWaitStandaloneTimeoutCancelsRunningTask(t *testing.T) {
	env, _ := testEnv(t)
	runner := newStuckRunner()
	env.Runner = runner
	installTools(t, env)
	clk := fakeClockOf(t, env)

	done := make(chan Result, 1)
	var errOut string
	go func() {
		res, _, e := run(t, env, "ingest", "yt:abcdefghijk", "--wait", "--timeout", "5m")
		errOut = e
		done <- res
	}()

	select {
	case <-runner.started:
	case <-time.After(10 * time.Second):
		t.Fatal("任務沒有開始執行")
	}
	// 等 CLI 建立好 --timeout 的計時器（用 fake clock，不真的等待）。
	for deadline := time.Now().Add(5 * time.Second); clk.PendingTimers() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("CLI 沒有建立 --timeout 的計時器")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case res := <-done:
		t.Fatalf("時間還沒到就結束了：%+v", res)
	default:
	}
	clk.Advance(5 * time.Minute)

	select {
	case res := <-done:
		if res.Code != exitWaiting || res.Mode != ModeStandalone {
			t.Errorf("結果 = %+v，預期結束碼 4（stderr：%s）", res, errOut)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("超過 --timeout 後沒有結束")
	}

	// 租約已放回：資料目錄可以再開，任務沒有停在 running。
	res, out, errOut2 := run(t, env, "items", "show", "yt:abcdefghijk", "--json")
	if res.Code != 0 {
		t.Fatalf("結果 = %+v，stderr：%s", res, errOut2)
	}
	if strings.Contains(out, `"running"`) || !strings.Contains(out, `"pending"`) {
		t.Errorf("取消的任務應該放回 pending：%s", out)
	}
}
