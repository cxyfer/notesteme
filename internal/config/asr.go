package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ASR provider 的類型（ADR-0006）。
const (
	// ASRTypeCommand 會在主機上執行命令，所以只能在設定檔中定義，API 與 MCP 只能選用（SEC-R3、ADR-0013）。
	ASRTypeCommand = "command"
	// ASRTypeOpenAICompatible 是 OpenAI、Groq、自架的 Speaches 或 faster-whisper server。
	ASRTypeOpenAICompatible = "openai-compatible"
	// ASRTypeMistral 是 Mistral（Voxtral）。
	ASRTypeMistral = "mistral"
)

// asrOutputFormats 是 command provider 的輸出可以解析的格式（ADR-0006）。
var asrOutputFormats = map[string]bool{"whisperx-json": true, "srt": true, "vtt": true, "notesteme-cues+json": true}

// asrTimings 是來源紀錄 timing（時間粒度）的值（SRC 規格）。
var asrTimings = map[string]bool{"word": true, "segment": true, "cue": true, "chunk": true, "model": true}

// asrProviderID 是 provider id 允許的字元：id 會放在 URL 路徑（GET /api/providers/{id}），所以不含 `/` 與空白。
var asrProviderID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ASRProvider 是設定檔 asr.providers.<id> 的內容。
//
// command 類型的 Command 與環境變數 allowlist 可能含敏感資訊，API 只回報 id 與類型，不回傳這些欄位。
type ASRProvider struct {
	ID   string
	Type string
	// BaseURL 與 Model 用於 openai-compatible、mistral。
	BaseURL string
	Model   string
	// Command 是 command 類型的 argv：每個元素直接傳給程式，不經過 shell；參數只能來自模板變數。
	Command []string
	// Output 描述 command 的輸出怎麼讀回來。
	Output ASROutput
	// Timing 是這個來源的時間粒度；空字串表示沒有指定。
	Timing string
	// EnvAllowlist 是 command 執行時保留的環境變數名稱；其餘一律清除。
	EnvAllowlist []string
}

// ASROutput 是 command 類型輸出的格式與位置。
type ASROutput struct {
	// Format 是 whisperx-json、srt、vtt 或 notesteme-cues+json。
	Format string
	// Path 是輸出檔的路徑模板，例如 "{out}/{stem}.json"。
	Path string
}

// asrProviderSchema 是 asr.providers.<id> 允許的欄位；打錯欄位名稱（例如 env_allowlsit）時載入失敗，
// 因為 allowlist 寫錯會讓子程序拿到不該有的環境變數。
type asrProviderSchema struct {
	Type    string   `yaml:"type"`
	BaseURL string   `yaml:"base_url"`
	Model   string   `yaml:"model"`
	Command []string `yaml:"command"`
	Output  *struct {
		Format string `yaml:"format"`
		Path   string `yaml:"path"`
	} `yaml:"output"`
	Timing       string   `yaml:"timing"`
	EnvAllowlist []string `yaml:"env_allowlist"`
}

// parseASRProviders 解析並驗證設定檔的 asr 區段，回傳以 id 為 key 的 provider。
// 沒有 asr 區段或沒有 providers 時回傳 nil。錯誤訊息指出是哪個欄位。
func parseASRProviders(node *yaml.Node, path string) (map[string]ASRProvider, error) {
	if node.IsZero() {
		return nil, nil
	}
	var asr struct {
		Providers map[string]yaml.Node `yaml:"providers"`
	}
	if err := node.Decode(&asr); err != nil {
		return nil, fmt.Errorf("設定檔 %s 的欄位 asr 格式不正確：%w", path, err)
	}
	if len(asr.Providers) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(asr.Providers))
	for id := range asr.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids) // 固定順序，錯誤訊息才不會每次不同

	out := make(map[string]ASRProvider, len(ids))
	for _, id := range ids {
		field := "asr.providers." + id
		if !asrProviderID.MatchString(id) {
			return nil, fmt.Errorf("設定檔 %s 的欄位 asr.providers 有不合法的 id %q：只能用英數字、底線、連字號與句點，並以英數字開頭。", path, id)
		}
		n := asr.Providers[id]
		p, err := decodeASRProvider(&n, id, field, path)
		if err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, nil
}

