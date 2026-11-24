package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func loadYAMLEnv(t *testing.T, content string, env map[string]string) (*Config, error) {
	t.Helper()
	return Load(Sources{File: writeFile(t, content), Env: env})
}

// 沒有 M4 區段時是各規格的預設值；備份目錄跟著資料目錄。
func TestM4Defaults(t *testing.T) {
	cfg, err := Load(Sources{File: writeFile(t, "listen: 127.0.0.1:9\n"), Env: map[string]string{EnvData: "/srv/nst"}})
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.M4
	if m.PublicURL != "" || m.JitterMax != 30*time.Minute || m.Deployment != DeploymentLocal {
		t.Errorf("M4 = %+v", m)
	}
	if m.Subscriptions != (Subscriptions{BackfillMax: 500, BackfillPerCheck: 50, RateLimits: RateLimits{30, 6, 10}}) {
		t.Errorf("subscriptions = %+v", m.Subscriptions)
	}
	if m.Notify.DedupeWindow != 24*time.Hour || m.Notify.RateCount != 20 || m.Notify.RatePer != 10*time.Minute || m.Notify.AllowHosts != nil {
		t.Errorf("notify = %+v", m.Notify)
	}
	if m.BundleImport != (BundleImport{64 << 20, 256 << 20, 2000, 64 << 20, 100}) {
		t.Errorf("bundles = %+v", m.BundleImport)
	}
	if m.DailyBackup != (DailyBackup{Enabled: true, At: "03:00", Timezone: "UTC", Dir: "/srv/nst/backups", Keep: 7}) {
		t.Errorf("backup = %+v", m.DailyBackup)
	}
	if m.Alerts != (Alerts{true, 6, 24, 90, 14, 5, 0.5}) {
		t.Errorf("alerts = %+v", m.Alerts)
	}
	if m.YtDlpUpdate != (YtDlpUpdate{Enabled: true, At: "04:00", Timezone: "UTC"}) {
		t.Errorf("updates.yt_dlp = %+v", m.YtDlpUpdate)
	}
	// 沒有設定檔時也一樣。
	cfg, err = Load(Sources{File: "/nonexistent/notesteme.yaml", Env: map[string]string{}})
	if err != nil || cfg.M4.JitterMax != DefaultJitterMax || cfg.M4.DailyBackup.Dir == "" {
		t.Errorf("沒有設定檔：%+v，%v", cfg, err)
	}
}

// updates.yt_dlp：寫了 enabled 時 EnabledSet 為 true（harness 的預設關閉只在沒有寫時生效）。
func TestYtDlpUpdateConfig(t *testing.T) {
	cfg, err := loadYAML(t, "updates: { yt_dlp: { enabled: false, at: \"05:30\", timezone: Asia/Taipei } }\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.M4.YtDlpUpdate != (YtDlpUpdate{Enabled: false, EnabledSet: true, At: "05:30", Timezone: "Asia/Taipei"}) {
		t.Errorf("updates.yt_dlp = %+v", cfg.M4.YtDlpUpdate)
	}
	cfg, err = loadYAML(t, "updates: { yt_dlp: { at: \"05:30\" } }\n")
	if err != nil || !cfg.M4.YtDlpUpdate.Enabled || cfg.M4.YtDlpUpdate.EnabledSet {
		t.Errorf("沒有寫 enabled：%+v，%v", cfg.M4.YtDlpUpdate, err)
	}
}

