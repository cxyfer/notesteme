// Package hubconfig 載入 Hub 的設定：環境變數 > 設定檔 > 內建預設值（ADR-0030 第 1 點，比照 ADR-0013 的分層）。
//
// 設定檔 /config/notesteme-hub.yaml 的欄位見 HACC、HREP、HMCP 規格的「介面」與 ADR-0030 第 2 點。
// OAuth client secret、主金鑰、資料庫密碼與 S3 的 secret 這類祕密只來自環境變數（store.dsn 可以寫在設定檔，但不能含密碼）；群組規則、站台可見度與管理員清單只能寫在設定檔，
// 沒有 API 可以修改（HACC 規格）。未知欄位一律讓載入失敗，錯誤訊息指出欄位。
package hubconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 預設值。
const (
	DefaultConfigPath = "/config/notesteme-hub.yaml"
	DefaultData       = "/data"
	// DefaultListen 預設只綁 loopback（和 Client 的 8080 錯開）；Docker 部署以 NOTESTEME_HUB_LISTEN 開放。
	DefaultListen = "127.0.0.1:8090"
)

// 環境變數名稱。
const (
	EnvConfig   = "NOTESTEME_HUB_CONFIG"
	EnvListen   = "NOTESTEME_HUB_LISTEN"
	EnvData     = "NOTESTEME_HUB_DATA"
	EnvStoreDSN = "NOTESTEME_HUB_STORE_DSN"
	// S3 相容儲存（blobs.driver: s3）：環境變數優先於設定檔的 blobs.s3；secret 只能來自環境變數。
	EnvS3Endpoint        = "NOTESTEME_HUB_S3_ENDPOINT"
	EnvS3Region          = "NOTESTEME_HUB_S3_REGION"
	EnvS3Bucket          = "NOTESTEME_HUB_S3_BUCKET"
	EnvS3AccessKeyID     = "NOTESTEME_HUB_S3_ACCESS_KEY_ID"
	EnvS3SecretAccessKey = "NOTESTEME_HUB_S3_SECRET_ACCESS_KEY"
	EnvMasterKey         = "NOTESTEME_HUB_MASTER_KEY"
	// EnvSessionKey 是 session 金鑰（HACC-R2：加密等待中登入的 PKCE verifier；HACC A10）。
	EnvSessionKey = "NOTESTEME_HUB_SESSION_KEY"
	// envOAuthSecret 是 OAuth client secret 的環境變數（HACC-R1）：NOTESTEME_HUB_OAUTH_<PROVIDER>_CLIENT_SECRET。
	envOAuthSecret = "NOTESTEME_HUB_OAUTH_%s_CLIENT_SECRET"
)

// OAuthSecretEnv 回傳登入方式的 client secret 環境變數名稱。
func OAuthSecretEnv(p Provider) string {
	return fmt.Sprintf(envOAuthSecret, strings.ToUpper(string(p)))
}

// Provider 是登入方式（HACC-R1）。
type Provider string

// 登入方式；Providers 的順序就是 GET /api/v1/auth/providers 的順序。
const (
	Google  Provider = "google"
	GitHub  Provider = "github"
	LinuxDo Provider = "linuxdo"
)

// Providers 是支援的登入方式，依 HACC-R1 的順序。
var Providers = []Provider{Google, GitHub, LinuxDo}

// 權限（ADR-0031 第 2 點）。
const (
	PermRead       = "read"
	PermDownload   = "download"
	PermRegenerate = "regenerate"
	PermUpload     = "upload"
	PermModerate   = "moderate"
	PermAdmin      = "admin"
)

// Permissions 是全部的權限。
var Permissions = []string{PermRead, PermDownload, PermRegenerate, PermUpload, PermModerate, PermAdmin}

// 內建群組（HACC-R10）。
const (
	GroupMembers      = "members"
	GroupContributors = "contributors"
	GroupModerators   = "moderators"
	GroupAdmins       = "admins"
)

