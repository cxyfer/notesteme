package config

import (
	"errors"
	"net/url"
	"strings"
)

// hubSchema 是設定檔 hub 區段（HSHR-R1、R12、ADR-0032 第 4 點）：Client 連到哪一個 Hub。
// hub.url 只能寫在設定檔（API 能改的話，可以把 Client 的請求與 token 導到任意主機）。
type hubSchema struct {
	URL      string            `yaml:"url"`
	Precheck hubPrecheckSchema `yaml:"precheck"`
}

// hubPrecheckSchema 是 hub.precheck：ASR 前查詢（HSHR-R12），預設關閉。
type hubPrecheckSchema struct {
	Enabled bool `yaml:"enabled"`
}

// ErrHubURLNotHTTPS 是 hub.url 不是 https 網址時的錯誤（HSHR-R1，serve 啟動失敗）。
var ErrHubURLNotHTTPS = errors.New("設定 hub.url 必須是 https 網址。")

// parseHubURL 驗證 hub.url：必須是 https、有主機名稱，不含帳號密碼、查詢字串與 fragment；結尾的 / 去掉。
// 沒有設定時回傳空字串（分享與重新生成回 409 hub_not_configured，HSHR-R1）。
func parseHubURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrHubURLNotHTTPS
	}
	return strings.TrimRight(raw, "/"), nil
}
