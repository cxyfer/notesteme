// Package config 載入設定：環境變數 > 設定檔 > 內建預設值（SEC-R1、ADR-0013）。
//
// 設定檔只放基礎設定（SEC-R2）；DB 管理的設定（處理方案、prompt 等）出現在設定檔時載入失敗，
// 並指出是哪個欄位。每個欄位都會記錄生效值的來源，供 /api/system 顯示。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cxyfer/notesteme/internal/netguard"
	"gopkg.in/yaml.v3"
)

// 預設值。
const (
	DefaultConfigPath = "/config/notesteme.yaml"
	DefaultData       = "/data"
	// DefaultListen 預設只綁 loopback；Docker 部署以 NOTESTEME_LISTEN 開放。
	DefaultListen = "127.0.0.1:8080"
)

// 環境變數名稱。
const (
	EnvConfig = "NOTESTEME_CONFIG"
	EnvListen = "NOTESTEME_LISTEN"
	EnvData   = "NOTESTEME_DATA"
	// EnvSupadataAPIKey 是 Supadata 的 API key（SEC-R1：NOTESTEME_ 加上設定路徑 supadata.api_key）。
	// 加密存放的 secrets 還沒做（SEC-R4），目前只從環境變數讀取；沒有設定時 Supadata 視為 not_configured。
	EnvSupadataAPIKey = "NOTESTEME_SUPADATA_API_KEY"
	// EnvGroqAPIKey 是 Groq 的 API key（SEC-R1，對應 asr.providers.groq 的 key）。
	// 目前只從環境變數讀取（SEC-R4 加密存放時一併整理命名）；沒有設定時 Groq 視為 not_configured。
	EnvGroqAPIKey = "NOTESTEME_GROQ_API_KEY"
)

// Source 是設定值的來源。
type Source string

const (
	SourceDefault    Source = "default"
	SourceConfigFile Source = "config_file"
	SourceEnv        Source = "env"
)

// 設定欄位名稱（YAML 的 key，也是環境變數 NOTESTEME_<大寫> 的後綴）。
const (
	FieldListen = "listen"
	FieldData   = "data"
	FieldInbox  = "inbox"
	// FieldTrashRetentionDays 是 library.trash_retention_days（DEL-R4）。
	FieldTrashRetentionDays = "library.trash_retention_days"
	// FieldUploadMaxBytes 是 library.upload_max_bytes（ING-05 上傳大小上限）。
	FieldUploadMaxBytes = "library.upload_max_bytes"
)

// DefaultUploadMaxBytes 是上傳檔案大小上限的預設值（4 GiB）；設定值必須大於 0。
const DefaultUploadMaxBytes int64 = 4 << 30

// 垃圾桶保留天數（DEL-R4）：預設值與允許的範圍。
const (
	DefaultTrashRetentionDays = 30
	MinTrashRetentionDays     = 1
	MaxTrashRetentionDays     = 3650
)

// Sources 是載入設定的輸入；設定檔以外不讀程序的環境。
type Sources struct {
	// File 是設定檔路徑；空字串表示使用 Env[NOTESTEME_CONFIG]，再沒有就用預設路徑。
	// 檔案不存在時視為沒有設定檔。
	File string
	// Env 是環境變數。
	Env map[string]string
}

// Config 是生效的設定。
type Config struct {
	// Listen 是 HTTP 監聽位址。
	Listen string
	// Data 是資料目錄（DB、鎖檔、暫存）。
	Data string
	// ConfigFile 是實際讀取的設定檔路徑。
	ConfigFile string
	// TrustedProxies 是可信任的反向代理位址（設定檔 trusted_proxies，CIDR 清單，預設為空）。
	// 連線來自這些位址時，AUTH-R6 的來源位址改取 X-Forwarded-For。
	TrustedProxies []netip.Prefix
	// Tools 是設定檔 tools 區段指定的外部工具路徑，key 為工具名稱（yt-dlp、ffmpeg；OPS-R8）。
	Tools map[string]string
	// AllowPrivate 是設定檔 network.allow_private 解析後的 allowlist（GRD-R3），預設為空。
	AllowPrivate netguard.Allowlist
	// Inbox 是 API 與 MCP 可以新增檔案的收件匣目錄，預設 <Data>/inbox（GRD-R7）。
	Inbox string
	// MediaRoots 是 API 與 MCP 可以新增檔案的媒體根目錄，一律是 Clean 過的絕對路徑（GRD-R7）。
	MediaRoots []string

	// TrashRetentionDays 是垃圾桶保留天數（設定檔 library.trash_retention_days，預設 30，範圍 1～3650；DEL-R4）。
	TrashRetentionDays int
	// UploadMaxBytes 是單一上傳檔案的大小上限（設定檔 library.upload_max_bytes，預設 4 GiB，必須大於 0）。
	UploadMaxBytes int64

	// ASRProviders 是設定檔 asr.providers 解析並驗證後的內容，以 id 為 key（SEC-R3：command 類型只能在這裡定義）。
	ASRProviders map[string]ASRProvider
	// LLMProviders 是設定檔 llm.providers 解析並驗證後的內容，以 id 為 key（LLM 規格的介面）。
	LLMProviders map[string]LLMProvider

	// Origin 記錄每個欄位生效值的來源，key 為欄位名稱。
	Origin map[string]Source
}

