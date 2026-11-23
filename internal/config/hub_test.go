package config

import (
	"errors"
	"strings"
	"testing"
)

func TestHubSection(t *testing.T) {
	cfg, err := loadYAML(t, "hub:\n  url: https://hub.example.org/\n  precheck: { enabled: true }\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubURL != "https://hub.example.org" || !cfg.HubPrecheckEnabled {
		t.Errorf("HubURL = %q, precheck = %v", cfg.HubURL, cfg.HubPrecheckEnabled)
	}
	cfg, err = loadYAML(t, "listen: 127.0.0.1:1\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HubURL != "" || cfg.HubPrecheckEnabled {
		t.Errorf("預設值 = %q %v，預期沒有 Hub、查詢關閉", cfg.HubURL, cfg.HubPrecheckEnabled)
	}
}

// HSHR-R1：hub.url 必須是 https 網址，否則 serve 啟動失敗。
func TestHubURLMustBeHTTPS(t *testing.T) {
	for _, u := range []string{
		"http://hub.example.org", "ftp://hub.example.org", "hub.example.org", "https://", "https:///x",
		"https://u:p@hub.example.org", "https://hub.example.org?x=1", "https://hub.example.org#f", "https:hub",
	} {
		_, err := loadYAML(t, "hub:\n  url: \""+u+"\"\n")
		if err == nil || !errors.Is(err, ErrHubURLNotHTTPS) || !strings.Contains(err.Error(), "設定 hub.url 必須是 https 網址。") {
			t.Errorf("hub.url %q：err = %v", u, err)
		}
	}
}

func TestHubUnknownField(t *testing.T) {
	if _, err := loadYAML(t, "hub:\n  token: x\n"); err == nil {
		t.Error("hub.token 不應該能寫在設定檔（token 用 PUT /api/settings/hub 設定）")
	}
	if _, err := loadYAML(t, "hub:\n  precheck: { enabled: true, acknowledge: x }\n"); err == nil {
		t.Error("hub.precheck 的未知欄位應該失敗")
	}
}

// @M5 場景在閘門句型實作之前一律 pending，驗證不到設定檔；這裡把 HSHR .feature 的設定檔直接載入一次。
func TestLoadM5FeatureConfigs(t *testing.T) {
	if n := loadFeatureConfigs(t, "HSHR-"); n < 1 {
		t.Errorf("只檢查了 %d 份設定檔，解析可能有誤", n)
	}
}