func TestM4Values(t *testing.T) {
	cfg, err := loadYAML(t, `
trusted_proxies: [127.0.0.0/8]
server: { public_url: "https://notes.example.com/" }
scheduler: { jitter_max: 0s }
subscriptions:
  backfill_max: 2
  backfill_per_check: 1
  rate_limits: { youtube: 1/m, bilibili: 600/m, feed_host: 5/m }
notify:
  allow_hosts: [TG-Proxy.example.com]
  dedupe_window: 30d
  rate_limit: { count: 2, per: 10m }
bundles:
  import: { max_upload_bytes: 1MiB, max_total_bytes: 2GiB, max_files: 5, max_file_bytes: 256KiB, max_ratio: 10 }
backup:
  daily: { enabled: false, at: "07:30", timezone: Asia/Taipei, dir: /backups/, keep: 365 }
alerts: { enabled: true, disk_usage_percent: 0, ytdlp_failure_ratio: 1, stuck_task_hours: 0 }
auth:
  proxy: { user_header: Remote-User, allowed_users: [alice] }
`)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.M4
	if m.PublicURL != "https://notes.example.com" || m.JitterMax != 0 {
		t.Errorf("server/scheduler = %q %v", m.PublicURL, m.JitterMax)
	}
	if m.Subscriptions != (Subscriptions{2, 1, RateLimits{1, 600, 5}}) {
		t.Errorf("subscriptions = %+v", m.Subscriptions)
	}
	if !slices.Equal(m.Notify.AllowHosts, []string{"tg-proxy.example.com"}) || m.Notify.DedupeWindow != 30*24*time.Hour || m.Notify.RateCount != 2 {
		t.Errorf("notify = %+v", m.Notify)
	}
	if m.BundleImport != (BundleImport{1 << 20, 2 << 30, 5, 256 << 10, 10}) {
		t.Errorf("bundles = %+v", m.BundleImport)
	}
	if m.DailyBackup != (DailyBackup{false, "07:30", "Asia/Taipei", "/backups", 365}) {
		t.Errorf("backup = %+v", m.DailyBackup)
	}
	if m.Alerts.DiskUsagePercent != 0 || m.Alerts.StuckTaskHours != 0 || m.Alerts.YtdlpFailureRatio != 1 || m.Alerts.JobStaleHours != 24 {
		t.Errorf("alerts = %+v", m.Alerts)
	}
	if m.Proxy.UserHeader != "Remote-User" || !slices.Equal(m.Proxy.AllowedUsers, []string{"alice"}) {
		t.Errorf("proxy = %+v", m.Proxy)
	}
}

