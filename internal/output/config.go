// Package output 把字幕寫到輸出目標（OUT-R10～R12、ADR-0010）：sidecar（本機影片旁）與 folder（指定資料夾）。
// 每個寫出的檔案都記錄 sha256；重新寫入時只有目標檔案目前的雜湊與紀錄相同才覆蓋，否則視為使用者改過，
// 依 on_user_change 另存新檔或不寫入（OUT-R11、R12）。
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// 輸出目標。vault 的寫出由 internal/vault 與 pipeline 的 publish 處理（VAULT），這裡只檢查設定的結構。
const (
	TargetSidecar = "sidecar"
	TargetFolder  = "folder"
	TargetVault   = "vault"
)

// on_user_change 的處理方式（OUT-R12）。
const (
	PolicySaveAsNew = "save_as_new" // 預設
	PolicySkip      = "skip"
)

// Formats 是 outputs 可以寫出的格式（OUT-R1）。
var Formats = []string{"srt", "vtt", "txt", "cues-json", "ass", "bilingual-srt"}

// DefaultFormats 是沒寫 formats 時的預設。
var DefaultFormats = []string{"srt"}

// Config 是設定中 outputs 的一項。
type Config struct {
	Target string `json:"target"`
	// Formats 沒寫時用 DefaultFormats。
	Formats []string `json:"formats,omitempty"`
	// Lang 是要輸出的語言（BCP-47）；沒寫時用字幕的預設選擇（原文語言，沒有就最近產生的）。
	Lang []string `json:"lang,omitempty"`
	// Style 是 ASS 樣式名稱，只對 ass 有效，預設 default。
	Style string `json:"style,omitempty"`
	// Path 是 folder 目標的資料夾（絕對路徑），sidecar 不可以有。
	Path string `json:"path,omitempty"`
	// OnUserChange 是目標檔案被改過時的處理方式，預設 save_as_new。
	OnUserChange string `json:"on_user_change,omitempty"`
	// Vault、Folder、Hooks 只用於 vault 目標（VAULT-R1、R2、R10）。
	Vault  string   `json:"vault,omitempty"`
	Folder string   `json:"folder,omitempty"`
	Hooks  []string `json:"hooks,omitempty"`
}

// Parse 嚴格解析設定中 outputs 的一項：不認識的欄位是錯誤，避免拼錯的設定被默默忽略。
// 只檢查結構與列舉；folder 的路徑是否允許由寫出時檢查。
func Parse(raw map[string]any) (Config, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return Config{}, err
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("輸出目標設定不正確：%v", err)
	}
	return c, c.validate()
}

var langPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// ValidLang 回報 s 是不是 BCP-47 風格的語言標記（只看形狀）。
func ValidLang(s string) bool { return langPattern.MatchString(s) }

func (c Config) validate() error {
	switch c.Target {
	case TargetSidecar, TargetFolder:
		if c.Vault != "" || c.Folder != "" || c.Hooks != nil {
			return fmt.Errorf("vault、folder、hooks 只用於 vault 目標。")
		}
	case TargetVault:
		// vault 與 hook 是否存在、folder 的形狀由處理方案的驗證檢查（VAULT-R1、R2、R10）；這裡只確認結構。
		if c.Vault == "" {
			return fmt.Errorf("vault 目標必須指定 vault。")
		}
		if c.Path != "" {
			return fmt.Errorf("vault 目標不可以指定 path。")
		}
	default:
		return fmt.Errorf("輸出目標 %q 不認識，必須是 sidecar、folder 或 vault。", c.Target)
	}
	for _, f := range c.Formats {
		if !slices.Contains(Formats, f) {
			return fmt.Errorf("formats 的 %q 不支援。", f)
		}
	}
	for _, l := range c.Lang {
		if !ValidLang(l) {
			return fmt.Errorf("lang 的 %q 不是正確的語言標記。", l)
		}
	}
	if c.OnUserChange != "" && c.OnUserChange != PolicySaveAsNew && c.OnUserChange != PolicySkip {
		return fmt.Errorf("on_user_change 必須是 save_as_new 或 skip。")
	}
	switch {
	case c.Target == TargetFolder && c.Path == "":
		return fmt.Errorf("folder 目標必須指定 path。")
	case c.Target == TargetSidecar && c.Path != "":
		return fmt.Errorf("sidecar 目標寫在影片旁邊，不可以指定 path。")
	}
	return nil
}

// FormatList 回傳要寫出的格式（沒寫時用預設）。
func (c Config) FormatList() []string {
	if len(c.Formats) == 0 {
		return DefaultFormats
	}
	return c.Formats
}

// VaultFormats 回傳 vault 目標要寫出的字幕格式（VAULT-R5）：沒寫時是預設的 [srt]，`formats: []` 時不寫字幕。
func (c Config) VaultFormats() []string {
	if c.Formats == nil {
		return DefaultFormats
	}
	return c.Formats
}

// Policy 回傳目標檔案被改過時的處理方式（沒寫時是 save_as_new）。
func (c Config) Policy() string {
	if c.OnUserChange == "" {
		return PolicySaveAsNew
	}
	return c.OnUserChange
}
