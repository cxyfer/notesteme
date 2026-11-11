// notesteme 的程式入口：只負責把 os 的內容轉成 app.Env 與 app.IO。
package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cxyfer/notesteme/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, os.Args[1:], app.IO{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, app.Env{
		Vars: environ(os.Environ()),
		Log:  os.Stderr,
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
