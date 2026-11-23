package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	// 時區（backup.daily.timezone）用 time.LoadLocation 驗證：容器映像不一定有 /usr/share/zoneinfo，
	// 內嵌時區資料，合法的設定才不會在容器裡啟動失敗。
	_ "time/tzdata"

	"gopkg.in/yaml.v3"

	"github.com/cxyfer/notesteme/internal/auth"
)

// M4 的設定檔區段（M4-S0）：只能寫在設定檔的鍵，依各規格的「介面」（DIG、NOTIFY、SUB、BND、OPS、AUTH、ADR-0021）。
// S0 只解析與驗證，功能本身由各自的切片實作；設定了也只是被接受，啟動時不報錯。

// M4 的環境變數。
const (
	// EnvDeployment 是部署方式（OPS-R21）：local（預設）或 vps。
	EnvDeployment = "NOTESTEME_DEPLOYMENT"
	// EnvOIDCClientSecret 是 OIDC 的 client secret（AUTH-R16），只能來自環境變數。
	EnvOIDCClientSecret = "NOTESTEME_OIDC_CLIENT_SECRET"
)

// 部署方式（OPS-R21）。
const (
	DeploymentLocal = "local"
	DeploymentVPS   = "vps"
)

// M4 設定的預設值（各規格的介面與待決問題）。
const (
	DefaultJitterMax          = 30 * time.Minute // ADR-0021 第 1 點
	DefaultBackfillMax        = 500              // SUB S5
	DefaultBackfillPerCheck   = 50
	DefaultRateYouTube        = 30 // 每分鐘（SUB-R10）
	DefaultRateBilibili       = 6
	DefaultRateFeedHost       = 10
	MaxRatePerMinute          = 600
	DefaultNotifyDedupeWindow = 24 * time.Hour // NOTIFY-R10
	MinNotifyDedupeWindow     = time.Minute
	MaxNotifyDedupeWindow     = 30 * 24 * time.Hour
	DefaultNotifyRateCount    = 20 // NOTIFY-R11
	DefaultNotifyRatePer      = 10 * time.Minute

	DefaultBundleMaxUpload = 64 << 20 // BND N4
	DefaultBundleMaxTotal  = 256 << 20
	DefaultBundleMaxFiles  = 2000
	DefaultBundleMaxFile   = 64 << 20
	DefaultBundleMaxRatio  = 100

	DefaultBackupAt   = "03:00" // OPS-R11
	DefaultBackupKeep = 7       // OPS-R12
	MaxBackupKeep     = 365

	DefaultAlertStuckTaskHours  = 6 // OPS-R14～R18
	DefaultAlertJobStaleHours   = 24
	DefaultAlertDiskPercent     = 90
	DefaultAlertCookieStaleDays = 14
	DefaultAlertYtdlpMinCalls   = 5
	DefaultAlertYtdlpFailRatio  = 0.5
)

// RateLimits 是 subscriptions.rate_limits（每分鐘的請求數，SUB-R10）。
type RateLimits struct {
	YouTube  int
	Bilibili int
	FeedHost int
}

// Subscriptions 是設定檔 subscriptions 區段（SUB 的介面）。
type Subscriptions struct {
	BackfillMax      int
	BackfillPerCheck int
	RateLimits       RateLimits
}

// Notify 是設定檔 notify 區段（NOTIFY 的介面）。
type Notify struct {
	// AllowHosts 是 Telegram、Discord 允許的非官方主機（小寫，比對規則同 GRD-R3）。
	AllowHosts   []string
	DedupeWindow time.Duration
	RateCount    int
	RatePer      time.Duration
}

// BundleImport 是設定檔 bundles.import 區段（BND-R5）。
type BundleImport struct {
	MaxUploadBytes int64
	MaxTotalBytes  int64
	MaxFiles       int
	MaxFileBytes   int64
	MaxRatio       int
}