// SourceOf 回傳欄位生效值的來源。
func (c *Config) SourceOf(field string) Source {
	if s, ok := c.Origin[field]; ok {
		return s
	}
	return SourceDefault
}

// fileSchema 是設定檔允許的欄位。
// tools、providers、asr、hooks 的內容由之後的切片定義，這裡只確認欄位名稱合法。
// asr.providers 放會執行命令的 provider，只能寫在設定檔（ADR-0013）。
type fileSchema struct {
	Listen string            `yaml:"listen"`
	Data   string            `yaml:"data"`
	Tools  map[string]string `yaml:"tools"`
	// TrustedProxies 先讀成字串，解析失敗時才能指出是哪一個值。
	TrustedProxies []string  `yaml:"trusted_proxies"`
	Providers      yaml.Node `yaml:"providers"`
	ASR            yaml.Node `yaml:"asr"`
	LLM            yaml.Node `yaml:"llm"`
	Hooks          yaml.Node `yaml:"hooks"`
	// Network 的子欄位用 KnownFields 檢查，打錯字時載入失敗。
	Network    networkSchema `yaml:"network"`
	Inbox      string        `yaml:"inbox"`
	MediaRoots []string      `yaml:"media_roots"`
	Library    librarySchema `yaml:"library"`
}

// librarySchema 是設定檔 library 區段（DEL-R4）。
type librarySchema struct {
	// TrashRetentionDays 先讀成 yaml.Node：型別不對與超出範圍時，錯誤訊息都要指出欄位。
	TrashRetentionDays yaml.Node `yaml:"trash_retention_days"`
	// UploadMaxBytes 同上，先讀成 yaml.Node 才能指出欄位。
	UploadMaxBytes yaml.Node `yaml:"upload_max_bytes"`
}

// networkSchema 是設定檔 network 區段（GRD-R3）。
type networkSchema struct {
	// AllowPrivate 先讀成字串，解析失敗時才能指出是哪一項。
	AllowPrivate []string `yaml:"allow_private"`
}

// dbManagedFields 是由資料庫管理、不能寫在設定檔的頂層欄位（SEC-R2）。
// 只列出已知的名稱：不在這裡也不在設定檔欄位中的名稱，會回報為「未知欄位」。
var dbManagedFields = map[string]bool{
	"profiles": true, "prompts": true, "budget": true, "budgets": true, "settings": true,
	"subscriptions": true, "tokens": true, "secrets": true, "cookies": true, "language": true,
	"notifications": true,
}

// Load 依 src 載入設定。
func Load(src Sources) (*Config, error) {
	cfg := &Config{
		Listen:             DefaultListen,
		Data:               DefaultData,
		TrashRetentionDays: DefaultTrashRetentionDays,
		UploadMaxBytes:     DefaultUploadMaxBytes,
		Origin:             map[string]Source{FieldListen: SourceDefault, FieldData: SourceDefault},
	}

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
		// 沒有設定檔：全部使用預設值。
	default:
		return nil, fmt.Errorf("讀取設定檔 %s 失敗：%w", path, err)
	}

	if v := src.Env[EnvListen]; v != "" {
		cfg.Listen, cfg.Origin[FieldListen] = v, SourceEnv
	}
	if v := src.Env[EnvData]; v != "" {
		cfg.Data, cfg.Origin[FieldData] = v, SourceEnv
	}
	// 資料目錄轉成絕對路徑：相對路徑會讓預設 inbox 也變成相對路徑，被 pathguard 靜默忽略（GRD-R7）。
	abs, err := filepath.Abs(cfg.Data)
	if err != nil {
		return nil, fmt.Errorf("無法取得資料目錄 %q 的絕對路徑：%w", cfg.Data, err)
	}
	cfg.Data = abs
	// inbox 的預設值依賴 data，所以要等環境變數覆寫 data 之後才計算。
	if cfg.Inbox == "" {
		cfg.Inbox = filepath.Join(cfg.Data, "inbox")
	}
	return cfg, nil
}

