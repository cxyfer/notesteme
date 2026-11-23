package hubconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, yml string, env map[string]string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "notesteme-hub.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(Sources{File: p, Env: env})
}

const minimal = "public_url: https://hub.example.org/\n"

func TestDefaults(t *testing.T) {
	cfg, err := load(t, minimal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://hub.example.org" || cfg.Listen != DefaultListen || cfg.StoreDriver != StoreSQLite || cfg.BlobsDriver != BlobsFS {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.SiteVisibility != VisibilityAuthenticated || cfg.DefaultVisibility("frames") != VisibilityPrivate || cfg.DefaultVisibility("note") != VisibilityAuthenticated {
		t.Errorf("可見度預設值不對：%q %q", cfg.SiteVisibility, cfg.DefaultVisibility("frames"))
	}
	if got := cfg.Groups[GroupMembers].Permissions; !slices.Equal(got, []string{PermRead, PermDownload}) {
		t.Errorf("members = %v（HACC A4：新帳號只有 read、download）", got)
	}
	if got := cfg.Groups[GroupContributors].Permissions; !slices.Equal(got, []string{PermRead, PermDownload, PermRegenerate, PermUpload}) {
		t.Errorf("contributors = %v", got)
	}
	rl := cfg.RateLimits
	if rl.RequestsPerMinute != 600 || rl.AnonymousRequestsPerMinute != 60 || rl.TakedownsPerHour != 5 || rl.SharesPerHour != 30 ||
		rl.UploadBytesPerDay != 512<<20 || rl.VotesPerHour != 60 || rl.ReportsPerDay != 10 {
		t.Errorf("rate_limits = %+v", rl)
	}
	if cfg.Bundles.MaxUploadBytes != 32<<20 || cfg.MCP.MaxResultBytes != 256<<10 || cfg.Moderation.AutoHideReports != 3 ||
		cfg.Moderation.AutoHideMinAccountAge != 168*time.Hour || len(cfg.Licenses) != 5 {
		t.Errorf("上限預設值不對：%+v %+v %+v", cfg.Bundles, cfg.MCP, cfg.Moderation)
	}
	if len(cfg.EnabledProviders()) != 0 {
		t.Error("沒有 client_id 與 secret 時不應該啟用任何登入方式")
	}
}

// HACC-R1：client_id 來自設定檔、secret 只來自環境變數，兩者都有才啟用；列出順序固定。
func TestOAuthProviders(t *testing.T) {
	env := map[string]string{
		"NOTESTEME_HUB_OAUTH_GOOGLE_CLIENT_SECRET":  "g",
		"NOTESTEME_HUB_OAUTH_GITHUB_CLIENT_SECRET":  "h",
		"NOTESTEME_HUB_OAUTH_LINUXDO_CLIENT_SECRET": "l",
	}
	cfg, err := load(t, minimal+"oauth:\n  linuxdo: { client_id: l }\n  github: { client_id: h }\n  google: { client_id: \"\" }\n", env)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.EnabledProviders(); !slices.Equal(got, []Provider{GitHub, LinuxDo}) {
		t.Errorf("EnabledProviders = %v", got)
	}
	if cfg.OAuth[GitHub].ClientSecret != "h" {
		t.Error("secret 沒有從環境變數讀入")
	}
	if _, err := load(t, minimal+"oauth:\n  github: { client_id: h, client_secret: x }\n", nil); err == nil {
		t.Error("client_secret 不能寫在設定檔")
	}
	if _, err := load(t, minimal+"oauth:\n  gitlab: { client_id: x }\n", nil); err == nil {
		t.Error("不支援的登入方式應該失敗")
	}
}