// DailyBackup 是設定檔 backup.daily 區段（OPS-R11、R12）。
type DailyBackup struct {
	Enabled  bool
	At       string // HH:MM
	Timezone string // IANA 名稱
	// Dir 是備份目錄（絕對路徑）；沒有設定時是 <資料目錄>/backups（Load 在環境變數覆寫 data 之後補上）。
	Dir  string
	Keep int
}

// Alerts 是設定檔 alerts 區段（OPS-R13～R18）；門檻為 0 代表停用那一項。
type Alerts struct {
	Enabled           bool
	StuckTaskHours    int
	JobStaleHours     int
	DiskUsagePercent  int
	CookieStaleDays   int
	YtdlpMinCalls     int
	YtdlpFailureRatio float64
}

// ProxyAuth 是設定檔 auth.proxy 區段（AUTH-R12）；UserHeader 為空代表沒有啟用。
type ProxyAuth struct {
	UserHeader   string
	AllowedUsers []string
}

// OIDCAuth 是設定檔 auth.oidc 區段（AUTH-R16）；Issuer 為空代表沒有啟用。
type OIDCAuth struct {
	Issuer          string
	ClientID        string
	RedirectURL     string
	AllowedSubjects []string
	// ClientSecret 來自環境變數 NOTESTEME_OIDC_CLIENT_SECRET。
	ClientSecret string
}

// M4 是 M4 的設定（Config.M4）。
type M4 struct {
	// PublicURL 是 server.public_url（DIG、NOTIFY），結尾不含 /；空字串代表沒有設定。
	PublicURL string
	// JitterMax 是 scheduler.jitter_max（ADR-0021 第 1 點），0 代表沒有隨機延遲。
	JitterMax     time.Duration
	Subscriptions Subscriptions
	Notify        Notify
	BundleImport  BundleImport
	DailyBackup   DailyBackup
	Alerts        Alerts
	Proxy         ProxyAuth
	OIDC          OIDCAuth
	// Deployment 是 NOTESTEME_DEPLOYMENT（OPS-R21）：local 或 vps。
	Deployment string
}

// defaultM4 是沒有設定時的 M4 設定。
func defaultM4() M4 {
	return M4{
		JitterMax: DefaultJitterMax,
		Subscriptions: Subscriptions{
			BackfillMax: DefaultBackfillMax, BackfillPerCheck: DefaultBackfillPerCheck,
			RateLimits: RateLimits{YouTube: DefaultRateYouTube, Bilibili: DefaultRateBilibili, FeedHost: DefaultRateFeedHost},
		},
		Notify: Notify{DedupeWindow: DefaultNotifyDedupeWindow, RateCount: DefaultNotifyRateCount, RatePer: DefaultNotifyRatePer},
		BundleImport: BundleImport{
			MaxUploadBytes: DefaultBundleMaxUpload, MaxTotalBytes: DefaultBundleMaxTotal, MaxFiles: DefaultBundleMaxFiles,
			MaxFileBytes: DefaultBundleMaxFile, MaxRatio: DefaultBundleMaxRatio,
		},
		DailyBackup: DailyBackup{Enabled: true, At: DefaultBackupAt, Timezone: "UTC", Keep: DefaultBackupKeep},
		Alerts: Alerts{
			Enabled: true, StuckTaskHours: DefaultAlertStuckTaskHours, JobStaleHours: DefaultAlertJobStaleHours,
			DiskUsagePercent: DefaultAlertDiskPercent, CookieStaleDays: DefaultAlertCookieStaleDays,
			YtdlpMinCalls: DefaultAlertYtdlpMinCalls, YtdlpFailureRatio: DefaultAlertYtdlpFailRatio,
		},
		Deployment: DeploymentLocal,
	}
}

