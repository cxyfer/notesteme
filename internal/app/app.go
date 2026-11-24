// Package app 是所有子命令的入口，cmd/notesteme 與驗收測試都從這裡進入程式。
package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// version 在建置時可由 -ldflags 覆寫。
var version = "dev"

// Version 回傳程式的版本（MCP 的 serverInfo 與 clientInfo 使用）。
func Version() string { return version }

// VersionFor 是這個程序對外報告的版本：測試可以用 TestControls.Version 讓 CLI 與 serve 報告不同的版本（CLI-R9）。
func VersionFor(env Env) string {
	if env.Test != nil && env.Test.Version != "" {
		return env.Test.Version
	}
	return version
}

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
	// Dial 建立 CLI 連到 --server 的連線（client 模式，CLI-R3）；nil 時用 net.Dialer。
	// 它不經過 netguard：目標是使用者自己指定的 server（可能在本機或區網），不是不受信任的外部網址。
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
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
	// Version 非空時取代程式版本，只用於 CLI 與 serve 之間的版本檢查（X-Notesteme-Version，CLI-R9）。
	Version string
	// SkipShutdownGrace 讓正常關閉的寬限期為 0：執行中的任務立刻被取消，驗收場景不必推進時鐘（PIPE-R12）。
	SkipShutdownGrace bool
	// ImplVersions 覆寫步驟的 impl_version（PIPE-R6），key 是步驟名稱；沒列出的步驟用預設值。
	// server 已啟動時改用 Server.SetImplVersion。
	ImplVersions map[string]int
	// Migrations 取代內建的 migration（store.Options.Migrations），用來模擬「程式內建的最新 schema 版本是 N」
	// （OPS-R3、R4）；nil 時用內建的。
	Migrations fs.FS
	// SerialLLM 讓 worker 一次最多執行一個 llm 類別的任務（不論設定的上限），chunk 任務依建立順序執行，
	// fakellm 的「依序回答」與請求編號才有確定的順序。不影響 GET /api/queue 顯示的上限（M2 S0）。
	SerialLLM bool
	// CostEstimates 取代這些步驟每個任務的估算金額（美元，COST 待決問題 C8）；PricesAsOf 取代內建計價表的確認日期。
	CostEstimates map[string]float64
	PricesAsOf    string
	// DiskUsage 取代讀取資料目錄所在檔案系統用量的函式（OPS-R16 的告警）；每次檢查都呼叫，可以在 server 啟動後改變結果。
	DiskUsage func(dir string) (int, error)
	// NoYtDlpAutoUpdate 讓 yt-dlp 的排程更新（updates.yt_dlp）在設定檔沒有明確寫 enabled 時預設關閉：
	// 驗收場景的 fake clock 常常跨過更新的時刻，假的 python 會真的換掉 yt-dlp 或送出告警，影響無關的場景。
	NoYtDlpAutoUpdate bool
}

// IO 是標準輸入輸出。
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// shutdownGrace 是正常關閉時等待執行中任務完成的寬限期（PIPE-R12）。
const shutdownGrace = 30 * time.Second

// httpShutdownTimeout 是收到訊號後等待 HTTP 處理中請求的上限。它只限制 HTTP 這一段：
// 任務的寬限期與放回租約有獨立的期限（Server.Shutdown），不受它影響。
const httpShutdownTimeout = 10 * time.Second

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
	opts, err := parseServeArgs(args)
	if err != nil {
		fmt.Fprintf(stdio.Stderr, "notesteme serve: %v\n", err)
		return 2
	}

	srv, err := StartServerWith(ctx, env, opts)
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
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), httpShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		fmt.Fprintf(stdio.Stderr, "notesteme: 關閉時發生錯誤：%v\n", err)
		return 1
	}
	return code
}