// 站台與內容的可見度（HACC-R13、R14）。
const (
	VisibilityPublic        = "public"
	VisibilityAuthenticated = "authenticated"
	VisibilityPrivate       = "private"
	visibilityGroupPrefix   = "group:"
)

// ContentKinds 是可以分享的產出種類（content_defaults 的 key）。
var ContentKinds = []string{"transcript", "chapters", "note", "frames"}

// 儲存的 driver（ADR-0030 第 2 點）。
const (
	StoreSQLite   = "sqlite"
	StorePostgres = "postgres"
	BlobsFS       = "fs"
	BlobsS3       = "s3"
)

// DefaultLicenses 是預設接受的授權（HREP-R1，與 HSHR-R3 相同）。
var DefaultLicenses = []string{"CC-BY-4.0", "CC-BY-SA-4.0", "CC-BY-NC-4.0", "CC-BY-NC-SA-4.0", "CC0-1.0"}

// Sources 是載入設定的輸入；不讀程序的環境。
type Sources struct {
	// File 是設定檔路徑；空字串時用 Env[NOTESTEME_HUB_CONFIG]，再沒有就用預設路徑。檔案不存在時視為沒有設定檔。
	File string
	Env  map[string]string
}

// OAuthClient 是一個登入方式的 OAuth client。
type OAuthClient struct {
	ClientID     string
	ClientSecret string
}

// Enabled 回報這個登入方式是否啟用：client_id 與 secret 都要有（HACC-R1）。
func (c OAuthClient) Enabled() bool { return c.ClientID != "" && c.ClientSecret != "" }

// Rule 是群組的自動規則（HACC-R11、ADR-0012）；依 Provider 只有一個條件有值。
type Rule struct {
	Provider      Provider
	TrustLevelGTE *int     // linuxdo
	OrgMember     []string // github
	EmailDomain   []string // google，只在 email_verified 時成立
}

// Group 是群組的權限與自動規則。
type Group struct {
	Name        string
	Permissions []string
	Rules       []Rule
	Builtin     bool
}

// Identity 是 (provider, subject)，用於 admins 清單（HACC-R12）。
type Identity struct {
	Provider Provider
	Subject  string
}

// RateLimits 是限流的數字（HACC-R15、R18、HREP-R11、R13、R14；數字是暫定值，HACC A6）。
type RateLimits struct {
	RequestsPerMinute          int
	AnonymousRequestsPerMinute int
	TakedownsPerHour           int
	SharesPerHour              int
	UploadBytesPerDay          int64
	VotesPerHour               int
	ReportsPerDay              int
}

// Bundles 是上傳 bundle 的上限（HREP-R2，其他上限預設同 BND-R5）。
type Bundles struct {
	MaxUploadBytes int64
	MaxTotalBytes  int64
	MaxFiles       int
	MaxFileBytes   int64
	MaxRatio       int
}

// Moderation 是自動隱藏的門檻（HREP-R15）。
type Moderation struct {
	AutoHideReports       int
	AutoHideMinAccountAge time.Duration
}

// MCP 是 Hub MCP 的 Origin、Host 檢查與結果大小（HMCP-R2、R7）。
type MCP struct {
	AllowedOrigins []string
	AllowedHosts   []string
	MaxResultBytes int64
}