// m4Schema 是 M4 的設定檔區段，以 inline 嵌在 fileSchema；子欄位一律 KnownFields 檢查。
// subscriptions 原本列在 dbManagedFields（訂閱本身仍由 API 管理）；M4 起它是設定檔區段，
// 只放探索的上限與 rate limit（SUB 的介面）。budget 仍由資料庫管理（COST 的 PUT /api/settings/budget）。
type m4Schema struct {
	Server        serverSchema        `yaml:"server"`
	Scheduler     schedulerSchema     `yaml:"scheduler"`
	Subscriptions subscriptionsSchema `yaml:"subscriptions"`
	Notify        notifySchema        `yaml:"notify"`
	Bundles       bundlesSchema       `yaml:"bundles"`
	Backup        backupSchema        `yaml:"backup"`
	Alerts        alertsSchema        `yaml:"alerts"`
	Auth          authSchema          `yaml:"auth"`
}

type serverSchema struct {
	PublicURL string `yaml:"public_url"`
}

type schedulerSchema struct {
	JitterMax yaml.Node `yaml:"jitter_max"`
}

type subscriptionsSchema struct {
	BackfillMax      yaml.Node `yaml:"backfill_max"`
	BackfillPerCheck yaml.Node `yaml:"backfill_per_check"`
	RateLimits       struct {
		YouTube  yaml.Node `yaml:"youtube"`
		Bilibili yaml.Node `yaml:"bilibili"`
		FeedHost yaml.Node `yaml:"feed_host"`
	} `yaml:"rate_limits"`
}

type notifySchema struct {
	AllowHosts   []string  `yaml:"allow_hosts"`
	DedupeWindow yaml.Node `yaml:"dedupe_window"`
	RateLimit    struct {
		Count yaml.Node `yaml:"count"`
		Per   yaml.Node `yaml:"per"`
	} `yaml:"rate_limit"`
}

type bundlesSchema struct {
	Import struct {
		MaxUploadBytes yaml.Node `yaml:"max_upload_bytes"`
		MaxTotalBytes  yaml.Node `yaml:"max_total_bytes"`
		MaxFiles       yaml.Node `yaml:"max_files"`
		MaxFileBytes   yaml.Node `yaml:"max_file_bytes"`
		MaxRatio       yaml.Node `yaml:"max_ratio"`
	} `yaml:"import"`
}

type backupSchema struct {
	Daily struct {
		Enabled  yaml.Node `yaml:"enabled"`
		At       yaml.Node `yaml:"at"`
		Timezone yaml.Node `yaml:"timezone"`
		Dir      yaml.Node `yaml:"dir"`
		Keep     yaml.Node `yaml:"keep"`
	} `yaml:"daily"`
}

type alertsSchema struct {
	Enabled           yaml.Node `yaml:"enabled"`
	StuckTaskHours    yaml.Node `yaml:"stuck_task_hours"`
	JobStaleHours     yaml.Node `yaml:"job_stale_hours"`
	DiskUsagePercent  yaml.Node `yaml:"disk_usage_percent"`
	CookieStaleDays   yaml.Node `yaml:"cookie_stale_days"`
	YtdlpMinCalls     yaml.Node `yaml:"ytdlp_min_calls"`
	YtdlpFailureRatio yaml.Node `yaml:"ytdlp_failure_ratio"`
}

type authSchema struct {
	Proxy struct {
		UserHeader   string   `yaml:"user_header"`
		AllowedUsers []string `yaml:"allowed_users"`
	} `yaml:"proxy"`
	OIDC struct {
		Issuer          string    `yaml:"issuer"`
		ClientID        string    `yaml:"client_id"`
		RedirectURL     string    `yaml:"redirect_url"`
		AllowedSubjects []string  `yaml:"allowed_subjects"`
		ClientSecret    yaml.Node `yaml:"client_secret"`
	} `yaml:"oidc"`
}

var (
	rateLimitValue = regexp.MustCompile(`^([0-9]+)/m$`)
	clockTime      = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	byteSize       = regexp.MustCompile(`^([0-9]+)\s*(B|KiB|MiB|GiB)?$`)
	hostName       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
)

