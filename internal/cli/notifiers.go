package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cxyfer/notesteme/internal/notify"
)

// parseNotifiers 解析 `notesteme notifiers ls | test <id>`（NOTIFY 的介面）。
func parseNotifiers(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("notifiers 需要子命令：ls 或 test。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "ls":
		return parseNotifiersLs(rest)
	case "test":
		return parseNotifiersTest(rest)
	default:
		return nil, usagef("不認得的子命令 notifiers %s。", sub)
	}
}

func parseNotifiersLs(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("notifiers ls", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("notifiers ls 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/notifiers", nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var body struct {
			Notifiers []notify.Info `json:"notifiers"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		for _, n := range body.Notifiers {
			state := "啟用"
			if !n.Enabled {
				state = "停用"
			}
			fmt.Fprintf(s.stdout, "%s\t%s\t%s\t%s\n", n.ID, n.Kind, state, strings.Join(n.Events, ","))
		}
		return exitOK, nil
	}}, nil
}

func parseNotifiersTest(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("notifiers test", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || !notify.ValidID(pos[0]) {
		return nil, usagef("notifiers test 需要一個 notifier 的 id。")
	}
	id := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/notifiers/"+url.PathEscape(id)+"/test", nil)
		if err != nil {
			return 0, err
		}
		var res notify.TestResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		if g.json {
			s.printRaw(raw)
		} else if res.OK {
			fmt.Fprintf(s.stdout, "已送出測試通知（HTTP %d）。\n", res.Status)
		} else {
			fmt.Fprintf(s.stdout, "測試通知沒有送達（%s）：%s\n", res.ErrorClass, res.Message)
		}
		if !res.OK {
			return exitError, nil
		}
		return exitOK, nil
	}}, nil
}