// Config 是 Hub 生效的設定。
type Config struct {
	ConfigFile string
	Listen     string
	Data       string
	// PublicURL 是 Hub 對外的網址（https，結尾不含 /）：OAuth 的 redirect_uri 與 MCP 的同源判斷都用它。
	PublicURL string

	StoreDriver string
	// StoreDSN 是 postgres 的連線字串：環境變數 NOTESTEME_HUB_STORE_DSN（可含密碼）優先，其次是設定檔的 store.dsn
	// （不能含密碼；密碼用 PGPASSWORD 或 ~/.pgpass）。sqlite 不使用（檔案在 Data 之下）。
	StoreDSN    string
	BlobsDriver string
	// S3 是 blobs.driver: s3 的設定。
	S3 S3Config

	OAuth map[Provider]OAuthClient
	// SiteVisibility 是站台可見度：authenticated（預設）或 public（HACC-R13）。
	SiteVisibility string
	// ContentDefaults 是各種產出上傳時的預設可見度（HACC-R14）；沒有列出的種類跟隨站台。
	ContentDefaults map[string]string
	// Groups 以名稱為 key，內建的四個群組一定存在。
	Groups     map[string]Group
	Admins     []Identity
	RateLimits RateLimits
	Licenses   []string
	Bundles    Bundles
	Moderation Moderation
	MCP        MCP
	// MasterKey 是信封加密的主金鑰（NOTESTEME_HUB_MASTER_KEY，同 SEC-R4）；格式由使用它的切片驗證。
	MasterKey string
	// SessionKey 是 session 金鑰（NOTESTEME_HUB_SESSION_KEY，至少 MinSessionKeyLen 個字元，HACC A10）；
	// 有啟用登入方式時必填，由 hub.Open 與 doctor 檢查。
	SessionKey string
}

// EnabledProviders 依 HACC-R1 的順序回傳啟用的登入方式。
func (c *Config) EnabledProviders() []Provider {
	var out []Provider
	for _, p := range Providers {
		if c.OAuth[p].Enabled() {
			out = append(out, p)
		}
	}
	return out
}

// DefaultVisibility 回傳某種產出上傳時的預設可見度（HACC-R14）：有效可見度不超過站台上限。
func (c *Config) DefaultVisibility(kind string) string {
	if v, ok := c.ContentDefaults[kind]; ok {
		return v
	}
	return c.SiteVisibility
}

// fileSchema 是設定檔允許的欄位；子區段都用 KnownFields 檢查。
type fileSchema struct {
	Listen          string                 `yaml:"listen"`
	Data            string                 `yaml:"data"`
	PublicURL       string                 `yaml:"public_url"`
	Store           storeSchema            `yaml:"store"`
	Blobs           blobsSchema            `yaml:"blobs"`
	OAuth           map[string]oauthSchema `yaml:"oauth"`
	Site            siteSchema             `yaml:"site"`
	ContentDefaults map[string]string      `yaml:"content_defaults"`
	Groups          map[string]groupSchema `yaml:"groups"`
	Admins          []identitySchema       `yaml:"admins"`
	RateLimits      map[string]yaml.Node   `yaml:"rate_limits"`
	Licenses        []string               `yaml:"licenses"`
	Bundles         map[string]yaml.Node   `yaml:"bundles"`
	Moderation      map[string]yaml.Node   `yaml:"moderation"`
	MCP             mcpSchema              `yaml:"mcp"`
}

type storeSchema struct {
	Driver string `yaml:"driver"`
	DSN    string `yaml:"dsn"`
}

type blobsSchema struct {
	Driver string    `yaml:"driver"`
	S3     *s3Schema `yaml:"s3"`
}

type s3Schema struct {
	Endpoint    string `yaml:"endpoint"`
	Region      string `yaml:"region"`
	Bucket      string `yaml:"bucket"`
	Prefix      string `yaml:"prefix"`
	PathStyle   *bool  `yaml:"path_style"`
	AccessKeyID string `yaml:"access_key_id"`
	Timeout     string `yaml:"timeout"`
}

type oauthSchema struct {
	ClientID string `yaml:"client_id"`
}

type siteSchema struct {
	Visibility string `yaml:"visibility"`
}

type groupSchema struct {
	Permissions *[]string    `yaml:"permissions"`
	Rules       []ruleSchema `yaml:"rules"`
}

type ruleSchema struct {
	Provider      string   `yaml:"provider"`
	TrustLevelGTE *int     `yaml:"trust_level_gte"`
	OrgMember     []string `yaml:"org_member"`
	EmailDomain   []string `yaml:"email_domain"`
}

type identitySchema struct {
	Provider string `yaml:"provider"`
	Subject  string `yaml:"subject"`
}