// AUTH-R12：不能當成 user_header 的標頭（不分大小寫）。
var reservedProxyHeaders = []string{
	"Authorization", "Cookie", "Host", "Origin", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-CSRF-Token",
}

// fieldErr 是 M4 設定欄位錯誤的共同格式。
func fieldErr(file, field, format string, args ...any) error {
	return fmt.Errorf("設定檔 %s 的欄位 %s %s", file, field, fmt.Sprintf(format, args...))
}

// nodeInt 讀取整數欄位；沒有設定時回傳 def。
func nodeInt(n yaml.Node, file, field string, def, lo, hi int) (int, error) {
	if n.Kind == 0 {
		return def, nil
	}
	var v int
	if n.Tag != "!!int" || n.Decode(&v) != nil || v < lo || v > hi {
		return 0, fieldErr(file, field, "必須是 %d 到 %d 的整數。", lo, hi)
	}
	return v, nil
}

// nodeBool 讀取 true／false 欄位。
func nodeBool(n yaml.Node, file, field string, def bool) (bool, error) {
	if n.Kind == 0 {
		return def, nil
	}
	var v bool
	if n.Tag != "!!bool" || n.Decode(&v) != nil {
		return false, fieldErr(file, field, "必須是 true 或 false。")
	}
	return v, nil
}

// nodeString 讀取字串欄位（數字等純量也照字面讀取，例如 03:00 被當成 YAML 1.1 的六十進位數時）。
func nodeString(n yaml.Node, file, field string) (string, bool, error) {
	if n.Kind == 0 {
		return "", false, nil
	}
	if n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return "", false, fieldErr(file, field, "必須是字串。")
	}
	return strings.TrimSpace(n.Value), true, nil
}

