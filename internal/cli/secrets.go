package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/cxyfer/notesteme/internal/app"
)

// parseSecrets 解析 `notesteme secrets rotate-key | reset`（SEC-R8、R9）。
//
// 兩者都直接操作資料目錄（取得檔案鎖，不經由 API），所以 noBackend 為 true；
// 不能和 --server 一起使用，serve 執行中時結束碼為 1。
func parseSecrets(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("secrets 需要子命令：rotate-key 或 reset。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "rotate-key":
		return parseSecretsRotateKey(rest)
	case "reset":
		return parseSecretsReset(rest)
	default:
		return nil, usagef("不認得的子命令 secrets %s。", sub)
	}
}

func parseSecretsRotateKey(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("secrets rotate-key", g)
	newKeyEnv := fs.String("new-key-env", "", "存放新 master key 的環境變數名稱")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("secrets rotate-key 不接受位置參數：%q。", pos[0])
	}
	envName := strings.TrimSpace(*newKeyEnv)
	if envName == "" {
		return nil, usagef("secrets rotate-key 需要 --new-key-env <變數名稱>。")
	}
	return &plan{g: g, noBackend: true, run: func(ctx context.Context, s *session) (int, error) {
		if g.server != "" {
			return 0, usagef("secrets rotate-key 直接改資料目錄，不能和 --server 一起使用。")
		}
		if err := app.RotateMasterKey(ctx, withData(s.env, g.data), envName); err != nil {
			return 0, err
		}
		if g.json {
			return exitOK, s.printJSON(map[string]any{"rotated": true})
		}
		fmt.Fprintln(s.stdout, "已輪替 master key；之後請使用新的 NOTESTEME_MASTER_KEY 啟動。")
		return exitOK, nil
	}}, nil
}

func parseSecretsReset(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("secrets reset", g)
	yes := fs.Bool("yes", false, "不詢問確認")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("secrets reset 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, noBackend: true, precheck: requireConfirmable(yes), run: func(ctx context.Context, s *session) (int, error) {
		if g.server != "" {
			return 0, usagef("secrets reset 直接改資料目錄，不能和 --server 一起使用。")
		}
		if !*yes && !s.confirm("要重設所有 secrets 與資料金鑰嗎？已儲存的 API key 與 cookie 都會刪除，無法復原。") {
			return s.declined()
		}
		if err := app.ResetSecrets(ctx, withData(s.env, g.data)); err != nil {
			return 0, err
		}
		if g.json {
			return exitOK, s.printJSON(map[string]any{"reset": true})
		}
		fmt.Fprintln(s.stdout, "已重設所有 secrets 與資料金鑰；影音、產出、設定與 token 皆保留，請重新輸入 API key 與 cookie。")
		return exitOK, nil
	}}, nil
}