// decodeASRProvider 嚴格解析一個 provider（不接受未知欄位）並依類型驗證。
func decodeASRProvider(n *yaml.Node, id, field, path string) (ASRProvider, error) {
	raw, err := yaml.Marshal(n)
	if err != nil {
		return ASRProvider{}, fmt.Errorf("設定檔 %s 的欄位 %s 格式不正確：%w", path, field, err)
	}
	var s asrProviderSchema
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return ASRProvider{}, fmt.Errorf("設定檔 %s 的欄位 %s 格式不正確：%w", path, field, err)
	}

	bad := func(sub, format string, a ...any) error {
		return fmt.Errorf("設定檔 %s 的欄位 %s.%s %s", path, field, sub, fmt.Sprintf(format, a...))
	}
	p := ASRProvider{
		ID: id, Type: strings.TrimSpace(s.Type), BaseURL: s.BaseURL, Model: s.Model,
		Command: s.Command, Timing: s.Timing, EnvAllowlist: s.EnvAllowlist,
	}
	if s.Output != nil {
		p.Output = ASROutput{Format: s.Output.Format, Path: s.Output.Path}
	}

	if p.Timing != "" && !asrTimings[p.Timing] {
		return ASRProvider{}, bad("timing", "必須是 word、segment、cue、chunk 或 model：%q", p.Timing)
	}
	switch p.Type {
	case "":
		return ASRProvider{}, bad("type", "必須指定（%s、%s 或 %s）。", ASRTypeCommand, ASRTypeOpenAICompatible, ASRTypeMistral)
	case ASRTypeCommand:
		if s.BaseURL != "" || s.Model != "" {
			return ASRProvider{}, bad("type", "是 command 時不能指定 base_url 或 model。")
		}
		if len(p.Command) == 0 {
			return ASRProvider{}, bad("command", "必須是非空的陣列（程式與參數，不經過 shell）。")
		}
		for i, a := range p.Command {
			if a == "" || (i == 0 && strings.TrimSpace(a) == "") {
				return ASRProvider{}, bad(fmt.Sprintf("command[%d]", i), "不能是空字串。")
			}
		}
		if s.Output == nil {
			return ASRProvider{}, bad("output", "必須指定（format 與 path）。")
		}
		if !asrOutputFormats[p.Output.Format] {
			return ASRProvider{}, bad("output.format", "必須是 whisperx-json、srt、vtt 或 notesteme-cues+json：%q", p.Output.Format)
		}
		if strings.TrimSpace(p.Output.Path) == "" {
			return ASRProvider{}, bad("output.path", "必須指定，例如 \"{out}/{stem}.json\"。")
		}
		for i, e := range p.EnvAllowlist {
			if strings.TrimSpace(e) == "" || strings.ContainsAny(e, "=\x00") {
				return ASRProvider{}, bad(fmt.Sprintf("env_allowlist[%d]", i), "必須是環境變數的名稱（不含 = 與空字元）：%q", e)
			}
		}
	case ASRTypeOpenAICompatible, ASRTypeMistral:
		// 只有 command 會在主機上執行東西；其他類型不可以帶 command 的欄位，免得以為設了卻沒有作用。
		if len(p.Command) > 0 || s.Output != nil || len(p.EnvAllowlist) > 0 {
			return ASRProvider{}, bad("type", "是 %s 時不能指定 command、output 或 env_allowlist。", p.Type)
		}
	default:
		return ASRProvider{}, bad("type", "不認得：%q（可用 %s、%s、%s）。", p.Type, ASRTypeCommand, ASRTypeOpenAICompatible, ASRTypeMistral)
	}
	return p, nil
}
