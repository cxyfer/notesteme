// Package hubcli 是 notesteme-hub 的命令列（ADR-0030 第 1 點）：serve、migrate、admin、doctor。
// cmd/notesteme-hub 只把 os 的內容轉成 Env 再呼叫 Run，這裡不讀 os 的環境，測試可以在程序內執行。
package hubcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite"
	"github.com/cxyfer/notesteme/internal/netguard"
)

// 結束碼：0 成功、1 執行失敗、2 命令列參數錯誤（同 Client 的 CLI）。
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Env 是命令列的外部依賴。
type Env struct {
	Vars   map[string]string
	Stdout io.Writer
	Stderr io.Writer
	Clock  clock.Clock
	Net    netguard.Deps
	Listen func(network, addr string) (net.Listener, error)
}

const usage = `用法：notesteme-hub <子命令> [參數]

子命令：
  serve                                     啟動 Hub（設定檔 $NOTESTEME_HUB_CONFIG，預設 /config/notesteme-hub.yaml）
  migrate                                   套用資料庫 migration 後結束
  admin grant|revoke --provider <p> --subject <s>   指定或移除管理員（直接操作 DB，給第一位管理員與救援用）
  admin suspend <user-id>                   停用帳號
  doctor                                    檢查設定、資料庫版本與登入方式
  version                                   顯示版本
`

// Run 執行一個子命令並回傳結束碼。
func Run(ctx context.Context, args []string, env Env) int {
	if env.Clock == nil {
		env.Clock = clock.Real{}
	}
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "serve":
		return serve(ctx, env)
	case "migrate":
		return migrate(ctx, env)
	case "admin":
		return admin(args[1:], env)
	case "doctor":
		return doctor(ctx, env)
	case "version", "--version", "-v":
		fmt.Fprintln(env.Stdout, "notesteme-hub", hub.Version())
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(env.Stdout, usage)
		return ExitOK
	}
	fmt.Fprintf(env.Stderr, "不認得的子命令「%s」。\n\n%s", args[0], usage)
	return ExitUsage
}

func loadConfig(env Env) (*hubconfig.Config, bool) {
	cfg, err := hubconfig.Load(hubconfig.Sources{Env: env.Vars})
	if err != nil {
		fmt.Fprintln(env.Stderr, err)
		return nil, false
	}
	return cfg, true
}

func serve(ctx context.Context, env Env) int {
	cfg, ok := loadConfig(env)
	if !ok {
		return ExitError
	}
	log := slog.New(slog.NewJSONHandler(env.Stderr, nil))
	s, err := hub.Open(ctx, hub.Options{Config: cfg, Clock: env.Clock, Log: log, Net: &env.Net})
	if err != nil {
		fmt.Fprintln(env.Stderr, "啟動 Hub 失敗：", err)
		return ExitError
	}
	defer s.Close()
	listen := env.Listen
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp", cfg.Listen)
	if err != nil {
		fmt.Fprintln(env.Stderr, "無法監聽", cfg.Listen, "：", err)
		return ExitError
	}
	log.Info("notesteme-hub 已啟動", "listen", ln.Addr().String(), "public_url", cfg.PublicURL, "version", hub.Version())
	if err := s.Serve(ctx, ln); err != nil {
		fmt.Fprintln(env.Stderr, "Hub 停止時發生錯誤：", err)
		return ExitError
	}
	return ExitOK
}

func migrate(ctx context.Context, env Env) int {
	cfg, ok := loadConfig(env)
	if !ok {
		return ExitError
	}
	s, err := hub.Open(ctx, hub.Options{Config: cfg, Clock: env.Clock, Net: &env.Net})
	if err != nil {
		fmt.Fprintln(env.Stderr, "migration 失敗：", err)
		return ExitError
	}
	defer s.Close()
	v, err := s.Store().SchemaVersion(ctx)
	if err != nil {
		fmt.Fprintln(env.Stderr, "讀取 schema 版本失敗：", err)
		return ExitError
	}
	fmt.Fprintf(env.Stdout, "Hub 資料庫的 schema 版本：%d\n", v)
	return ExitOK
}

