package cli

// 這個檔案是 Client 與 Hub 的子命令（HSHR 的「介面」）：hub share、hub import、hub status。
// 分享與重新生成經 server 的 API（POST /api/items/:id/hub/share、POST /api/hub/import），CLI 不直接連 Hub。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func parseHub(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("hub 需要子命令：share、import 或 status。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "share":
		return parseHubShare(rest)
	case "import":
		return parseHubImport(rest)
	case "status":
		return parseHubStatus(rest)
	default:
		return nil, usagef("不認得的子命令 hub %s。", sub)
	}
}

// hub share <item> --license <SPDX> --yes [--artifact <variant>[:<lang>]]… [--include-frames]（HSHR-R2）。
// --yes 等同 attest: true；沒有 --yes 時由 server 回 attest_required，不連線到 Hub。
func parseHubShare(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("hub share", g)
	license := fs.String("license", "", "授權（SPDX）：CC-BY-4.0、CC-BY-SA-4.0、CC-BY-NC-4.0、CC-BY-NC-SA-4.0 或 CC0-1.0")
	yes := fs.Bool("yes", false, "確認你有權分享這些內容")
	arts := fs.StringArray("artifact", nil, "只分享這個字幕：<variant>[:<lang>]，可以重複")
	frames := fs.Bool("include-frames", false, "也分享選中的畫面")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("hub share 需要一個影音：notesteme hub share <item> --license <SPDX> --yes。")
	}
	body := map[string]any{"license": *license, "attest": *yes}
	if *frames {
		body["include_frames"] = true
	}
	var list []map[string]string
	for _, a := range *arts {
		variant, lang, _ := strings.Cut(a, ":")
		if variant == "" {
			return nil, usagef("--artifact 要寫成 <variant>[:<lang>]：%q。", a)
		}
		ref := map[string]string{"kind": "transcript", "variant": variant}
		if variant == "note" || variant == "chapters" {
			ref = map[string]string{"kind": variant}
		}
		if lang != "" {
			ref["lang"] = lang
		}
		list = append(list, ref)
	}
	if len(list) > 0 {
		body["artifacts"] = list
	}
	item := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/items/"+url.PathEscape(item)+"/hub/share", body)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var v struct {
			ShareID  string `json:"share_id"`
			Versions []struct {
				Kind, Variant, Lang string
				VersionID           string `json:"version_id"`
				Deduplicated        bool   `json:"deduplicated"`
			} `json:"versions"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已分享到 Hub：%s\n", oneLine(v.ShareID))
		for _, x := range v.Versions {
			dup := ""
			if x.Deduplicated {
				dup = "（Hub 上已有相同內容）"
			}
			fmt.Fprintf(s.stdout, "  %s %s %s → %s%s\n", oneLine(x.Kind), oneLine(x.Variant), oneLine(x.Lang), oneLine(x.VersionID), dup)
		}
		return exitOK, nil
	}}, nil
}

// hub import <version-id> [--profile <p>]（HSHR-R9）。
func parseHubImport(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("hub import", g)
	profile := fs.String("profile", "", "處理方案")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 {
		return nil, usagef("hub import 需要 Hub 上的版本 ID：notesteme hub import <version-id>。")
	}
	body := map[string]any{"version": pos[0]}
	if *profile != "" {
		body["profile"] = *profile
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/hub/import", body)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var v struct {
			Item struct{ ID, Ref string } `json:"item"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已排入重新生成：%s（%s）\n", oneLine(v.Item.Ref), oneLine(v.Item.ID))
		return exitOK, nil
	}}, nil
}

// hub status：顯示 GET /api/settings/hub，並以 POST /api/settings/hub/test 測試連線（Hub 的 GET /api/v1/me）。
func parseHubStatus(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("hub status", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("hub status 不接受參數。")
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/settings/hub", nil)
		if err != nil {
			return 0, err
		}
		var v struct {
			URL      string `json:"url"`
			TokenSet bool   `json:"token_set"`
			Precheck struct {
				Enabled bool `json:"enabled"`
			} `json:"precheck"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		var me json.RawMessage
		if v.URL != "" && v.TokenSet {
			if me, _, err = s.be.call(ctx, http.MethodPost, "/api/settings/hub/test", nil); err != nil {
				return 0, err
			}
		}
		if g.json {
			return exitOK, s.printJSON(map[string]any{"settings": json.RawMessage(raw), "me": me})
		}
		url := v.URL
		if url == "" {
			url = "（沒有設定）"
		}
		fmt.Fprintf(s.stdout, "Hub：%s\ntoken：%s\nASR 前查詢：%s\n", oneLine(url), yesNo(v.TokenSet, "已設定", "沒有設定"), yesNo(v.Precheck.Enabled, "開啟", "關閉"))
		if me != nil {
			var m struct {
				DisplayName string   `json:"display_name"`
				Permissions []string `json:"permissions"`
			}
			_ = json.Unmarshal(me, &m)
			fmt.Fprintf(s.stdout, "連線：成功（%s，權限 %s）\n", oneLine(m.DisplayName), oneLine(strings.Join(m.Permissions, ",")))
		}
		return exitOK, nil
	}}, nil
}

func yesNo(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}
