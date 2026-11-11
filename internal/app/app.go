// Package app 是所有子命令的入口，cmd/notesteme 與驗收測試都從這裡進入程式。
package app

import (
	"context"
	"fmt"
	"io"
)

// version 在建置時可由 -ldflags 覆寫。
var version = "dev"

// Env 是程式對外部環境的全部依賴；production 程式碼只能經由它取得環境變數與 log 輸出。
//
// TODO: 之後依 ADR-0015 補上 Clock、網路依賴（netguard.Deps）、外部工具 runner、
// listener 與測試用的 worker 控制點（Test）。
type Env struct {
	// Vars 是環境變數，key 為變數名稱。
	Vars map[string]string
	// Log 是 log 輸出，nil 時視為丟棄。
	Log io.Writer
}

// IO 是標準輸入輸出。
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run 執行一次命令並回傳結束碼。args 不含程式名稱。
func Run(ctx context.Context, args []string, stdio IO, env Env) int {
	_ = ctx
	_ = env

	if len(args) == 0 {
		fmt.Fprintln(stdio.Stderr, "usage: notesteme <command> [flags]")
		return 2
	}

	switch args[0] {
	case "--version":
		fmt.Fprintf(stdio.Stdout, "notesteme %s\n", version)
		return 0
	default:
		fmt.Fprintf(stdio.Stderr, "notesteme: %q is not implemented\n", args[0])
		return 2
	}
}
