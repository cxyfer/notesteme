package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "notesteme.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsWhenFileMissing(t *testing.T) {
	cfg, err := Load(Sources{File: filepath.Join(t.TempDir(), "none.yaml"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen || cfg.Data != DefaultData {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.SourceOf(FieldListen) != SourceDefault || cfg.SourceOf(FieldData) != SourceDefault {
		t.Errorf("origin = %v", cfg.Origin)
	}
}

func TestLoadPrecedence(t *testing.T) {
	p := writeFile(t, "listen: 127.0.0.1:8080\ndata: /srv/data\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{EnvListen: "127.0.0.1:9090"}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9090" || cfg.SourceOf(FieldListen) != SourceEnv {
		t.Errorf("listen = %q (%s)", cfg.Listen, cfg.SourceOf(FieldListen))
	}
	if cfg.Data != "/srv/data" || cfg.SourceOf(FieldData) != SourceConfigFile {
		t.Errorf("data = %q (%s)", cfg.Data, cfg.SourceOf(FieldData))
	}
}

func TestLoadUsesEnvConfigPath(t *testing.T) {
	p := writeFile(t, "listen: 127.0.0.1:7000\n")
	cfg, err := Load(Sources{Env: map[string]string{EnvConfig: p}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7000" || cfg.ConfigFile != p {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadRejectsDatabaseManagedFields(t *testing.T) {
	p := writeFile(t, "profiles:\n  standard: { language: { targets: [en] } }\n")
	_, err := Load(Sources{File: p})
	if err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("err = %v, want it to name profiles", err)
	}
}

// 會執行命令的 ASR provider 只能寫在設定檔的頂層 asr（plan 第 19 節、ADR-0013），不可被當成 DB 管理的欄位。
// 內容取自 SEC 場景 "Command providers from the config file are listed read-only"。
func TestLoadAcceptsTopLevelASRProviders(t *testing.T) {
	p := writeFile(t, `asr:
  providers:
    whisperx:
      type: command
      command: ["whisperx", "{audio}", "--output_format", "json", "--output_dir", "{out}"]
      output: { format: whisperx-json, path: "{out}/{stem}.json" }
`)
	cfg, err := Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("asr providers in the config file must load: %v", err)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("listen = %q", cfg.Listen)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("listen = %q", cfg.Listen)
	}
}

func TestLoadToolPaths(t *testing.T) {
	p := writeFile(t, "tools:\n  ffmpeg: /opt/ffmpeg/bin/ffmpeg\n  yt-dlp: /opt/yt-dlp\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools["ffmpeg"] != "/opt/ffmpeg/bin/ffmpeg" || cfg.Tools["yt-dlp"] != "/opt/yt-dlp" {
		t.Errorf("tools = %v", cfg.Tools)
	}
}

func TestLoadTrustedProxies(t *testing.T) {
	p := writeFile(t, "trusted_proxies:\n  - 172.18.0.0/16\n  - \"fd00::/8\"\n  - 10.1.2.3/8\n")
	cfg, err := Load(Sources{File: p, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range cfg.TrustedProxies {
		got = append(got, p.String())
	}
	want := []string{"172.18.0.0/16", "fd00::/8", "10.0.0.0/8"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("TrustedProxies = %v, want %v", got, want)
	}
}

func TestLoadTrustedProxiesDefaultsToEmpty(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "listen: 127.0.0.1:1\n"), Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}
}

func TestLoadRejectsInvalidTrustedProxies(t *testing.T) {
	for _, body := range []string{
		"trusted_proxies:\n  - 172.18.0.0/16\n  - not-a-cidr\n",
		"trusted_proxies:\n  - 10.0.0.1\n", // 缺少遮罩
	} {
		_, err := Load(Sources{File: writeFile(t, body), Env: map[string]string{}})
		if err == nil || !strings.Contains(err.Error(), "trusted_proxies") {
			t.Errorf("err = %v，應指出 trusted_proxies", err)
		}
	}
}

// NOTESTEME_TRUSTED_PROXIES（SEC-R1：環境變數優先）：compose 的 vps profile 以它信任 caddy（AUTH-R6、OPS-R20）。
func TestTrustedProxiesFromEnv(t *testing.T) {
	file := writeFile(t, "trusted_proxies: [\"10.0.0.0/8\"]\n")
	cfg, err := Load(Sources{File: file, Env: map[string]string{EnvTrustedProxies: " 172.31.250.10/32 , ,2001:db8::1/64"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{netip.MustParsePrefix("172.31.250.10/32"), netip.MustParsePrefix("2001:db8::/64")}
	if !slices.Equal(cfg.TrustedProxies, want) || cfg.SourceOf(FieldTrustedProxies) != SourceEnv {
		t.Errorf("環境變數要整份取代設定檔：%v（來源 %s）", cfg.TrustedProxies, cfg.SourceOf(FieldTrustedProxies))
	}
	// 空字串視為沒有設定，沿用設定檔。
	cfg, err = Load(Sources{File: file, Env: map[string]string{EnvTrustedProxies: "  "}})
	if err != nil || len(cfg.TrustedProxies) != 1 || cfg.SourceOf(FieldTrustedProxies) != SourceConfigFile {
		t.Errorf("空的環境變數：%v %v", cfg.TrustedProxies, err)
	}
	if _, err := Load(Sources{File: file, Env: map[string]string{EnvTrustedProxies: "172.31.250.10/32,caddy"}}); err == nil ||
		!strings.Contains(err.Error(), "NOTESTEME_TRUSTED_PROXIES") || !strings.Contains(err.Error(), `"caddy"`) {
		t.Errorf("不是 CIDR 要拒絕並指出值：%v", err)
	}
}
