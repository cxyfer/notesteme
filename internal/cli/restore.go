package cli

import (
	"context"
	"fmt"

	"github.com/cxyfer/notesteme/internal/app"
)

// parseRestore 解析 `notesteme restore <file>`（OPS-R2）。
//
// 還原不經過 API，只取得資料目錄的鎖（CLI-R2 的例外），所以不判斷模式；serve 執行中時結束碼 1。
// 它改的是這台機器的資料目錄，不能和 --server 一起用。
func parseRestore(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("restore", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("restore 需要一個 <file>（備份檔）。")
	}
	file := pos[0]
	return &plan{g: g, noBackend: true, run: func(ctx context.Context, s *session) (int, error) {
		if g.server != "" {
			return 0, usagef("restore 直接改資料目錄，不能和 --server 一起使用。")
		}
		res, err := app.Restore(ctx, withData(s.env, g.data), file)
		if err != nil {
			return 0, err
		}
		if g.json {
			return exitOK, s.printJSON(map[string]any{
				"schema_version": res.SchemaVersion, "created_at": res.CreatedAt,
				"pre_restore": res.PreRestore, "config_dir": res.ConfigDir,
			})
		}
		fmt.Fprintf(s.stdout, "已還原備份（schema 版本 %d，建立於 %s）。\n", res.SchemaVersion, res.CreatedAt.Format("2006-01-02 15:04:05 MST"))
		if res.PreRestore != "" {
			fmt.Fprintf(s.stdout, "還原前的資料庫已存到：%s\n", res.PreRestore)
		}
		if res.ConfigDir != "" {
			fmt.Fprintf(s.stdout, "備份中的設定檔已解到：%s\n（不會覆寫 /config，請自行比對後替換。）\n", res.ConfigDir)
		}
		fmt.Fprintln(s.stdout, "注意：備份中的資料金鑰是用備份當時的 master key 加密的。"+
			"若 master key 在那之後輪替過，notesteme serve 會以 SEC-R5 的訊息（master key 不正確，無法解開資料金鑰）拒絕啟動；"+
			"請改用備份當時的 NOTESTEME_MASTER_KEY。")
		return exitOK, nil
	}}, nil
}
