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
	"os"
	"sort"
	"strings"

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
// tools、outbound、providers、hooks 的內容由之後的切片定義，這裡只確認欄位名稱合法。
type fileSchema struct {
	Listen    string    `yaml:"listen"`
	Data      string    `yaml:"data"`
	Tools     yaml.Node `yaml:"tools"`
	Outbound  yaml.Node `yaml:"outbound"`
	Providers yaml.Node `yaml:"providers"`
	Hooks     yaml.Node `yaml:"hooks"`
}

// Load 依 src 載入設定。
func Load(src Sources) (*Config, error) {
	cfg := &Config{
		Listen: DefaultListen,
		Data:   DefaultData,
		Origin: map[string]Source{FieldListen: SourceDefault, FieldData: SourceDefault},
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
	return cfg, nil
}

// applyFile 解析設定檔內容並套用到 cfg。
func applyFile(cfg *Config, raw []byte, path string) error {
	// 先確認頂層沒有不允許的欄位（SEC-R2），錯誤訊息要指出欄位名稱。
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return fmt.Errorf("設定檔 %s 格式不正確：%w", path, err)
	}
	var unknown []string
	for k := range top {
		switch k {
		case "listen", "data", "tools", "outbound", "providers", "hooks":
		default:
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("設定檔 %s 不接受欄位 %s：這些設定由資料庫管理，請改用 UI 或 API 修改。",
			path, strings.Join(unknown, "、"))
	}

	var f fileSchema
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("設定檔 %s 格式不正確：%w", path, err)
	}
	if f.Listen != "" {
		cfg.Listen, cfg.Origin[FieldListen] = f.Listen, SourceConfigFile
	}
	if f.Data != "" {
		cfg.Data, cfg.Origin[FieldData] = f.Data, SourceConfigFile
	}
	return nil
}