// nodeDuration 讀取期間（jitter_max 用 Go 的寫法，可以是 0s；其他允許 d，例如 30d）。
func nodeDuration(n yaml.Node, file, field string, def, lo, hi time.Duration, allowDays bool) (time.Duration, error) {
	s, ok, err := nodeString(n, file, field)
	if err != nil || !ok {
		return def, err
	}
	var d time.Duration
	if allowDays {
		d, err = auth.ParseTokenDuration(s)
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d < lo || (hi > 0 && d > hi) {
		if hi > 0 {
			return 0, fieldErr(file, field, "必須是 %s 到 %s 之間的期間：%q", lo, hi, s)
		}
		return 0, fieldErr(file, field, "必須是不小於 %s 的期間（例如 30m）：%q", lo, s)
	}
	return d, nil
}

// bundleSize 讀取 bundles.import 的大小（"64MiB" 這類寫法或整數），必須大於 0（BND 的介面）。
func bundleSize(n yaml.Node, file, key string, def int64) (int64, error) {
	if n.Kind == 0 {
		return def, nil
	}
	bad := fmt.Errorf("設定檔 %s：設定 bundles.import.%s 必須是大於 0 的值。", file, key)
	if n.Kind != yaml.ScalarNode {
		return 0, bad
	}
	m := byteSize.FindStringSubmatch(strings.TrimSpace(n.Value))
	if m == nil {
		return 0, bad
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, bad
	}
	shift := map[string]uint{"": 0, "B": 0, "KiB": 10, "MiB": 20, "GiB": 30}[m[2]]
	if v <= 0 || v > (1<<62)>>shift {
		return 0, bad
	}
	return v << shift, nil
}

// bundleCount 讀取 bundles.import 的整數（max_files、max_ratio），必須大於 0。
func bundleCount(n yaml.Node, file, key string, def int) (int, error) {
	if n.Kind == 0 {
		return def, nil
	}
	var v int
	if n.Tag != "!!int" || n.Decode(&v) != nil || v <= 0 {
		return 0, fmt.Errorf("設定檔 %s：設定 bundles.import.%s 必須是大於 0 的值。", file, key)
	}
	return v, nil
}

// rateLimit 讀取 subscriptions.rate_limits 的 <n>/m（n 為 1～600，SUB-R10）。
func rateLimit(n yaml.Node, file, key string, def int) (int, error) {
	s, ok, err := nodeString(n, file, "subscriptions.rate_limits."+key)
	if err != nil || !ok {
		return def, err
	}
	m := rateLimitValue.FindStringSubmatch(s)
	v := 0
	if m != nil {
		v, _ = strconv.Atoi(m[1])
	}
	if v < 1 || v > MaxRatePerMinute {
		return 0, fieldErr(file, "subscriptions.rate_limits."+key, "的格式是 <n>/m（n 為 1 到 %d）：%q", MaxRatePerMinute, s)
	}
	return v, nil
}

// parseM4 解析 M4 的設定檔區段。跨欄位的檢查（AUTH-R12 需要 trusted_proxies、AUTH-R16 需要環境變數）在 finishM4。
func parseM4(s *m4Schema, file string, cfg *Config) error {
	out := defaultM4()
	var err error

	// server.public_url（DIG 的介面）：http 或 https，不含帳號密碼、query 與 fragment。
	if v := strings.TrimSpace(s.Server.PublicURL); v != "" {
		u, perr := url.Parse(v)
		if perr != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(v, "?") {
			return fieldErr(file, "server.public_url", "必須是 http 或 https 的網址（不含帳號密碼、查詢字串與 #）：%q", v)
		}
		out.PublicURL = strings.TrimRight(v, "/")
	}

	if out.JitterMax, err = nodeDuration(s.Scheduler.JitterMax, file, "scheduler.jitter_max", DefaultJitterMax, 0, 0, false); err != nil {
		return err
	}

	sub := &out.Subscriptions
	if sub.BackfillMax, err = nodeInt(s.Subscriptions.BackfillMax, file, "subscriptions.backfill_max", DefaultBackfillMax, 1, 1<<20); err != nil {
		return err
	}
	if sub.BackfillPerCheck, err = nodeInt(s.Subscriptions.BackfillPerCheck, file, "subscriptions.backfill_per_check", DefaultBackfillPerCheck, 1, 1<<20); err != nil {
		return err
	}
	if sub.RateLimits.YouTube, err = rateLimit(s.Subscriptions.RateLimits.YouTube, file, "youtube", DefaultRateYouTube); err != nil {
		return err
	}
	if sub.RateLimits.Bilibili, err = rateLimit(s.Subscriptions.RateLimits.Bilibili, file, "bilibili", DefaultRateBilibili); err != nil {
		return err
	}
	if sub.RateLimits.FeedHost, err = rateLimit(s.Subscriptions.RateLimits.FeedHost, file, "feed_host", DefaultRateFeedHost); err != nil {
		return err
	}

	nt := &out.Notify
	for i, h := range s.Notify.AllowHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if !hostName.MatchString(h) {
			return fieldErr(file, fmt.Sprintf("notify.allow_hosts[%d]", i), "不是有效的主機名稱（不含 scheme、port 與萬用字元）：%q", s.Notify.AllowHosts[i])
		}
		nt.AllowHosts = append(nt.AllowHosts, h)
	}
	if nt.DedupeWindow, err = nodeDuration(s.Notify.DedupeWindow, file, "notify.dedupe_window", DefaultNotifyDedupeWindow, MinNotifyDedupeWindow, MaxNotifyDedupeWindow, true); err != nil {
		return err
	}
	if nt.RateCount, err = nodeInt(s.Notify.RateLimit.Count, file, "notify.rate_limit.count", DefaultNotifyRateCount, 1, 10000); err != nil {
		return err
	}
	if nt.RatePer, err = nodeDuration(s.Notify.RateLimit.Per, file, "notify.rate_limit.per", DefaultNotifyRatePer, time.Second, 24*time.Hour, true); err != nil {
		return err
	}

	bi, imp := &out.BundleImport, s.Bundles.Import
	if bi.MaxUploadBytes, err = bundleSize(imp.MaxUploadBytes, file, "max_upload_bytes", DefaultBundleMaxUpload); err != nil {
		return err
	}
	if bi.MaxTotalBytes, err = bundleSize(imp.MaxTotalBytes, file, "max_total_bytes", DefaultBundleMaxTotal); err != nil {
		return err
	}
	if bi.MaxFileBytes, err = bundleSize(imp.MaxFileBytes, file, "max_file_bytes", DefaultBundleMaxFile); err != nil {
		return err
	}
	if bi.MaxFiles, err = bundleCount(imp.MaxFiles, file, "max_files", DefaultBundleMaxFiles); err != nil {
		return err
	}
	if bi.MaxRatio, err = bundleCount(imp.MaxRatio, file, "max_ratio", DefaultBundleMaxRatio); err != nil {
		return err
	}

	bd, d := &out.DailyBackup, s.Backup.Daily
	if bd.Enabled, err = nodeBool(d.Enabled, file, "backup.daily.enabled", true); err != nil {
		return err
	}
	if v, ok, err := nodeString(d.At, file, "backup.daily.at"); err != nil {
		return err
	} else if ok {
		if !clockTime.MatchString(v) {
			return fieldErr(file, "backup.daily.at", "必須是 HH:MM（例如 \"03:00\"）：%q", v)
		}
		bd.At = v
	}
	if v, ok, err := nodeString(d.Timezone, file, "backup.daily.timezone"); err != nil {
		return err
	} else if ok {
		if _, lerr := time.LoadLocation(v); lerr != nil || v == "" || v == "Local" {
			return fieldErr(file, "backup.daily.timezone", "不是認得的 IANA 時區名稱（例如 Asia/Taipei）：%q", v)
		}
		bd.Timezone = v
	}
	if v, ok, err := nodeString(d.Dir, file, "backup.daily.dir"); err != nil {
		return err
	} else if ok {
		if !filepath.IsAbs(v) {
			return fieldErr(file, "backup.daily.dir", "必須是絕對路徑：%q", v)
		}
		bd.Dir = filepath.Clean(v)
	}
	if bd.Keep, err = nodeInt(d.Keep, file, "backup.daily.keep", DefaultBackupKeep, 1, MaxBackupKeep); err != nil {
		return err
	}

	al, a := &out.Alerts, s.Alerts
	if al.Enabled, err = nodeBool(a.Enabled, file, "alerts.enabled", true); err != nil {
		return err
	}
	if al.StuckTaskHours, err = nodeInt(a.StuckTaskHours, file, "alerts.stuck_task_hours", DefaultAlertStuckTaskHours, 0, 24*365); err != nil {
		return err
	}
	if al.JobStaleHours, err = nodeInt(a.JobStaleHours, file, "alerts.job_stale_hours", DefaultAlertJobStaleHours, 0, 24*365); err != nil {
		return err
	}
	if al.DiskUsagePercent, err = nodeInt(a.DiskUsagePercent, file, "alerts.disk_usage_percent", DefaultAlertDiskPercent, 0, 100); err != nil {
		return err
	}
	if al.CookieStaleDays, err = nodeInt(a.CookieStaleDays, file, "alerts.cookie_stale_days", DefaultAlertCookieStaleDays, 0, 3650); err != nil {
		return err
	}
	if al.YtdlpMinCalls, err = nodeInt(a.YtdlpMinCalls, file, "alerts.ytdlp_min_calls", DefaultAlertYtdlpMinCalls, 0, 100000); err != nil {
		return err
	}
	if n := a.YtdlpFailureRatio; n.Kind != 0 {
		var v float64
		if (n.Tag != "!!float" && n.Tag != "!!int") || n.Decode(&v) != nil || v < 0 || v > 1 {
			return fieldErr(file, "alerts.ytdlp_failure_ratio", "必須是 0 到 1 的數字。")
		}
		al.YtdlpFailureRatio = v
	}

	// auth.proxy（AUTH-R12）：user_header 的值檢查在這裡，trusted_proxies 的檢查在 finishM4。
	px := s.Auth.Proxy
	out.Proxy.UserHeader = strings.TrimSpace(px.UserHeader)
	for i, u := range px.AllowedUsers {
		if u == "" {
			return fieldErr(file, fmt.Sprintf("auth.proxy.allowed_users[%d]", i), "不可以是空字串。")
		}
	}
	out.Proxy.AllowedUsers = px.AllowedUsers
	if h := out.Proxy.UserHeader; h != "" {
		for _, r := range reservedProxyHeaders {
			if strings.EqualFold(h, r) {
				return fmt.Errorf("auth.proxy.user_header 不能使用 %s。", h)
			}
		}
	}

	// auth.oidc（AUTH-R16）：client secret 只能來自環境變數。
	oi := s.Auth.OIDC
	if oi.ClientSecret.Kind != 0 {
		return fmt.Errorf("auth.oidc 的 client secret 只能用環境變數 %s 設定。", EnvOIDCClientSecret)
	}
	out.OIDC = OIDCAuth{
		Issuer: strings.TrimSpace(oi.Issuer), ClientID: strings.TrimSpace(oi.ClientID),
		RedirectURL: strings.TrimSpace(oi.RedirectURL), AllowedSubjects: oi.AllowedSubjects,
	}
	cfg.M4 = out
	return nil
}

