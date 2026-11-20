package cli

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/config"
)

// parseBackup 解析 `notesteme backup [--out <dir>]`（OPS-R1）。
//
// 備份經由 API（POST /api/backup）取得，所以 serve 執行中也可以做；server 不寫入 client 指定的路徑，
// 備份串流由 CLI 寫到 --out（預設 <資料目錄>/backups），並更新 notesteme-backup-latest.tar.gz。
func parseBackup(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("backup", g)
	out := fs.String("out", "", "備份檔放的目錄；預設是 <資料目錄>/backups")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("backup 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		dir := *out
		if dir == "" {
			cfg, err := config.Load(config.Sources{Env: withData(s.env, g.data).Vars})
			if err != nil {
				return 0, err
			}
			dir = filepath.Join(cfg.Data, "backups")
		}
		resp, err := s.be.open(ctx, http.MethodPost, "/api/backup", nil)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return 0, errorFromResponse(resp.StatusCode, raw)
		}
		name, err := backupName(resp)
		if err != nil {
			return 0, err
		}
		path, err := backup.SaveTo(dir, name, resp.Body)
		if err != nil {
			return 0, err
		}
		var size int64
		if fi, err := os.Stat(path); err == nil {
			size = fi.Size()
		}
		latest := filepath.Join(dir, backup.LatestName)
		if g.json {
			return exitOK, s.printJSON(map[string]any{"file": path, "latest": latest, "bytes": size})
		}
		fmt.Fprintf(s.stdout, "備份完成：%s（%d 位元組）\n最新的備份：%s\n", path, size, latest)
		return exitOK, nil
	}}, nil
}

// backupName 取出 server 建議的檔名（Content-Disposition）。檔名來自 server 的回應，只接受備份檔名的格式，
// 不能夾帶路徑；否則 server（或中間的 proxy）可以讓 CLI 寫到任意位置。
func backupName(resp *http.Response) (string, error) {
	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil || !backup.ValidFileName(params["filename"]) {
		return "", fmt.Errorf("server 回應的備份檔名不正確：%q。", resp.Header.Get("Content-Disposition"))
	}
	return params["filename"], nil
}
