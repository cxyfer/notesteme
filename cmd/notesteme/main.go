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
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/netguard"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, os.Args[1:], app.IO{
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
	os.Exit(code)
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