type mcpSchema struct {
	AllowedOrigins []string  `yaml:"allowed_origins"`
	AllowedHosts   []string  `yaml:"allowed_hosts"`
	MaxResultBytes yaml.Node `yaml:"max_result_bytes"`
}

// defaults 回傳全部使用預設值的設定（還沒有 public_url）。
func defaults() *Config {
	return &Config{
		Listen:          DefaultListen,
		Data:            DefaultData,
		StoreDriver:     StoreSQLite,
		BlobsDriver:     BlobsFS,
		OAuth:           map[Provider]OAuthClient{},
		SiteVisibility:  VisibilityAuthenticated,
		ContentDefaults: map[string]string{"frames": VisibilityPrivate},
		Groups: map[string]Group{
			GroupMembers:      {Name: GroupMembers, Permissions: []string{PermRead, PermDownload}, Builtin: true},
			GroupContributors: {Name: GroupContributors, Permissions: []string{PermRead, PermDownload, PermRegenerate, PermUpload}, Builtin: true},
			GroupModerators:   {Name: GroupModerators, Permissions: []string{PermRead, PermDownload, PermModerate}, Builtin: true},
			GroupAdmins:       {Name: GroupAdmins, Permissions: []string{PermAdmin}, Builtin: true},
		},
		RateLimits: RateLimits{
			RequestsPerMinute: 600, AnonymousRequestsPerMinute: 60, TakedownsPerHour: 5,
			SharesPerHour: 30, UploadBytesPerDay: 512 << 20, VotesPerHour: 60, ReportsPerDay: 10,
		},
		Licenses:   slices.Clone(DefaultLicenses),
		Bundles:    Bundles{MaxUploadBytes: 32 << 20, MaxTotalBytes: 256 << 20, MaxFiles: 2000, MaxFileBytes: 64 << 20, MaxRatio: 100},
		Moderation: Moderation{AutoHideReports: 3, AutoHideMinAccountAge: 7 * 24 * time.Hour},
		MCP:        MCP{MaxResultBytes: 256 << 10},
	}
}

