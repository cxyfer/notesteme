// notesteme 的程式入口：只負責把 os 的內容轉成 app.Env 與 app.IO。
// 這裡是唯一讀取 os.Environ 與建立真實時鐘、listener 的地方（ADR-0015）。
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/cli"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/netguard"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// serve 有自己的關閉流程；其他子命令（獨立模式）第一次中斷後恢復預設行為，第二次 Ctrl-C 立刻結束程序。
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		go stopAfterFirstSignal(ctx, stop)
	}
	res := cli.Execute(ctx, os.Args[1:], app.IO{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, app.Env{
		Vars:  environ(os.Environ()),
		Clock: clock.Real{},
		// 明確使用系統的 resolver、dialer 與根憑證；零值的 Deps 會拒絕連線。
		Net:    netguard.SystemDeps(),
		Listen: net.Listen,
		Log:    os.Stderr,
	})
	stop()
	os.Exit(res.Code)
}

// stopAfterFirstSignal 在 ctx 被取消（收到第一次訊號）後呼叫 stop，讓之後的訊號回到預設的結束行為。
// 第一次中斷會讓執行中的任務有機會乾淨地結束（放回租約、釋放資料目錄）；使用者等不及時，第二次 Ctrl-C 直接中斷。
func stopAfterFirstSignal(ctx context.Context, stop func()) {
	<-ctx.Done()
	stop()
}

// environ 把 KEY=VALUE 形式的環境變數轉成 map。
func environ(kv []string) map[string]string {
	vars := make(map[string]string, len(kv))
	for _, e := range kv {
		if k, v, ok := strings.Cut(e, "="); ok {
			vars[k] = v
		}
	}
	return vars
}
