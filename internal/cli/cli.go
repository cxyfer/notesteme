// Package cli 實作 notesteme 的子命令（CLI-R1～R8）。
//
// Execute 是所有子命令的入口：`serve` 與 `--version` 轉給 app.Run，其他子命令在這裡解析參數，
// 依資料目錄的鎖判斷模式（獨立、本機 client、--server，見 backend.go），
// 再經由 HTTP 呼叫 API。cmd/notesteme 與驗收測試都從這裡進入（ADR-0015）。
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/pflag"

	"github.com/cxyfer/notesteme/internal/app"
	"github.com/cxyfer/notesteme/internal/clock"
)

// Mode 是 CLI 實際使用的模式（CLI-R2、R3）。
type Mode string

const (
	// ModeStandalone 是獨立模式：CLI 程序自己持有資料目錄。
	ModeStandalone Mode = "standalone"
	// ModeClient 是 client 模式：經由 serve 的本機 socket，或 --server 的 TCP 連線。
	ModeClient Mode = "client"
)

// Result 是一次 Execute 的結果。
type Result struct {
	// Code 是結束碼（CLI-R6）。
	Code int
	// Mode 是實際使用的模式；doctor、參數錯誤等沒有判斷模式時為空。
	Mode Mode
	// ErrorCode 是 API 錯誤的 code（例如 not_implemented）；沒有時為空。
	ErrorCode string
}

// globals 是所有子命令共有的旗標。
type globals struct {
	server string
	token  string
	data   string
	json   bool
}

// bind 把全域旗標掛到 fs。
func (g *globals) bind(fs *pflag.FlagSet) {
	fs.StringVar(&g.server, "server", "", "serve 的位址（http://host:port）；指定時一律使用 client 模式")
	fs.StringVar(&g.token, "token", "", "--server 使用的 API token")
	fs.StringVar(&g.data, "data", "", "資料目錄（獨立模式）")
	fs.BoolVar(&g.json, "json", false, "輸出 JSON")
}

// session 是一次子命令執行時用到的東西。
type session struct {
	be     *backend // doctor 等不需要 API 的子命令為 nil
	g      *globals
	env    app.Env
	clock  clock.Clock
	stdout io.Writer
	stderr io.Writer
}

// plan 是解析完參數的子命令。
type plan struct {
	g *globals
	// noBackend 為 true 時不判斷模式（doctor、還沒實作的子命令）。
	noBackend bool
	// run 執行子命令，回傳結束碼；錯誤依 exitFor 對應。
	run func(ctx context.Context, s *session) (int, error)
}

// errHelp 表示使用者要求說明（-h、--help），不是錯誤。
var errHelp = errors.New("help")

// Execute 執行一個子命令。args 不含程式名稱。
func Execute(ctx context.Context, args []string, stdio app.IO, env app.Env) Result {
	if stdio.Stdout == nil {
		stdio.Stdout = io.Discard
	}
	if stdio.Stderr == nil {
		stdio.Stderr = io.Discard
	}
	if env.Clock == nil {
		env.Clock = clock.Real{}
	}
	if len(args) == 0 {
		fmt.Fprint(stdio.Stderr, usageText)
		return Result{Code: exitUsage}
	}
	switch args[0] {
	case "serve", "--version":
		return Result{Code: app.Run(ctx, args, stdio, env)}
	case "-h", "--help", "help":
		fmt.Fprint(stdio.Stdout, usageText)
		return Result{Code: exitOK}
	}

	// 參數錯誤一律在判斷模式之前檢查（CLI-R6）。
	p, err := parse(args)
	if errors.Is(err, errHelp) {
		fmt.Fprint(stdio.Stdout, usageText)
		return Result{Code: exitOK}
	}
	if err != nil {
		return fail(stdio, err, "")
	}

	s := &session{g: p.g, env: env, clock: env.Clock, stdout: stdio.Stdout, stderr: stdio.Stderr}
	var mode Mode
	if !p.noBackend {
		be, err := openBackend(ctx, p.g, env)
		if err != nil {
			return fail(stdio, err, "")
		}
		s.be, mode = be, be.mode
		if be.warn != "" {
			fmt.Fprintf(stdio.Stderr, "notesteme: 警告：%s\n", be.warn)
		}
		defer func() {
			if err := be.close(); err != nil {
				fmt.Fprintf(stdio.Stderr, "notesteme: 關閉時發生錯誤：%v\n", err)
			}
		}()
	}

	code, err := p.run(ctx, s)
	if err != nil {
		r := fail(stdio, err, mode)
		return r
	}
	return Result{Code: code, Mode: mode}
}

// fail 印出錯誤並對應成結束碼；參數錯誤另外附上用法的提示。
func fail(stdio app.IO, err error, mode Mode) Result {
	code, errorCode := exitFor(err)
	fmt.Fprintf(stdio.Stderr, "notesteme: %v\n", err)
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintln(stdio.Stderr, "執行 notesteme --help 查看用法。")
	}
	return Result{Code: code, Mode: mode, ErrorCode: errorCode}
}

// newFlagSet 建立子命令的旗標集：支援位置參數與旗標穿插（`ingest URL --json`），錯誤由 parseFlags 回報。
func newFlagSet(name string, g *globals) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.SetInterspersed(true)
	g.bind(fs)
	return fs
}

// parseFlags 解析參數並回傳位置參數；旗標錯誤是 usageError。
func parseFlags(fs *pflag.FlagSet, args []string) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil, errHelp
		}
		return nil, usagef("%v", err)
	}
	return fs.Args(), nil
}

// parse 依第一個參數挑選子命令並解析其餘參數。
func parse(args []string) (*plan, error) {
	switch name := args[0]; name {
	case "ingest":
		return parseIngest(args[1:])
	case "items":
		return parseItems(args[1:])
	case "export":
		return parseExport(args[1:])
	case "doctor":
		return parseDoctor(args[1:])
	case "backup", "restore", "tokens", "secrets", "config", "mcp", "tools":
		// 屬於之後的切片（OPS、AUTH、SEC、MCP）：不判斷模式，直接回報還沒實作。
		return unimplemented(name), nil
	default:
		return nil, usagef("不認得的子命令 %q。", name)
	}
}

// unimplemented 回傳一個只回報「還沒實作」的子命令。
func unimplemented(name string) *plan {
	return &plan{g: &globals{}, noBackend: true, run: func(context.Context, *session) (int, error) {
		return 0, notImplementedf("notesteme %s 尚未實作。", name)
	}}
}

const usageText = `用法：notesteme <子命令> [旗標]

子命令：
  serve                                 啟動 server
  ingest <url|ref|path>                 新增影音（--profile、--until、--wait、--timeout）
  items ls | show <item> | rerun <item> --from <stage>
  export <item> --format srt|vtt|txt    輸出字幕（--lang、--variant、--out）
  doctor                                檢查外部工具與設定

全域旗標：--server <url> --token <t> --data <dir> --json
結束碼：0 成功、1 執行錯誤、2 參數錯誤、3 需要使用者處理、4 等待中
`