func TestEnvOverrides(t *testing.T) {
	cfg, err := load(t, minimal+"listen: 0.0.0.0:1\n", map[string]string{EnvListen: "127.0.0.1:2", EnvData: "/srv/hub", EnvMasterKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:2" || cfg.Data != "/srv/hub" || cfg.MasterKey != "k" {
		t.Errorf("cfg = %q %q %q", cfg.Listen, cfg.Data, cfg.MasterKey)
	}
	if _, err := load(t, minimal+"store: { driver: postgres }\n", nil); err == nil || !strings.Contains(err.Error(), EnvStoreDSN) {
		t.Errorf("postgres 沒有 DSN：err = %v", err)
	}
	cfg, err = load(t, minimal+"store: { driver: postgres }\n", map[string]string{EnvStoreDSN: "postgres://x"})
	if err != nil || cfg.StoreDSN != "postgres://x" {
		t.Errorf("DSN：%v %v", cfg, err)
	}
}

func TestGroupsAndRules(t *testing.T) {
	cfg, err := load(t, minimal+`groups:
  contributors:
    rules:
      - { provider: linuxdo, trust_level_gte: 2 }
      - { provider: github, org_member: [notesteme-friends] }
      - { provider: google, email_domain: [Example.com] }
  translators: { permissions: [read, download] }
content_defaults: { note: "group:translators" }
`, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Groups[GroupContributors]
	if len(c.Rules) != 3 || *c.Rules[0].TrustLevelGTE != 2 || c.Rules[2].EmailDomain[0] != "example.com" {
		t.Errorf("rules = %+v", c.Rules)
	}
	if !slices.Contains(c.Permissions, PermUpload) {
		t.Error("只寫 rules 時，內建群組沿用預設權限")
	}
	if g := cfg.Groups["translators"]; g.Builtin || !slices.Equal(g.Permissions, []string{PermRead, PermDownload}) {
		t.Errorf("translators = %+v", g)
	}
	if cfg.DefaultVisibility("note") != "group:translators" {
		t.Error("content_defaults 沒有套用")
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string]string{
		"沒有 public_url":        "listen: x\n",
		"public_url 是 http":    "public_url: http://hub.example.org\n",
		"未知頂層欄位":               minimal + "sessions: {}\n",
		"site.visibility":      minimal + "site: { visibility: private }\n",
		"store.driver":         minimal + "store: { driver: mysql }\n",
		"blobs.driver":         minimal + "blobs: { driver: gcs }\n",
		"不認得的權限":               minimal + "groups:\n  members: { permissions: [write] }\n",
		"admins 權限":            minimal + "groups:\n  admins: { permissions: [read] }\n",
		"moderators 的規則":       minimal + "groups:\n  moderators: { rules: [ { provider: linuxdo, trust_level_gte: 1 } ] }\n",
		"自訂群組沒有權限":             minimal + "groups:\n  x: { rules: [] }\n",
		"群組名稱":                 minimal + "groups:\n  Bad Name: { permissions: [read] }\n",
		"規則條件不符":               minimal + "groups:\n  contributors: { rules: [ { provider: github, trust_level_gte: 1 } ] }\n",
		"規則兩個條件":               minimal + "groups:\n  contributors: { rules: [ { provider: google, email_domain: [a], org_member: [b] } ] }\n",
		"trust level 範圍":       minimal + "groups:\n  contributors: { rules: [ { provider: linuxdo, trust_level_gte: 9 } ] }\n",
		"content_defaults 種類":  minimal + "content_defaults: { video: public }\n",
		"content_defaults 群組":  minimal + "content_defaults: { note: \"group:nobody\" }\n",
		"admins 缺 subject":     minimal + "admins: [ { provider: google } ]\n",
		"rate_limits 拼字":       minimal + "rate_limits: { request_per_minute: 1 }\n",
		"rate_limits 0":        minimal + "rate_limits: { requests_per_minute: 0 }\n",
		"rate_limits 小數":       minimal + "rate_limits: { requests_per_minute: 1.5 }\n",
		"bundles 大小":           minimal + "bundles: { max_upload_bytes: \"1KB\" }\n",
		"moderation 時間":        minimal + "moderation: { auto_hide_min_account_age: \"7 days\" }\n",
		"mcp.max_result_bytes": minimal + "mcp: { max_result_bytes: 0 }\n",
		"licenses 空":           minimal + "licenses: []\n",
	}
	for name, y := range cases {
		if _, err := load(t, y, nil); err == nil {
			t.Errorf("%s：應該載入失敗", name)
		}
	}
}

func TestSizes(t *testing.T) {
	cfg, err := load(t, minimal+"bundles: { max_upload_bytes: \"1KiB\", max_files: 3 }\nmcp: { max_result_bytes: 120 }\nrate_limits: { upload_bytes_per_day: 2MiB }\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bundles.MaxUploadBytes != 1024 || cfg.Bundles.MaxFiles != 3 || cfg.MCP.MaxResultBytes != 120 || cfg.RateLimits.UploadBytesPerDay != 2<<20 {
		t.Errorf("大小 = %+v %+v %+v", cfg.Bundles, cfg.MCP, cfg.RateLimits)
	}
}

// @M5 場景在閘門實作之前一律 pending，驗證不到 Hub 設定；這裡把每份 `the Hub config contains:` 直接載入一次。
// harness 會和預設設定（public_url 等）合併，這裡只補上 public_url。
func TestLoadM5FeatureHubConfigs(t *testing.T) {
	files, err := filepath.Glob("../../../features/H*.feature")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(raw), "\n")
		for i := 0; i+1 < len(lines); i++ {
			if !strings.HasSuffix(strings.TrimSpace(lines[i]), "the Hub config contains:") {
				continue
			}
			indent := strings.Index(lines[i+1], `"""`)
			var body []string
			for j := i + 2; j < len(lines) && strings.TrimSpace(lines[j]) != `"""`; j++ {
				l := lines[j]
				if len(l) >= indent {
					l = l[indent:]
				}
				body = append(body, l)
			}
			doc := strings.Join(body, "\n")
			if !regexp.MustCompile(`(?m)^public_url:`).MatchString(doc) {
				doc = minimal + doc
			}
			if _, err := load(t, doc, nil); err != nil {
				t.Errorf("%s 第 %d 行：Hub 設定應該可以載入：%v\n%s", filepath.Base(f), i+1, err, doc)
			}
			checked++
		}
	}
	if checked < 10 {
		t.Errorf("只檢查了 %d 份 Hub 設定，解析可能有誤", checked)
	}
	t.Logf("檢查了 %d 份 Hub 設定", checked)
}
