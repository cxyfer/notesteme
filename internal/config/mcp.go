package config

import (
	"fmt"
	"net/url"
	"strings"
)

// parseMCP 驗證設定檔 mcp 區段（AUTH-R11）並寫入 cfg。
func parseMCP(f *mcpSchema, path string, cfg *Config) error {
	for i, v := range f.AllowedOrigins {
		o, ok := NormalizeOrigin(v)
		if !ok {
			return fmt.Errorf("設定檔 %s 的欄位 mcp.allowed_origins[%d] 必須是 http:// 或 https:// 開頭、不含路徑的來源（例如 \"https://notes.example.com\"）：%q", path, i, v)
		}
		cfg.MCPAllowedOrigins = append(cfg.MCPAllowedOrigins, o)
	}
	for i, v := range f.AllowedHosts {
		h := strings.ToLower(strings.TrimSpace(v))
		if h == "" || strings.ContainsAny(h, "/:@ *") {
			return fmt.Errorf("設定檔 %s 的欄位 mcp.allowed_hosts[%d] 必須是主機名稱（不含 port 與萬用字元，例如 \"notes.example.com\"）：%q", path, i, v)
		}
		cfg.MCPAllowedHosts = append(cfg.MCPAllowedHosts, h)
	}
	return nil
}

// NormalizeOrigin 把 Origin 正規化成小寫的 scheme://host[:port]（去掉預設 port）；不是合法的 http(s) 來源時 ok 為 false。
func NormalizeOrigin(v string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	out := u.Scheme + "://" + host
	if port != "" {
		out += ":" + port
	}
	return out, true
}
