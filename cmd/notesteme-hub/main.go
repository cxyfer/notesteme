// notesteme-hub 是 Hub 的程式入口（ADR-0030 第 1 點）：只負責把 os 的內容轉成 hubcli.Env。
// 這裡是唯一讀取 os.Environ 與建立真實時鐘、listener 的地方（ADR-0015）。
package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubcli"
	"github.com/cxyfer/notesteme/internal/netguard"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			vars[k] = v
		}
	}
	code := hubcli.Run(ctx, os.Args[1:], hubcli.Env{
		Vars:   vars,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Clock:  clock.Real{},
		// 明確使用系統的 resolver、dialer 與根憑證；零值的 Deps 會拒絕連線。
		Net:    netguard.SystemDeps(),
		Listen: net.Listen,
	})
	stop()
	os.Exit(code)
}
