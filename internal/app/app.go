// Package app 是所有子命令的入口，cmd/notesteme 與驗收測試都從這裡進入程式。
package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// version 在建置時可由 -ldflags 覆寫。
var version = "dev"

// Env 是程式對外部環境的全部依賴（ADR-0015）。
// production 程式碼只能經由它取得環境變數、時間、網路與 log 輸出；
// 只有 cmd/notesteme/main.go 可以讀 os.Environ 與建立真實的 clock、listener。
type Env struct {
	// Vars 是環境變數，key 為變數名稱。
	Vars map[string]string
	// Clock 是時鐘；serve 必須提供。
	Clock clock.Clock
	// Net 是對外 HTTP 的依賴（DNS、dialer、根憑證），交給 netguard。
	Net netguard.Deps
	// Listen 建立 HTTP listener；serve 必須提供。測試把設定的位址換成 127.0.0.1:0。
	Listen func(network, addr string) (net.Listener, error)
	// Log 是 log 輸出，nil 時視為丟棄。
	Log io.Writer
	// Runner 是執行外部工具（yt-dlp、ffmpeg）的 runner；nil 時使用 production 實作，
	// 它只帶 allowlist 內的環境變數，並以 Vars["PATH"] 尋找工具（ADR-0009、ADR-0015）。
	Runner toolexec.Runner

	// Test 只在驗收與單元測試設定；production（main.go）永遠是 nil。
	Test *TestControls
}

// TestControls 是只給測試使用的開關。
type TestControls struct {
	// LowCostHashing 讓密碼雜湊與 master key 的金鑰推導使用低成本的 argon2id 參數，測試不必等待。
	LowCostHashing bool
	// ManualWorker 讓 server 不自己執行 worker：任務只在測試呼叫 Server.RunUntilIdle 或
	// Server.RunRound 時才會被領取與執行（ADR-0015）。
	ManualWorker bool
	// SkipShutdownGrace 讓正常關閉的寬限期為 0：執行中的任務立刻被取消，驗收場景不必推進時鐘（PIPE-R12）。
	SkipShutdownGrace bool
}

// IO 是標準輸入輸出。
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// shutdownGrace 是正常關閉時等待執行中任務完成的寬限期（PIPE-R12）。
const shutdownGrace = 30 * time.Second

// shutdownTimeout 是收到訊號後等待正常關閉的上限：寬限期之外還要留時間給放回租約與關閉資料庫，
// 否則放回租約時 ctx 已經逾時。
const shutdownTimeout = shutdownGrace + 10*time.Second

// Run 執行一次命令並回傳結束碼。args 不含程式名稱。
func Run(ctx context.Context, args []string, stdio IO, env Env) int {
	if len(args) == 0 {
		fmt.Fprintln(stdio.Stderr, "usage: notesteme <command> [flags]")
		return 2
	}

	switch args[0] {
	case "--version":
		fmt.Fprintf(stdio.Stdout, "notesteme %s\n", version)
		return 0
	case "serve":
		return runServe(ctx, args[1:], stdio, env)
	default:
		fmt.Fprintf(stdio.Stderr, "notesteme: %q is not implemented\n", args[0])
		return 2
	}
}

// runServe 啟動 server，直到 ctx 被取消（main.go 在收到 SIGINT／SIGTERM 時取消）後正常關閉。
func runServe(ctx context.Context, args []string, stdio IO, env Env) int {
	// TODO(AUTH-R3)：--insecure-no-auth 才允許在沒有密碼時啟動。
	if len(args) > 0 {
		fmt.Fprintf(stdio.Stderr, "notesteme serve: unknown argument %q\n", args[0])
		return 2
	}

	srv, err := StartServer(ctx, env)
	if err != nil {
		fmt.Fprintf(stdio.Stderr, "notesteme: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdio.Stdout, "notesteme listening on %s\n", srv.Addr())

	code := 0
	select {
	case <-ctx.Done():
	case <-srv.Done():
		// serve 自己停止了（listener 出錯）：回報錯誤，但仍要走 Shutdown 釋放 DB、鎖與 worker。
		if err := srv.Err(); err != nil {
			fmt.Fprintf(stdio.Stderr, "notesteme: %v\n", err)
			code = 1
		}
	}

	// ctx 可能已取消，正常關閉要用新的 context。
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintf(stdio.Stderr, "notesteme: 關閉時發生錯誤：%v\n", err)
		return 1
	}
	return code
}
