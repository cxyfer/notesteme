package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
)

// parseTokens 解析 `notesteme tokens create | ls | revoke | rotate`（AUTH-R10、AUTH-R21）。
func parseTokens(args []string) (*plan, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, usagef("tokens 需要子命令：create、ls、revoke 或 rotate。")
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "create":
		return parseTokensCreate(rest)
	case "ls":
		return parseTokensLs(rest)
	case "revoke":
		return parseTokensRevoke(rest)
	case "rotate":
		return parseTokensRotate(rest)
	default:
		return nil, usagef("不認得的子命令 tokens %s。", sub)
	}
}

func parseTokensCreate(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("tokens create", g)
	name := fs.String("name", "", "token 名稱")
	scopesRaw := fs.String("scopes", "", "權限範圍（以逗號分隔：read、agent、ingest、admin）")
	expires := fs.String("expires", "", "有效期限（例如 30d、24h）")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("tokens create 不接受位置參數：%q。", pos[0])
	}
	if strings.TrimSpace(*name) == "" || hasControlChar(*name) || strings.ContainsRune(*name, '/') {
		return nil, usagef("tokens create 需要 --name <名稱>（不可為空、包含斜線或控制字元）。")
	}
	scopes, err := parseScopes(*scopesRaw)
	if err != nil {
		return nil, err
	}
	var expiresIn string
	if fs.Changed("expires") {
		if _, err := auth.ParseTokenDuration(*expires); err != nil {
			return nil, usagef("--expires 不正確：%v。", err)
		}
		expiresIn = *expires
	}
	tokenName := *name
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		body := map[string]any{
			"name":   tokenName,
			"scopes": scopes,
		}
		if expiresIn != "" {
			body["expires_in"] = expiresIn
		}
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/tokens", body)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var tok auth.CreatedToken
		if err := json.Unmarshal(raw, &tok); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已建立 token：%s（%s，權限：%s）\n", tok.Name, tok.ID, strings.Join(tok.Scopes, ","))
		if tok.ExpiresAt != nil {
			fmt.Fprintf(s.stdout, "到期時間：%s\n", tok.ExpiresAt.Format(time.RFC3339))
		}
		fmt.Fprintf(s.stdout, "Token（只會顯示這一次，請妥善保存）：%s\n", tok.Token)
		return exitOK, nil
	}}, nil
}

func parseTokensLs(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("tokens ls", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 0 {
		return nil, usagef("tokens ls 不接受位置參數：%q。", pos[0])
	}
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodGet, "/api/tokens", nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var body struct {
			Tokens []auth.TokenInfo `json:"tokens"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		now := s.clock.Now()
		for _, t := range body.Tokens {
			status := "有效"
			switch {
			case t.RevokedAt != nil:
				status = "已撤銷"
			case t.ExpiresAt != nil && !t.ExpiresAt.After(now):
				status = "已到期"
			}
			exp := "永不過期"
			if t.ExpiresAt != nil {
				exp = t.ExpiresAt.Format("2006-01-02")
			}
			fmt.Fprintf(s.stdout, "%s\t%s\t%s...\t%s\t%s\t%s\n",
				t.ID, t.Name, t.Prefix, strings.Join(t.Scopes, ","), status, exp)
		}
		return exitOK, nil
	}}, nil
}

func parseTokensRevoke(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("tokens revoke", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || strings.TrimSpace(pos[0]) == "" {
		return nil, usagef("tokens revoke 需要一個 <name|id>。")
	}
	target := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodDelete, "/api/tokens/"+url.PathEscape(target), nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		fmt.Fprintf(s.stdout, "已撤銷 token：%s\n", target)
		return exitOK, nil
	}}, nil
}

// parseTokensRotate 解析 `notesteme tokens rotate <name> [--json]`（AUTH-R21）：產生新的 secret，舊的立即失效；
// 名稱、scope、到期時間不變。新 token 只顯示這一次。
func parseTokensRotate(args []string) (*plan, error) {
	g := &globals{}
	fs := newFlagSet("tokens rotate", g)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return nil, err
	}
	if len(pos) != 1 || strings.TrimSpace(pos[0]) == "" {
		return nil, usagef("tokens rotate 需要一個 <name|id>。")
	}
	target := pos[0]
	return &plan{g: g, run: func(ctx context.Context, s *session) (int, error) {
		raw, _, err := s.be.call(ctx, http.MethodPost, "/api/tokens/"+url.PathEscape(target)+"/rotate", nil)
		if err != nil {
			return 0, err
		}
		if g.json {
			s.printRaw(raw)
			return exitOK, nil
		}
		var tok auth.CreatedToken
		if err := json.Unmarshal(raw, &tok); err != nil {
			return 0, fmt.Errorf("回應不是預期的 JSON：%w", err)
		}
		fmt.Fprintf(s.stdout, "已輪替 token：%s（%s，權限：%s），舊的 token 已經失效\n", tok.Name, tok.ID, strings.Join(tok.Scopes, ","))
		if tok.ExpiresAt != nil {
			fmt.Fprintf(s.stdout, "到期時間：%s\n", tok.ExpiresAt.Format(time.RFC3339))
		}
		fmt.Fprintf(s.stdout, "Token（只會顯示這一次，請妥善保存）：%s\n", tok.Token)
		return exitOK, nil
	}}, nil
}

// parseScopes 解析並驗證逗號分隔的 token scope 清單。
func parseScopes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, usagef("tokens create 需要 --scopes <權限>（可用逗號分隔：read、agent、ingest、admin）。")
	}
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		sc := strings.TrimSpace(p)
		if !auth.ValidScope(sc) {
			return nil, usagef("--scopes 包含不支援的權限 %q（支援：read、agent、ingest、admin）。", sc)
		}
		if _, ok := seen[sc]; ok {
			continue
		}
		seen[sc] = struct{}{}
		out = append(out, sc)
	}
	return out, nil
}

func hasControlChar(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
