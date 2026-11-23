package cli

import (
	"context"
	"fmt"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/ops"
)

// parseDeployCheck 解析 `notesteme deploy-check vps`（OPS-R20）：compose 的 vps-check 服務執行它，
// 檢查不通過時 Caddy 不會啟動。它不經 API、不取得資料目錄的鎖，只以唯讀方式開 DB（CLI-R2 的例外，同 restore）。
func parseDeployCheck(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("deploy-check", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || pos[0] != "vps" {
		return nil, usagef("用法：notesteme deploy-check vps")
	}
	return &plan{g: g, noBackend: true, run: func(ctx context.Context, s *session) (int, error) {
		if g.server != "" {
			return 0, usagef("deploy-check 檢查這台機器的資料目錄，不能和 --server 一起使用。")
		}
		env := withData(s.env, g.data).Vars
		ok, msg, err := ops.CheckVPS(env, func() (bool, error) {
			cfg, err := config.Load(config.Sources{Env: env})
			if err != nil {
				return false, err
			}
			return ops.PasswordInDB(ctx, cfg.Data)
		})
		if err != nil {
			return 0, err
		}
		if g.json {
			if err := s.printJSON(map[string]any{"ok": ok, "message": msg}); err != nil {
				return 0, err
			}
		} else if ok {
			fmt.Fprintln(s.stdout, msg)
		} else {
			fmt.Fprintln(s.stderr, msg)
		}
		if !ok {
			return exitError, nil
		}
		return exitOK, nil
	}}, nil
}
