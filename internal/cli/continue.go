package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// maxContinueBatch 是批次 API 一次可接受的 ids 數量（DIG-R7）。
const maxContinueBatch = 100

// parseItemsContinue 解析 `items continue <ref>...`（DIG 的介面）：呼叫批次 API，
// 超過 100 個時分批送出；任何一部沒有被繼續時結束碼為 1。
func parseItemsContinue(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("items continue", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) == 0 {
		return nil, usagef("items continue 需要至少一個 <ref>。")
	}
	for _, p := range pos {
		if strings.TrimSpace(p) == "" {
			return nil, usagef("items continue 的 <ref> 不能是空字串。")
		}
	}
	refs := pos
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		type skipped struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		}
		var all struct {
			Continued []string  `json:"continued"`
			Skipped   []skipped `json:"skipped"`
		}
		all.Continued, all.Skipped = []string{}, []skipped{}
		for i := 0; i < len(refs); i += maxContinueBatch {
			batch := refs[i:min(i+maxContinueBatch, len(refs))]
			raw, _, err := s.be.call(ctx, http.MethodPost, "/api/items/continue", map[string]any{"ids": batch})
			if err != nil {
				return 0, err
			}
			var res struct {
				Continued []string  `json:"continued"`
				Skipped   []skipped `json:"skipped"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
			}
			all.Continued = append(all.Continued, res.Continued...)
			all.Skipped = append(all.Skipped, res.Skipped...)
		}
		code := exitOK
		if len(all.Skipped) > 0 {
			code = exitError
		}
		if g.json {
			return code, s.printJSON(all)
		}
		for _, id := range all.Continued {
			fmt.Fprintf(s.stdout, "已繼續：%s\n", id)
		}
		for _, sk := range all.Skipped {
			fmt.Fprintf(s.stdout, "略過：%s（%s）\n", sk.ID, continueSkipReason(sk.Code))
		}
		return code, nil
	}}, nil
}

// continueSkipReason 把批次 API 的 code 換成說明；不認得的 code 原樣顯示。
func continueSkipReason(code string) string {
	switch code {
	case "not_found":
		return "找不到這部影音"
	case "in_trash":
		return "影音在垃圾桶中"
	case "not_awaiting_user":
		return "沒有在等待你決定是否繼續"
	}
	return code
}
