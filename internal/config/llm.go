package config

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LLM provider 的 wire format（llm.providers.<id>.type，ADR-0019）。
const (
	LLMTypeAnthropic = "anthropic"
	LLMTypeOpenAI    = "openai" // OpenAI 與相容的 provider
)

// llmProviderID 是 LLM provider id 允許的字元：id 會變成環境變數名稱 NOTESTEME_<ID>_API_KEY 的一部分（L6）。
var llmProviderID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// LLMProvider 是設定檔 llm.providers.<id> 的內容（LLM 規格的介面）。RPM、TPM 為 0 代表不限制（LLM-R7）。
type LLMProvider struct {
	ID      string
	Type    string
	BaseURL string
	RPM     int
	TPM     int
}

// llmProviderSchema 是 llm.providers.<id> 允許的欄位；打錯欄位名稱時載入失敗。
type llmProviderSchema struct {
	Type    string    `yaml:"type"`
	BaseURL string    `yaml:"base_url"`
	RPM     yaml.Node `yaml:"rpm"`
	TPM     yaml.Node `yaml:"tpm"`
}

// parseLLM 解析設定檔的 llm 區段。設定檔只放 provider（llm.providers）；chunk 與驗證重試由處理方案設定（PRF）。
func parseLLM(node *yaml.Node, path string) (map[string]LLMProvider, error) {
	if node.IsZero() {
		return nil, nil
	}
	var top map[string]yaml.Node
	if err := node.Decode(&top); err != nil {
		return nil, fmt.Errorf("設定檔 %s 的欄位 llm 格式不正確：%w", path, err)
	}
	for k := range top {
		if k != "providers" {
			return nil, fmt.Errorf("設定檔 %s 不接受欄位 llm.%s：設定檔的 llm 區段只能設定 providers，chunk 與驗證重試請在處理方案中設定。", path, k)
		}
	}
	var llm struct {
		Providers map[string]yaml.Node `yaml:"providers"`
	}
	if err := decodeKnownYAML(node, &llm); err != nil {
		return nil, fmt.Errorf("設定檔 %s 的欄位 llm 格式不正確：%w", path, err)
	}
	ids := make([]string, 0, len(llm.Providers))
	for id := range llm.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make(map[string]LLMProvider, len(ids))
	// envKey 是 id 對應的環境變數名稱部分（- 換成 _）：foo-bar 與 foo_bar 會讀到同一個 NOTESTEME_FOO_BAR_API_KEY。
	byEnvKey := map[string]string{}
	for _, id := range ids {
		field := "llm.providers." + id
		if !llmProviderID.MatchString(id) {
			return nil, fmt.Errorf("設定檔 %s 的欄位 llm.providers 有不合法的 id %q：只能用小寫英數字、底線與連字號，並以英數字開頭。", path, id)
		}
		envKey := strings.ReplaceAll(id, "-", "_")
		if other, ok := byEnvKey[envKey]; ok {
			return nil, fmt.Errorf("設定檔 %s 的欄位 llm.providers 的 id %q 與 %q 會使用同一個 API key 環境變數 NOTESTEME_%s_API_KEY，請改用不同的 id。",
				path, other, id, strings.ToUpper(envKey))
		}
		byEnvKey[envKey] = id
		n := llm.Providers[id]
		var s llmProviderSchema
		if err := decodeKnownYAML(&n, &s); err != nil {
			return nil, fmt.Errorf("設定檔 %s 的欄位 %s 格式不正確：%w", path, field, err)
		}
		bad := func(sub, format string, a ...any) error {
			return fmt.Errorf("設定檔 %s 的欄位 %s.%s %s", path, field, sub, fmt.Sprintf(format, a...))
		}
		p := LLMProvider{ID: id, Type: strings.TrimSpace(s.Type), BaseURL: strings.TrimSpace(s.BaseURL)}
		switch p.Type {
		case LLMTypeAnthropic, LLMTypeOpenAI:
		case "":
			return nil, bad("type", "必須指定（%s 或 %s）。", LLMTypeAnthropic, LLMTypeOpenAI)
		default:
			return nil, bad("type", "不認得：%q（可用 %s、%s）。", p.Type, LLMTypeAnthropic, LLMTypeOpenAI)
		}
		if p.BaseURL != "" {
			u, err := url.Parse(p.BaseURL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
				return nil, bad("base_url", "必須是 http 或 https 的網址（不含帳號密碼）：%q", p.BaseURL)
			}
		}
		for _, lim := range []struct {
			name string
			node yaml.Node
			dst  *int
		}{{"rpm", s.RPM, &p.RPM}, {"tpm", s.TPM, &p.TPM}} {
			if lim.node.Kind == 0 {
				continue
			}
			if lim.node.Tag != "!!int" || lim.node.Decode(lim.dst) != nil || *lim.dst < 0 {
				return nil, bad(lim.name, "必須是不小於 0 的整數（0 代表不限制）。")
			}
		}
		out[id] = p
	}
	return out, nil
}