// finishM4 是讀完設定檔與環境變數之後的 M4 檢查（AUTH-R12、R16、OPS-R21）與依賴資料目錄的預設值。
func finishM4(cfg *Config, env map[string]string) error {
	m := &cfg.M4
	if m.DailyBackup.Dir == "" {
		m.DailyBackup.Dir = filepath.Join(cfg.Data, "backups")
	}
	switch v := env[EnvDeployment]; v {
	case "":
	case DeploymentLocal, DeploymentVPS:
		m.Deployment = v
	default:
		return fmt.Errorf("%s 只能是 local 或 vps。", EnvDeployment)
	}
	if m.Proxy.UserHeader != "" && (len(cfg.TrustedProxies) == 0 || len(m.Proxy.AllowedUsers) == 0) {
		return fmt.Errorf("設定了 auth.proxy.user_header，但 trusted_proxies 或 auth.proxy.allowed_users 是空的。")
	}
	o := &m.OIDC
	if o.Issuer == "" {
		if o.ClientID != "" || o.RedirectURL != "" || len(o.AllowedSubjects) > 0 {
			return fmt.Errorf("auth.oidc 缺少 issuer。")
		}
		return nil
	}
	if u, err := url.Parse(o.Issuer); err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("auth.oidc.issuer 必須是 https:// 開頭的網址：%q", o.Issuer)
	}
	for _, k := range []struct {
		name string
		ok   bool
	}{{"client_id", o.ClientID != ""}, {"redirect_url", o.RedirectURL != ""}, {"allowed_subjects", len(o.AllowedSubjects) > 0}} {
		if !k.ok {
			return fmt.Errorf("auth.oidc 缺少 %s。", k.name)
		}
	}
	if u, err := url.Parse(o.RedirectURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("auth.oidc.redirect_url 必須是 http 或 https 的網址：%q", o.RedirectURL)
	}
	for i, s := range o.AllowedSubjects {
		if s == "" {
			return fmt.Errorf("auth.oidc.allowed_subjects[%d] 不可以是空字串。", i)
		}
	}
	o.ClientSecret = env[EnvOIDCClientSecret]
	if o.ClientSecret == "" {
		return fmt.Errorf("已設定 auth.oidc，但沒有設定環境變數 %s。", EnvOIDCClientSecret)
	}
	return nil
}