// applyFile 解析設定檔內容並套用到 cfg。
func applyFile(cfg *Config, raw []byte, path string) error {
	// 先確認頂層沒有不允許的欄位（SEC-R2），錯誤訊息要指出欄位名稱。
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("設定檔 %s 格式不正確：%w", path, err)
	}
	var managed, unknown []string
	for k := range top {
		switch k {
		case "listen", "data", "trusted_proxies", "tools", "providers", "hooks", "asr", "llm", "network", "inbox", "media_roots", "library":
		default:
			if dbManagedFields[k] {
				managed = append(managed, k)
			} else {
				unknown = append(unknown, k)
			}
		}
	}
	if len(managed) > 0 {
		sort.Strings(managed)
		return fmt.Errorf("設定檔 %s 不接受欄位 %s：這些設定由資料庫管理，請改用 UI 或 API 修改。",
			path, strings.Join(managed, "、"))
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("設定檔 %s 有不認得的欄位 %s，請檢查拼字。", path, strings.Join(unknown, "、"))
	}

	var f fileSchema
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// 頂層欄位上面已經檢查過；這裡讓 network 等有明確結構的區段拒絕未知的子欄位。
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("設定檔 %s 格式不正確：%w", path, err)
	}
	if f.Listen != "" {
		cfg.Listen, cfg.Origin[FieldListen] = f.Listen, SourceConfigFile
	}
	if f.Data != "" {
		cfg.Data, cfg.Origin[FieldData] = f.Data, SourceConfigFile
	}
	cfg.Tools = f.Tools
	providers, err := parseASRProviders(&f.ASR, path)
	if err != nil {
		return err
	}
	cfg.ASRProviders = providers
	llmProviders, err := parseLLM(&f.LLM, path)
	if err != nil {
		return err
	}
	cfg.LLMProviders = llmProviders
	for i, v := range f.TrustedProxies {
		p, err := netip.ParsePrefix(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("設定檔 %s 的欄位 trusted_proxies[%d] 不是有效的 CIDR（例如 \"172.18.0.0/16\"）：%q", path, i, v)
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, p.Masked())
	}

	allow, err := netguard.ParseAllowlist(f.Network.AllowPrivate)
	if err != nil {
		var ee *netguard.EntryError
		if errors.As(err, &ee) {
			if ee.Metadata.IsValid() {
				return fmt.Errorf("設定檔 %s 的欄位 network.allow_private[%d]：%s 是雲端 metadata 位址，一律封鎖，不能加入 allowlist。",
					path, ee.Index, ee.Metadata)
			}
			return fmt.Errorf("設定檔 %s 的欄位 network.allow_private[%d] 不是有效的主機名稱、IP 或 CIDR（不支援萬用字元）：%q",
				path, ee.Index, ee.Value)
		}
		return fmt.Errorf("設定檔 %s 的欄位 network.allow_private 不正確：%w", path, err)
	}
	cfg.AllowPrivate = allow

	if f.Inbox != "" {
		if !filepath.IsAbs(f.Inbox) {
			return fmt.Errorf("設定檔 %s 的欄位 inbox 必須是絕對路徑：%q", path, f.Inbox)
		}
		cfg.Inbox, cfg.Origin[FieldInbox] = filepath.Clean(f.Inbox), SourceConfigFile
	}
	if n := f.Library.TrashRetentionDays; n.Kind != 0 {
		var days int
		if n.Tag != "!!int" || n.Decode(&days) != nil { // yaml.v3 會把 1.5 截成 1，所以要先確認是整數
			return fmt.Errorf("設定檔 %s 的欄位 library.trash_retention_days 必須是整數（%d 到 %d）。",
				path, MinTrashRetentionDays, MaxTrashRetentionDays)
		}
		if days < MinTrashRetentionDays || days > MaxTrashRetentionDays {
			return fmt.Errorf("設定檔 %s 的欄位 library.trash_retention_days 必須在 %d 到 %d 之間：%d",
				path, MinTrashRetentionDays, MaxTrashRetentionDays, days)
		}
		cfg.TrashRetentionDays, cfg.Origin[FieldTrashRetentionDays] = days, SourceConfigFile
	}
	if n := f.Library.UploadMaxBytes; n.Kind != 0 {
		var size int64
		if n.Tag != "!!int" || n.Decode(&size) != nil {
			return fmt.Errorf("設定檔 %s 的欄位 library.upload_max_bytes 必須是大於 0 的整數。", path)
		}
		if size <= 0 {
			return fmt.Errorf("設定檔 %s 的欄位 library.upload_max_bytes 必須大於 0：%d", path, size)
		}
		cfg.UploadMaxBytes, cfg.Origin[FieldUploadMaxBytes] = size, SourceConfigFile
	}
	for i, v := range f.MediaRoots {
		if !filepath.IsAbs(v) {
			return fmt.Errorf("設定檔 %s 的欄位 media_roots[%d] 必須是絕對路徑：%q", path, i, v)
		}
		cfg.MediaRoots = append(cfg.MediaRoots, filepath.Clean(v))
	}
	return nil
}