func TestM4Rejects(t *testing.T) {
	cases := []struct{ name, yaml, want string }{
		{"public_url 不是網址", `server: { public_url: "notes.example.com" }`, "server.public_url"},
		{"public_url 有 query", `server: { public_url: "https://n.example.com/?a=1" }`, "server.public_url"},
		{"public_url 有 fragment", `server: { public_url: "https://n.example.com/#x" }`, "server.public_url"},
		{"public_url 有帳號", `server: { public_url: "https://u:p@n.example.com" }`, "server.public_url"},
		{"server 打錯字", `server: { public_uri: "https://n.example.com" }`, "public_uri"},
		{"jitter_max 是負的", `scheduler: { jitter_max: -1m }`, "scheduler.jitter_max"},
		{"jitter_max 不是期間", `scheduler: { jitter_max: soon }`, "scheduler.jitter_max"},
		{"backfill_max 是 0", `subscriptions: { backfill_max: 0 }`, "subscriptions.backfill_max"},
		{"backfill_per_check 不是整數", `subscriptions: { backfill_per_check: 1.5 }`, "subscriptions.backfill_per_check"},
		{"rate limit 的格式", `subscriptions: { rate_limits: { youtube: 30 } }`, "subscriptions.rate_limits.youtube"},
		{"rate limit 太大", `subscriptions: { rate_limits: { bilibili: 601/m } }`, "subscriptions.rate_limits.bilibili"},
		{"rate limit 是 0", `subscriptions: { rate_limits: { feed_host: 0/m } }`, "subscriptions.rate_limits.feed_host"},
		{"rate limit 的平台", `subscriptions: { rate_limits: { vimeo: 1/m } }`, "vimeo"},
		{"allow_hosts 有 scheme", `notify: { allow_hosts: ["https://x.example.com"] }`, "notify.allow_hosts[0]"},
		{"allow_hosts 有萬用字元", `notify: { allow_hosts: ["*.example.com"] }`, "notify.allow_hosts[0]"},
		{"dedupe_window 太短", `notify: { dedupe_window: 30s }`, "notify.dedupe_window"},
		{"dedupe_window 太長", `notify: { dedupe_window: 31d }`, "notify.dedupe_window"},
		{"rate_limit.count 是 0", `notify: { rate_limit: { count: 0 } }`, "notify.rate_limit.count"},
		{"bundle 上限是 0", `bundles: { import: { max_upload_bytes: 0 } }`, "設定 bundles.import.max_upload_bytes 必須是大於 0 的值。"},
		{"bundle 上限的單位", `bundles: { import: { max_total_bytes: 1TB } }`, "設定 bundles.import.max_total_bytes 必須是大於 0 的值。"},
		{"bundle 項目數是負的", `bundles: { import: { max_files: -1 } }`, "設定 bundles.import.max_files 必須是大於 0 的值。"},
		{"bundle 壓縮比是 0", `bundles: { import: { max_ratio: 0 } }`, "設定 bundles.import.max_ratio 必須是大於 0 的值。"},
		{"bundle 單檔上限", `bundles: { import: { max_file_bytes: x } }`, "設定 bundles.import.max_file_bytes 必須是大於 0 的值。"},
		{"備份時間", `backup: { daily: { at: "3:00" } }`, "backup.daily.at"},
		{"備份時間超過 23 點", `backup: { daily: { at: "24:00" } }`, "backup.daily.at"},
		{"備份時區", `backup: { daily: { timezone: Mars/Base } }`, "backup.daily.timezone"},
		{"備份時區不能是 Local", `backup: { daily: { timezone: Local } }`, "backup.daily.timezone"},
		{"備份目錄是相對路徑", `backup: { daily: { dir: backups } }`, "backup.daily.dir"},
		{"保留份數是 0", `backup: { daily: { keep: 0 } }`, "backup.daily.keep"},
		{"保留份數太多", `backup: { daily: { keep: 366 } }`, "backup.daily.keep"},
		{"enabled 不是 bool", `backup: { daily: { enabled: "yes" } }`, "backup.daily.enabled"},
		{"更新時間", `updates: { yt_dlp: { at: "4:00" } }`, "updates.yt_dlp.at"},
		{"更新時區", `updates: { yt_dlp: { timezone: Local } }`, "updates.yt_dlp.timezone"},
		{"更新的 enabled 不是 bool", `updates: { yt_dlp: { enabled: 1 } }`, "updates.yt_dlp.enabled"},
		{"updates 打錯字", `updates: { ytdlp: { enabled: true } }`, "ytdlp"},
		{"磁碟門檻超過 100", `alerts: { disk_usage_percent: 101 }`, "alerts.disk_usage_percent"},
		{"失敗率超過 1", `alerts: { ytdlp_failure_ratio: 1.5 }`, "alerts.ytdlp_failure_ratio"},
		{"告警門檻是負的", `alerts: { cookie_stale_days: -1 }`, "alerts.cookie_stale_days"},
		{"告警打錯字", `alerts: { disk_percent: 90 }`, "disk_percent"},
		// AUTH-R12
		{"proxy 沒有 trusted_proxies", `auth: { proxy: { user_header: Remote-User, allowed_users: [a] } }`,
			"設定了 auth.proxy.user_header，但 trusted_proxies 或 auth.proxy.allowed_users 是空的。"},
		{"proxy 沒有 allowed_users", "trusted_proxies: [127.0.0.1/32]\nauth: { proxy: { user_header: Remote-User } }",
			"設定了 auth.proxy.user_header，但 trusted_proxies 或 auth.proxy.allowed_users 是空的。"},
		{"proxy 的標頭是 Authorization", "trusted_proxies: [127.0.0.1/32]\nauth: { proxy: { user_header: authorization, allowed_users: [a] } }",
			"auth.proxy.user_header 不能使用 authorization。"},
		{"proxy 的標頭是 X-CSRF-Token", "trusted_proxies: [127.0.0.1/32]\nauth: { proxy: { user_header: X-CSRF-Token, allowed_users: [a] } }",
			"auth.proxy.user_header 不能使用 X-CSRF-Token。"},
		// AUTH-R16
		{"OIDC 寫了 client_secret", `auth: { oidc: { issuer: "https://id.example.com", client_secret: x } }`,
			"auth.oidc 的 client secret 只能用環境變數 NOTESTEME_OIDC_CLIENT_SECRET 設定。"},
		{"OIDC 缺 client_id", `auth: { oidc: { issuer: "https://id.example.com", redirect_url: "https://n.example.com/cb", allowed_subjects: [s] } }`,
			"auth.oidc 缺少 client_id。"},
		{"OIDC 缺 redirect_url", `auth: { oidc: { issuer: "https://id.example.com", client_id: c, allowed_subjects: [s] } }`,
			"auth.oidc 缺少 redirect_url。"},
		{"OIDC 缺 allowed_subjects", `auth: { oidc: { issuer: "https://id.example.com", client_id: c, redirect_url: "https://n.example.com/cb" } }`,
			"auth.oidc 缺少 allowed_subjects。"},
		{"OIDC 缺 issuer", `auth: { oidc: { client_id: c } }`, "auth.oidc 缺少 issuer。"},
		{"OIDC 的 issuer 不是 https", `auth: { oidc: { issuer: "http://id.example.com", client_id: c, redirect_url: "https://n.example.com/cb", allowed_subjects: [s] } }`,
			"auth.oidc.issuer 必須是 https://"},
		{"OIDC 沒有環境變數", `auth: { oidc: { issuer: "https://id.example.com", client_id: c, redirect_url: "https://n.example.com/cb", allowed_subjects: [s] } }`,
			"已設定 auth.oidc，但沒有設定環境變數 NOTESTEME_OIDC_CLIENT_SECRET。"},
		{"auth 打錯字", `auth: { saml: {} }`, "saml"},
		// budget 仍由資料庫管理（COST 的 PUT /api/settings/budget）。
		{"budget 由資料庫管理", `budget: { monthly_usd: 5 }`, "由資料庫管理"},
	}
	for _, c := range cases {
		_, err := loadYAML(t, c.yaml)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：err = %v，預期含 %q", c.name, err, c.want)
		}
	}
}

