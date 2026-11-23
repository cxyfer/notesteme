// Package hubcli 是 notesteme-hub 的命令列（ADR-0030 第 1 點）：serve、migrate、admin、doctor。
// cmd/notesteme-hub 只把 os 的內容轉成 Env 再呼叫 Run，這裡不讀 os 的環境，測試可以在程序內執行。
package hubcli

import (
	"context"
	"errors"
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
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres"
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
  admin unsuspend <user-id>                 恢復帳號（救援用）
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
		return admin(ctx, args[1:], env)
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
	// 下架申請（不需要登入）的聯絡 email 以主金鑰加密，serve 一律需要它（HACC-R15）。
	if cfg.MasterKey == "" {
		fmt.Fprintln(env.Stderr, hubconfig.ErrNoMasterKey)
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

// openStoreNoMigrate 開啟儲存但不套用 migration（doctor 用），回傳程式支援的最新 schema 版本。
func openStoreNoMigrate(ctx context.Context, cfg *hubconfig.Config, clk clock.Clock) (hubstore.Store, int64, error) {
	switch cfg.StoreDriver {
	case hubconfig.StorePostgres:
		st, err := postgres.Open(ctx, cfg.StoreDSN, clk, postgres.Options{NoMigrate: true})
		if err != nil {
			return nil, 0, err
		}
		return st, postgres.LatestVersion(), nil
	default:
		st, err := sqlite.Open(ctx, cfg.Data, clk, sqlite.Options{NoMigrate: true})
		if err != nil {
			return nil, 0, err
		}
		return st, sqlite.LatestVersion(), nil
	}
}

// admin 是 HACC 介面的救援命令（直接操作 DB，不經過 API；稽核紀錄的執行者是「notesteme-hub admin」）：
//   - grant、revoke 以 source: cli 把 (provider, subject) 的帳號加入或移出 admins（設定檔的 admins 不受影響，
//     啟動時依設定檔的同步也不會清掉它）。身分要先登入過 Hub 一次；還沒登入過的請寫在設定檔的 admins。
//   - suspend、unsuspend 停用或恢復帳號（unsuspend 是規格之外的救援命令：所有管理員都被停用時用它恢復）。
func admin(ctx context.Context, args []string, env Env) int {
	const use = "用法：notesteme-hub admin grant|revoke --provider <p> --subject <s>，或 admin suspend|unsuspend <user-id>\n"
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, use)
		return ExitUsage
	}
	var provider, subject, userID string
	switch args[0] {
	case "grant", "revoke":
		fs := flag.NewFlagSet("admin "+args[0], flag.ContinueOnError)
		fs.SetOutput(env.Stderr)
		p := fs.String("provider", "", "登入方式（google、github、linuxdo）")
		sub := fs.String("subject", "", "供應商的使用者 ID（GitHub、Linux.do 是數字）")
		if err := fs.Parse(args[1:]); err != nil {
			return ExitUsage
		}
		provider, subject = *p, strings.TrimSpace(*sub)
		if !slices.Contains(hubconfig.Providers, hubconfig.Provider(provider)) || subject == "" || fs.NArg() > 0 {
			fmt.Fprintln(env.Stderr, "admin "+args[0]+" 要指定 --provider（google、github、linuxdo）與 --subject。")
			return ExitUsage
		}
	case "suspend", "unsuspend":
		if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
			fmt.Fprintln(env.Stderr, "用法：notesteme-hub admin "+args[0]+" <user-id>")
			return ExitUsage
		}
		userID = strings.TrimSpace(args[1])
	default:
		fmt.Fprintf(env.Stderr, "不認得的 admin 子命令「%s」（grant、revoke、suspend、unsuspend）。\n", args[0])
		return ExitUsage
	}
	cfg, ok := loadConfig(env)
	if !ok {
		return ExitError
	}
	st, err := hub.OpenStore(ctx, cfg, env.Clock)
	if err != nil {
		fmt.Fprintln(env.Stderr, "開啟 Hub 資料庫失敗：", err)
		return ExitError
	}
	defer func() { _ = st.Close() }()
	if userID == "" {
		id, err := st.IdentityBySubject(ctx, provider, subject)
		if errors.Is(err, hubstore.ErrNotFound) {
			fmt.Fprintf(env.Stderr, "找不到 %s 的身分 %s：請先用它登入 Hub 一次，或改寫在設定檔的 admins。\n", provider, subject)
			return ExitError
		}
		if err != nil {
			fmt.Fprintln(env.Stderr, "查詢身分失敗：", err)
			return ExitError
		}
		userID = id.UserID
	}
	var changed bool
	switch args[0] {
	case "grant", "revoke":
		changed, err = st.SetCLIGroup(ctx, userID, hubconfig.GroupAdmins, args[0] == "grant")
	case "suspend":
		changed, err = st.SuspendUser(ctx, hubstore.CLIActorID, userID, "由 notesteme-hub admin 停用")
	case "unsuspend":
		changed, err = st.UnsuspendUser(ctx, hubstore.CLIActorID, userID)
	}
	if errors.Is(err, hubstore.ErrNotFound) {
		fmt.Fprintf(env.Stderr, "找不到帳號 %s。\n", userID)
		return ExitError
	}
	if err != nil {
		fmt.Fprintln(env.Stderr, "admin "+args[0]+" 失敗：", err)
		return ExitError
	}
	msg := map[string][2]string{
		"grant":     {"已把帳號 %s 加入 admins。", "帳號 %s 已經由命令列加入 admins，沒有變更。"},
		"revoke":    {"已把帳號 %s 移出 admins（只移除命令列加入的資格；設定檔的 admins 要改設定檔）。", "帳號 %s 沒有由命令列加入的 admins 資格，沒有變更。"},
		"suspend":   {"已停用帳號 %s。", "帳號 %s 已經是停用的，沒有變更。"},
		"unsuspend": {"已恢復帳號 %s（停用前的 session 與 Hub token 已失效）。", "帳號 %s 沒有停用，沒有變更。"},
	}[args[0]]
	if changed {
		fmt.Fprintf(env.Stdout, msg[0]+"\n", userID)
	} else {
		fmt.Fprintf(env.Stdout, msg[1]+"\n", userID)
	}
	return ExitOK
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
		if cfg.SessionKey == "" {
			fmt.Fprintf(out, "  ✗ 沒有環境變數 %s（session 金鑰）\n", hubconfig.EnvSessionKey)
			problems++
		}
	}
	if cfg.MasterKey == "" {
		fmt.Fprintf(out, "  ✗ 沒有環境變數 %s（主金鑰，加密下架申請的聯絡 email）\n", hubconfig.EnvMasterKey)
		problems++
	}
	if st, latest, err := openStoreNoMigrate(ctx, cfg, env.Clock); err != nil {
		fmt.Fprintf(out, "  ✗ 資料庫：%v\n", err)
		problems++
	} else {
		v, err := st.SchemaVersion(ctx)
		switch {
		case err != nil:
			fmt.Fprintf(out, "  ✗ 讀取資料庫的 schema 版本失敗：%v\n", err)
			problems++
		case v < latest:
			fmt.Fprintf(out, "  ✗ 資料庫的 schema 版本 %d 落後程式的 %d，請先執行 notesteme-hub migrate。\n", v, latest)
			problems++
		default:
			n, err := st.CountUsers(ctx)
			if err != nil {
				fmt.Fprintf(out, "  ✗ 讀取帳號數失敗：%v\n", err)
				problems++
			} else {
				fmt.Fprintf(out, "資料庫：schema 版本 %d、%d 個帳號\n", v, n)
			}
		}
		_ = st.Close()
	}
	if cfg.BlobsDriver == hubconfig.BlobsS3 {
		if err := hub.CheckBlobs(ctx, cfg); err != nil {
			fmt.Fprintf(out, "  ✗ 檔案儲存：%v\n", err)
			problems++
		} else {
			fmt.Fprintf(out, "檔案儲存：S3 bucket %s 可以使用\n", cfg.S3.Bucket)
		}
	}
	if problems > 0 {
		fmt.Fprintf(out, "發現 %d 個問題。\n", problems)
		return ExitError
	}
	fmt.Fprintln(out, "沒有發現問題。")
	return ExitOK
}
