package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cxyfer/notesteme/internal/tools/ytdlp"
)

// parseTools 解析 `notesteme tools update [--pin <version>] [--unpin]`（OPS-R6、R7）。
func parseTools(args []string) (*plan, error) {
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		return nil, usagef("tools 需要子命令：update。")
	}
	switch sub := args[0]; sub {
	case "update":
		return parseToolsUpdate(args[1:])
	default:
		return nil, usagef("不認得的子命令 tools %s。", sub)
	}
}

func parseToolsUpdate(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("tools update", g)
	pin := fs.String("pin", "", "固定在這個版本（YYYY.MM.DD）；固定後手動與排程的更新都略過")
	unpin := fs.Bool("unpin", false, "解除固定")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("tools update 不接受位置參數：%q。", pos[0])
	}
	if fs.Changed("pin") && *unpin {
		return nil, usagef("--pin 與 --unpin 不能同時使用。")
	}
	// 版本格式在這裡先擋一次（server 仍會檢查）：不合法的字串不該送出去。
	if fs.Changed("pin") && !ytdlp.ValidVersion(*pin) {
		return nil, usagef("--pin 必須是 YYYY.MM.DD 或 YYYY.MM.DD.N 的格式，收到 %q。", *pin)
	}
	// 沒有旗標時不送 body（更新到最新版）。body 要用 any：nil 的 map 放進 any 之後不是 nil，會被送成 JSON null。
	var body any
	switch {
	case *unpin:
		body = map[string]any{"unpin": true}
	case fs.Changed("pin"):
		body = map[string]any{"pin": *pin}
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/tools/yt-dlp/update", body)
		if err != nil {
			return 0, err
		}
		var res ytdlp.Result
		if err := json.Unmarshal(raw, &res); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		if s.g.json {
			s.printRaw(raw)
		} else {
			fmt.Fprintln(s.stdout, res.Message)
		}
		// 新版本沒有通過自我檢查（已回退）：更新沒有成功，結束碼 1。因為固定而略過不是失敗，結束碼 0（OPS-R7）。
		if res.RolledBack {
			return exitError, nil
		}
		return exitOK, nil
	}}, nil
}