func TestM4OIDCAndDeploymentEnv(t *testing.T) {
	oidc := `auth: { oidc: { issuer: "https://id.example.com", client_id: notesteme, redirect_url: "https://notes.example.com/api/auth/oidc/callback", allowed_subjects: [user-7f3a] } }`
	cfg, err := loadYAMLEnv(t, oidc, map[string]string{EnvOIDCClientSecret: "s3cret", EnvDeployment: "vps"})
	if err != nil {
		t.Fatal(err)
	}
	if o := cfg.M4.OIDC; o.Issuer != "https://id.example.com" || o.ClientID != "notesteme" || o.ClientSecret != "s3cret" || !slices.Equal(o.AllowedSubjects, []string{"user-7f3a"}) {
		t.Errorf("oidc = %+v", o)
	}
	if cfg.M4.Deployment != DeploymentVPS {
		t.Errorf("deployment = %q", cfg.M4.Deployment)
	}
	// 環境變數只在 auth.oidc 有設定時才有意義：沒有設定 OIDC 時不讀它。
	cfg, err = loadYAMLEnv(t, "listen: 127.0.0.1:9\n", map[string]string{EnvOIDCClientSecret: "s3cret"})
	if err != nil || cfg.M4.OIDC.ClientSecret != "" || cfg.M4.OIDC.Issuer != "" {
		t.Errorf("沒有 OIDC：%+v，%v", cfg.M4.OIDC, err)
	}
	if _, err := loadYAMLEnv(t, "listen: 127.0.0.1:9\n", map[string]string{EnvDeployment: "cloud"}); err == nil ||
		!strings.Contains(err.Error(), "NOTESTEME_DEPLOYMENT 只能是 local 或 vps。") {
		t.Errorf("deployment = cloud：%v", err)
	}
	if cfg, err := loadYAMLEnv(t, "listen: 127.0.0.1:9\n", map[string]string{EnvDeployment: "local"}); err != nil || cfg.M4.Deployment != DeploymentLocal {
		t.Errorf("deployment = local：%v", err)
	}
}

// steps.md 4.17：@M4 場景在閘門句型實作之前一律 pending，驗證不到設定檔。
// 這裡把 14 個含 @M4 的 .feature（LIB-library 自 LIB-R24 起有 @M4 場景）中每個 `the config file contains:` 的內容載入一次（Scenario Outline 逐列代入）。
// AUTH 的 proxy、OIDC 設定由步驟合併進設定檔（steps.md 4.21），由 TestM4Values、TestM4Rejects、TestM4OIDCAndDeploymentEnv 涵蓋。
func TestLoadM4FeatureConfigs(t *testing.T) {
	files := 0
	checked := checkFeatureConfigs(t, func(_, content string) bool {
		if strings.Contains(content, "@M4") {
			files++
			return true
		}
		return false
	})
	if files != 14 || checked < 20 {
		t.Errorf("找到 %d 個 M4 feature、檢查了 %d 份設定檔，解析可能有誤", files, checked)
	}
	t.Logf("檢查了 %d 份 M4 的設定檔", checked)
}