// Load 依 src 載入設定。
func Load(src Sources) (*Config, error) {
	cfg := defaults()
	path := src.File
	if path == "" {
		path = src.Env[EnvConfig]
	}
	if path == "" {
		path = DefaultConfigPath
	}
	cfg.ConfigFile = path
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := applyFile(cfg, raw, path); err != nil {
			return nil, err
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		return nil, fmt.Errorf("讀取設定檔 %s 失敗：%w", path, err)
	}
	if v := src.Env[EnvListen]; v != "" {
		cfg.Listen = v
	}
	if v := src.Env[EnvData]; v != "" {
		cfg.Data = v
	}
	abs, err := filepath.Abs(cfg.Data)
	if err != nil {
		return nil, fmt.Errorf("無法取得資料目錄 %q 的絕對路徑：%w", cfg.Data, err)
	}
	cfg.Data = abs
	if v := src.Env[EnvStoreDSN]; v != "" {
		cfg.StoreDSN = v
	}
	applyS3Env(&cfg.S3, src.Env)
	cfg.MasterKey = src.Env[EnvMasterKey]
	if cfg.MasterKey != "" && len(cfg.MasterKey) < MinMasterKeyLen {
		return nil, ErrMasterKey
	}
	cfg.SessionKey = src.Env[EnvSessionKey]
	if cfg.SessionKey != "" && len(cfg.SessionKey) < MinSessionKeyLen {
		return nil, ErrSessionKey
	}
	for _, p := range Providers {
		c := cfg.OAuth[p]
		c.ClientSecret = src.Env[OAuthSecretEnv(p)]
		if c.ClientID != "" || c.ClientSecret != "" {
			cfg.OAuth[p] = c
		}
	}
	if cfg.PublicURL == "" {
		return nil, fmt.Errorf("設定檔 %s 沒有 public_url：%w", path, ErrPublicURL)
	}
	if cfg.StoreDriver == StorePostgres && cfg.StoreDSN == "" {
		return nil, fmt.Errorf("store.driver 是 postgres 時要設定環境變數 %s 或設定檔的 store.dsn。", EnvStoreDSN)
	}
	if cfg.BlobsDriver == BlobsS3 {
		if err := cfg.S3.validate(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// MinSessionKeyLen 是 session 金鑰的最短長度（字元）；金鑰應是隨機產生的值，例如 openssl rand -base64 32。
const MinSessionKeyLen = 32

// ErrSessionKey 是 session 金鑰太短時的錯誤（不帶金鑰內容）。
var ErrSessionKey = fmt.Errorf("環境變數 %s 至少要 %d 個字元（例如 openssl rand -base64 32 產生的值）。", EnvSessionKey, MinSessionKeyLen)

// ErrNoSessionKey 是有啟用登入方式、卻沒有 session 金鑰時的錯誤。
var ErrNoSessionKey = fmt.Errorf("有啟用登入方式時要設定環境變數 %s（至少 %d 個字元，例如 openssl rand -base64 32 產生的值）。", EnvSessionKey, MinSessionKeyLen)

// MinMasterKeyLen 是主金鑰的最短長度（字元，同 session 金鑰）。
const MinMasterKeyLen = 32

// ErrMasterKey 是主金鑰太短時的錯誤（不帶金鑰內容）。
var ErrMasterKey = fmt.Errorf("環境變數 %s 至少要 %d 個字元（例如 openssl rand -base64 32 產生的值）。", EnvMasterKey, MinMasterKeyLen)

// ErrNoMasterKey 是沒有主金鑰時的錯誤：下架申請的聯絡 email 要以它加密（HACC-R15），serve 一律需要。
var ErrNoMasterKey = fmt.Errorf("要設定環境變數 %s（至少 %d 個字元，例如 openssl rand -base64 32 產生的值），下架申請的聯絡 email 以它加密。", EnvMasterKey, MinMasterKeyLen)

// ErrPublicURL 是 public_url 缺少或不是 https 網址時的錯誤。
var ErrPublicURL = errors.New("設定 public_url 必須是 https 網址。")

var (
	groupName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	sizeValue = regexp.MustCompile(`^([0-9]+)\s*(B|KiB|MiB|GiB)?$`)
)

func applyFile(cfg *Config, raw []byte, path string) error {
	var f fileSchema
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("設定檔 %s 格式不正確：%w", path, err)
	}
	bad := func(field, format string, a ...any) error {
		return fmt.Errorf("設定檔 %s 的欄位 %s %s", path, field, fmt.Sprintf(format, a...))
	}
	if f.Listen != "" {
		cfg.Listen = f.Listen
	}
	if f.Data != "" {
		cfg.Data = f.Data
	}
	if f.PublicURL != "" {
		u, err := parsePublicURL(f.PublicURL)
		if err != nil {
			return fmt.Errorf("設定檔 %s 的欄位 public_url 不正確：%w", path, err)
		}
		cfg.PublicURL = u
	}
	if d := f.Store.Driver; d != "" {
		if d != StoreSQLite && d != StorePostgres {
			return bad("store.driver", "必須是 sqlite 或 postgres：%q", d)
		}
		cfg.StoreDriver = d
	}
	if d := strings.TrimSpace(f.Store.DSN); d != "" {
		// 設定檔不放密碼（錯誤訊息也不回顯 DSN）。
		if dsnHasPassword(d) {
			return bad("store.dsn", "不能含密碼；請改用環境變數 %s（完整的 DSN）或 PGPASSWORD。", EnvStoreDSN)
		}
		cfg.StoreDSN = d
	}
	if d := f.Blobs.Driver; d != "" {
		if d != BlobsFS && d != BlobsS3 {
			return bad("blobs.driver", "必須是 fs 或 s3：%q", d)
		}
		cfg.BlobsDriver = d
	}
	if f.Blobs.S3 != nil {
		if err := applyS3File(&cfg.S3, f.Blobs.S3); err != nil {
			return bad("blobs.s3."+err.field, "%s", err.msg)
		}
	}
	for name, o := range f.OAuth {
		p := Provider(name)
		if !slices.Contains(Providers, p) {
			return bad("oauth."+name, "不是支援的登入方式（google、github、linuxdo）。")
		}
		// client_id 留空代表停用這個登入方式（HACC-R1）。
		cfg.OAuth[p] = OAuthClient{ClientID: strings.TrimSpace(o.ClientID)}
	}
	if v := f.Site.Visibility; v != "" {
		if v != VisibilityAuthenticated && v != VisibilityPublic {
			return bad("site.visibility", "必須是 authenticated 或 public：%q", v)
		}
		cfg.SiteVisibility = v
	}
	if err := applyGroups(cfg, f.Groups, bad); err != nil {
		return err
	}
	for kind, v := range f.ContentDefaults {
		if !slices.Contains(ContentKinds, kind) {
			return bad("content_defaults."+kind, "不是可以分享的產出種類（%s）。", strings.Join(ContentKinds, "、"))
		}
		if !validVisibility(cfg, v) {
			return bad("content_defaults."+kind, "必須是 public、authenticated、private 或 group:<群組名稱>：%q", v)
		}
		cfg.ContentDefaults[kind] = v
	}
	for i, a := range f.Admins {
		p := Provider(a.Provider)
		if !slices.Contains(Providers, p) || strings.TrimSpace(a.Subject) == "" {
			return bad(fmt.Sprintf("admins[%d]", i), "必須有 provider（google、github、linuxdo）與 subject。")
		}
		cfg.Admins = append(cfg.Admins, Identity{Provider: p, Subject: a.Subject})
	}
	if err := applyRateLimits(&cfg.RateLimits, f.RateLimits, bad); err != nil {
		return err
	}
	if f.Licenses != nil {
		if len(f.Licenses) == 0 {
			return bad("licenses", "至少要有一個授權。")
		}
		cfg.Licenses = slices.Clone(f.Licenses)
	}
	if err := applyBundles(&cfg.Bundles, f.Bundles, bad); err != nil {
		return err
	}
	if err := applyModeration(&cfg.Moderation, f.Moderation, bad); err != nil {
		return err
	}
	if f.MCP.AllowedOrigins != nil {
		cfg.MCP.AllowedOrigins = nil
		for i, v := range f.MCP.AllowedOrigins {
			o, ok := NormalizeOrigin(v)
			if !ok {
				return bad(fmt.Sprintf("mcp.allowed_origins[%d]", i), "必須是 http:// 或 https:// 開頭、不含路徑的來源（例如 \"https://agent.example.com\"）：%q", v)
			}
			cfg.MCP.AllowedOrigins = append(cfg.MCP.AllowedOrigins, o)
		}
	}
	if f.MCP.AllowedHosts != nil {
		cfg.MCP.AllowedHosts = nil
		for i, v := range f.MCP.AllowedHosts {
			h := strings.ToLower(strings.TrimSpace(v))
			if h == "" || strings.ContainsAny(h, "/:@ *[]") {
				return bad(fmt.Sprintf("mcp.allowed_hosts[%d]", i), "必須是主機名稱（不含 port 與萬用字元，例如 \"hub.example.org\"）：%q", v)
			}
			cfg.MCP.AllowedHosts = append(cfg.MCP.AllowedHosts, h)
		}
	}
	if f.MCP.MaxResultBytes.Kind != 0 {
		n, ok := sizeNode(&f.MCP.MaxResultBytes)
		if !ok {
			return bad("mcp.max_result_bytes", "必須是大於 0 的大小（例如 256KiB）。")
		}
		cfg.MCP.MaxResultBytes = n
	}
	return nil
}

// NormalizeOrigin 把 Origin 正規化成小寫的 scheme://host[:port]（去掉預設 port）；不是合法的 http(s) 來源時 ok 為 false
// （同 Client 的 config.NormalizeOrigin；Hub 不引用 Client 的設定套件）。
func NormalizeOrigin(v string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
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

func parsePublicURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", ErrPublicURL
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func validVisibility(cfg *Config, v string) bool {
	switch v {
	case VisibilityPublic, VisibilityAuthenticated, VisibilityPrivate:
		return true
	}
	name, ok := strings.CutPrefix(v, visibilityGroupPrefix)
	_, exists := cfg.Groups[name]
	return ok && exists
}

func applyGroups(cfg *Config, groups map[string]groupSchema, bad func(string, string, ...any) error) error {
	for name, g := range groups {
		field := "groups." + name
		if !groupName.MatchString(name) {
			return bad(field, "的名稱只能用小寫英數、-、_（最多 40 個字元）。")
		}
		cur, builtin := cfg.Groups[name]
		if !builtin {
			cur = Group{Name: name}
			if g.Permissions == nil {
				return bad(field, "是自訂群組，要列出 permissions。")
			}
		}
		if g.Permissions != nil {
			for _, p := range *g.Permissions {
				if !slices.Contains(Permissions, p) {
					return bad(field+".permissions", "有不認得的權限 %q（%s）。", p, strings.Join(Permissions, "、"))
				}
			}
			// admins 一律是 [admin]：管理員只能由設定檔的 admins 或 CLI 指定，權限不能縮小或改變（ADR-0031 第 2 點）。
			if name == GroupAdmins && !slices.Equal(*g.Permissions, []string{PermAdmin}) {
				return bad(field+".permissions", "固定是 [admin]，不能修改。")
			}
			// 其他群組不能帶 admin：否則可以經由 API 指派或自動規則取得管理員（ADR-0031 第 2 點，安全修補 A）。
			if name != GroupAdmins && slices.Contains(*g.Permissions, PermAdmin) {
				return bad(field+".permissions", "不能包含 admin：管理員只能放在 admins 群組（設定檔或 notesteme-hub admin grant）。")
			}
			cur.Permissions = slices.Clone(*g.Permissions)
		}
		if len(g.Rules) > 0 && (name == GroupAdmins || name == GroupModerators || name == GroupMembers) {
			return bad(field+".rules", "不能設定：這個群組只能手動指派（或包含每個帳號）。")
		}
		cur.Rules = nil
		for i, r := range g.Rules {
			rule, err := parseRule(r)
			if err != nil {
				return bad(fmt.Sprintf("%s.rules[%d]", field, i), "%s", err.Error())
			}
			cur.Rules = append(cur.Rules, rule)
		}
		cfg.Groups[name] = cur
	}
	return nil
}

// parseRule 依 HACC-R11：每個 provider 只有一種條件。
func parseRule(r ruleSchema) (Rule, error) {
	p := Provider(r.Provider)
	rule := Rule{Provider: p}
	set := 0
	if r.TrustLevelGTE != nil {
		set++
	}
	if r.OrgMember != nil {
		set++
	}
	if r.EmailDomain != nil {
		set++
	}
	if set != 1 {
		return rule, errors.New("要剛好有一個條件（trust_level_gte、org_member 或 email_domain）。")
	}
	switch {
	case p == LinuxDo && r.TrustLevelGTE != nil:
		if *r.TrustLevelGTE < 0 || *r.TrustLevelGTE > 4 {
			return rule, errors.New("的 trust_level_gte 必須是 0 到 4。")
		}
		v := *r.TrustLevelGTE
		rule.TrustLevelGTE = &v
	case p == GitHub && len(r.OrgMember) > 0:
		rule.OrgMember = slices.Clone(r.OrgMember)
	case p == Google && len(r.EmailDomain) > 0:
		for _, d := range r.EmailDomain {
			rule.EmailDomain = append(rule.EmailDomain, strings.ToLower(strings.TrimSpace(d)))
		}
	default:
		return rule, errors.New("的條件和 provider 不符：linuxdo 用 trust_level_gte、github 用 org_member、google 用 email_domain。")
	}
	return rule, nil
}

// positiveInt 讀取大於 0 的整數。
func positiveInt(n *yaml.Node) (int, bool) {
	if n.Tag != "!!int" {
		return 0, false
	}
	v, err := strconv.Atoi(n.Value)
	return v, err == nil && v > 0
}

// sizeNode 讀取大於 0 的大小：整數（bytes）或 "64KiB" 這類字串。
func sizeNode(n *yaml.Node) (int64, bool) {
	if n.Kind != yaml.ScalarNode {
		return 0, false
	}
	m := sizeValue.FindStringSubmatch(strings.TrimSpace(n.Value))
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	shift := map[string]uint{"": 0, "B": 0, "KiB": 10, "MiB": 20, "GiB": 30}[m[2]]
	if v > (1<<62)>>shift {
		return 0, false
	}
	v <<= shift
	return v, v > 0
}

func applyRateLimits(rl *RateLimits, m map[string]yaml.Node, bad func(string, string, ...any) error) error {
	ints := map[string]*int{
		"requests_per_minute": &rl.RequestsPerMinute, "anonymous_requests_per_minute": &rl.AnonymousRequestsPerMinute,
		"takedowns_per_hour": &rl.TakedownsPerHour, "shares_per_hour": &rl.SharesPerHour,
		"votes_per_hour": &rl.VotesPerHour, "reports_per_day": &rl.ReportsPerDay,
	}
	for k, n := range m {
		field := "rate_limits." + k
		if k == "upload_bytes_per_day" {
			v, ok := sizeNode(&n)
			if !ok {
				return bad(field, "必須是大於 0 的大小（例如 512MiB）。")
			}
			rl.UploadBytesPerDay = v
			continue
		}
		dst, known := ints[k]
		if !known {
			return bad(field, "不是認得的欄位，請檢查拼字。")
		}
		v, ok := positiveInt(&n)
		if !ok {
			return bad(field, "必須是大於 0 的整數。")
		}
		*dst = v
	}
	return nil
}

func applyBundles(b *Bundles, m map[string]yaml.Node, bad func(string, string, ...any) error) error {
	sizes := map[string]*int64{"max_upload_bytes": &b.MaxUploadBytes, "max_total_bytes": &b.MaxTotalBytes, "max_file_bytes": &b.MaxFileBytes}
	ints := map[string]*int{"max_files": &b.MaxFiles, "max_ratio": &b.MaxRatio}
	for k, n := range m {
		field := "bundles." + k
		// 訊息比照 BND 的「設定 bundles.import.<鍵> 必須是大於 0 的值。」。
		if dst, ok := sizes[k]; ok {
			v, ok := sizeNode(&n)
			if !ok {
				return bad(field, "必須是大於 0 的值。")
			}
			*dst = v
			continue
		}
		dst, ok := ints[k]
		if !ok {
			return bad(field, "不是認得的欄位，請檢查拼字。")
		}
		v, ok := positiveInt(&n)
		if !ok {
			return bad(field, "必須是大於 0 的值。")
		}
		*dst = v
	}
	return nil
}

func applyModeration(mo *Moderation, m map[string]yaml.Node, bad func(string, string, ...any) error) error {
	for k, n := range m {
		field := "moderation." + k
		switch k {
		case "auto_hide_reports":
			v, ok := positiveInt(&n)
			if !ok {
				return bad(field, "必須是大於 0 的整數。")
			}
			mo.AutoHideReports = v
		case "auto_hide_min_account_age":
			d, err := time.ParseDuration(strings.TrimSpace(n.Value))
			if n.Kind != yaml.ScalarNode || err != nil || d < 0 {
				return bad(field, "必須是時間長度（例如 168h）。")
			}
			mo.AutoHideMinAccountAge = d
		default:
			return bad(field, "不是認得的欄位，請檢查拼字。")
		}
	}
	return nil
}
