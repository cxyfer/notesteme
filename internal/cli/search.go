package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// parseSearch 解析 `search <query> [--lang <l>] [--limit <n>] [--json]`（SRCH-R10）：呼叫 GET /api/search，
// 只讀一頁。查詢的驗證交給 API（422 的結束碼是 2，CLI-R6）。
// 非 JSON 輸出每行為 `<ref>  <start>  <text>`；字幕中的換行與 Tab 換成空白，一句只佔一行。
func parseSearch(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("search", g)
	lang := fs.String("lang", "", "只搜尋這個語言的字幕（BCP 47）")
	limit := fs.Int("limit", 0, "最多回傳幾筆（1～100，預設 20）")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("search 需要一個 <query>（含空白時請加引號）。")
	}
	params := url.Values{"mode": {"fulltext"}, "q": {pos[0]}}
	if *lang != "" {
		params.Set("lang", *lang)
	}
	if fs.Changed("limit") {
		params.Set("limit", strconv.Itoa(*limit))
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/search?"+params.Encode(), nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var res struct {
			Results []struct {
				Item struct {
					Ref string `json:"ref"`
				} `json:"item"`
				Start string `json:"start"`
				Text  string `json:"text"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		for _, r := range res.Results {
			fmt.Fprintf(s.stdout, "%s  %s  %s\n", r.Item.Ref, r.Start, oneLine(r.Text))
		}
		return exitOK, nil
	}}, nil
}