// admin 是 HACC 介面的救援命令；M5-S0 只檢查參數，實作留給 HACC 的切片（S2）。
func admin(args []string, env Env) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, "用法：notesteme-hub admin grant|revoke --provider <p> --subject <s>，或 admin suspend <user-id>\n")
		return ExitUsage
	}
	switch args[0] {
	case "grant", "revoke":
		fs := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
		fs.SetOutput(env.Stderr)
		provider := fs.String("provider", "", "登入方式（google、github、linuxdo）")
		subject := fs.String("subject", "", "供應商的使用者 ID（GitHub、Linux.do 是數字）")
		if err := fs.Parse(args[1:]); err != nil {
			return ExitUsage
		}
		if !slices.Contains(hubconfig.Providers, hubconfig.Provider(*provider)) || strings.TrimSpace(*subject) == "" || fs.NArg() > 0 {
			fmt.Fprintln(env.Stderr, "admin "+args[0]+" 要指定 --provider（google、github、linuxdo）與 --subject。")
			return ExitUsage
		}
	case "suspend":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(env.Stderr, "用法：notesteme-hub admin suspend <user-id>")
			return ExitUsage
		}
	default:
		fmt.Fprintf(env.Stderr, "不認得的 admin 子命令「%s」（grant、revoke、suspend）。\n", args[0])
		return ExitUsage
	}
	fmt.Fprintln(env.Stderr, "notesteme-hub admin "+args[0]+" 尚未實作。")
	return ExitError
}

// doctor 檢查設定與資料庫，不套用 migration、不修改任何東西；有問題時結束碼 1。
func doctor(ctx context.Context, env Env) int {
	cfg, ok := loadConfig(env)
	if !ok {
		return ExitError
	}
	out := env.Stdout
	problems := 0
	fmt.Fprintf(out, "設定檔：%s\npublic_url：%s\n資料目錄：%s\n", cfg.ConfigFile, cfg.PublicURL, cfg.Data)
	fmt.Fprintf(out, "儲存：store.driver=%s、blobs.driver=%s\n", cfg.StoreDriver, cfg.BlobsDriver)
	if cfg.StoreDriver != hubconfig.StoreSQLite || cfg.BlobsDriver != hubconfig.BlobsFS {
		fmt.Fprintln(out, "  ✗ 目前只支援 sqlite 與 fs（postgres、s3 尚未實作）")
		problems++
	}
	var enabled []string
	for _, p := range hubconfig.Providers {
		c := cfg.OAuth[p]
		switch {
		case c.Enabled():
			enabled = append(enabled, string(p))
		case c.ClientID != "":
			fmt.Fprintf(out, "  ✗ 登入方式 %s 有 client_id，但沒有環境變數 %s\n", p, hubconfig.OAuthSecretEnv(p))
			problems++
		}
	}
	if len(enabled) == 0 {
		fmt.Fprintln(out, "  ✗ 沒有啟用任何登入方式")
		problems++
	} else {
		fmt.Fprintf(out, "登入方式：%s\n", strings.Join(enabled, "、"))
	}
	if cfg.StoreDriver == hubconfig.StoreSQLite {
		st, err := sqlite.Open(ctx, cfg.Data, env.Clock, sqlite.Options{NoMigrate: true})
		if err != nil {
			fmt.Fprintf(out, "  ✗ 資料庫：%v\n", err)
			problems++
		} else {
			v, _ := st.SchemaVersion(ctx)
			n, _ := st.CountUsers(ctx)
			fmt.Fprintf(out, "資料庫：schema 版本 %d、%d 個帳號\n", v, n)
			_ = st.Close()
		}
	}
	if problems > 0 {
		fmt.Fprintf(out, "發現 %d 個問題。\n", problems)
		return ExitError
	}
	fmt.Fprintln(out, "沒有發現問題。")
	return ExitOK
}
