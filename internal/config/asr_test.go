package config

import (
	"reflect"
	"strings"
	"testing"
)

func loadASR(t *testing.T, yamlText string) (*Config, error) {
	t.Helper()
	return Load(Sources{File: writeFile(t, yamlText), Env: map[string]string{}})
}

// 內容取自 SEC 場景 "Command providers from the config file are listed read-only"，加上其他類型與選填欄位。
func TestLoadASRProviders(t *testing.T) {
	cfg, err := loadASR(t, `asr:
  providers:
    whisperx:
      type: command
      command: ["whisperx", "{audio}", "--output_format", "json", "--output_dir", "{out}"]
      output: { format: whisperx-json, path: "{out}/{stem}.json" }
      timing: word
      env_allowlist: [PATH, HOME, HF_HOME]
    groq: { type: openai-compatible, base_url: "https://api.groq.com/openai/v1", model: whisper-large-v3-turbo }
    voxtral: { type: mistral, model: voxtral-mini-transcribe-3 }
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ASRProviders) != 3 {
		t.Fatalf("providers = %v", cfg.ASRProviders)
	}
	want := ASRProvider{
		ID: "whisperx", Type: ASRTypeCommand,
		Command:      []string{"whisperx", "{audio}", "--output_format", "json", "--output_dir", "{out}"},
		Output:       ASROutput{Format: "whisperx-json", Path: "{out}/{stem}.json"},
		Timing:       "word",
		EnvAllowlist: []string{"PATH", "HOME", "HF_HOME"},
	}
	if got := cfg.ASRProviders["whisperx"]; !reflect.DeepEqual(got, want) {
		t.Errorf("whisperx = %+v\n預期 %+v", got, want)
	}
	if g := cfg.ASRProviders["groq"]; g.Type != ASRTypeOpenAICompatible || g.BaseURL == "" || g.Model != "whisper-large-v3-turbo" {
		t.Errorf("groq = %+v", g)
	}
	if v := cfg.ASRProviders["voxtral"]; v.Type != ASRTypeMistral || v.Model != "voxtral-mini-transcribe-3" {
		t.Errorf("voxtral = %+v", v)
	}
}

func TestLoadWithoutASRProviders(t *testing.T) {
	for _, text := range []string{"", "asr: {}\n", "asr:\n  providers: {}\n"} {
		cfg, err := loadASR(t, text)
		if err != nil {
			t.Fatalf("%q：%v", text, err)
		}
		if len(cfg.ASRProviders) != 0 {
			t.Errorf("%q：providers = %v", text, cfg.ASRProviders)
		}
	}
}

const validCommand = `      type: command
      command: ["whisperx", "{audio}"]
      output: { format: srt, path: "{out}/{stem}.srt" }
`

// 驗證失敗時整份設定檔載入失敗，訊息指出欄位。
func TestLoadASRProvidersRejectsInvalid(t *testing.T) {
	for _, c := range []struct {
		name, body, wantField string
	}{
		{"沒有 type", "      command: [x]\n", "asr.providers.p.type"},
		{"未知的 type", "      type: shell\n", "asr.providers.p.type"},
		{"command 沒有 command", "      type: command\n      output: { format: srt, path: p }\n", "asr.providers.p.command"},
		{"command 是空陣列", "      type: command\n      command: []\n      output: { format: srt, path: p }\n", "asr.providers.p.command"},
		{"argv 有空字串", "      type: command\n      command: [x, \"\"]\n      output: { format: srt, path: p }\n", "asr.providers.p.command[1]"},
		{"程式是空白", "      type: command\n      command: [\" \"]\n      output: { format: srt, path: p }\n", "asr.providers.p.command[0]"},
		{"command 沒有 output", "      type: command\n      command: [x]\n", "asr.providers.p.output"},
		{"output.format 未知", "      type: command\n      command: [x]\n      output: { format: mp3, path: p }\n", "asr.providers.p.output.format"},
		{"output.path 空白", "      type: command\n      command: [x]\n      output: { format: srt }\n", "asr.providers.p.output.path"},
		{"timing 未知", validCommand + "      timing: fast\n", "asr.providers.p.timing"},
		{"env_allowlist 含 =", validCommand + "      env_allowlist: [\"A=B\"]\n", "asr.providers.p.env_allowlist[0]"},
		{"env_allowlist 空字串", validCommand + "      env_allowlist: [\"\"]\n", "asr.providers.p.env_allowlist[0]"},
		{"command 帶 base_url", validCommand + "      base_url: http://x\n", "asr.providers.p.type"},
		{"其他類型帶 command", "      type: openai-compatible\n      command: [x]\n", "asr.providers.p.type"},
		{"其他類型帶 output", "      type: mistral\n      output: { format: srt, path: p }\n", "asr.providers.p.type"},
		{"打錯欄位名稱", validCommand + "      env_allowlsit: [PATH]\n", "env_allowlsit"},
	} {
		_, err := loadASR(t, "asr:\n  providers:\n    p:\n"+c.body)
		if err == nil {
			t.Errorf("%s：應該載入失敗", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.wantField) {
			t.Errorf("%s：錯誤訊息沒有指出 %s：%v", c.name, c.wantField, err)
		}
	}
}

func TestLoadASRProvidersRejectsBadID(t *testing.T) {
	for _, id := range []string{"a/b", "-x", "a b"} {
		_, err := loadASR(t, "asr:\n  providers:\n    \""+id+"\":\n"+validCommand)
		if err == nil || !strings.Contains(err.Error(), "asr.providers") {
			t.Errorf("id %q：err = %v，預期載入失敗並指出 asr.providers", id, err)
		}
	}
}

// providers 不是 map（例如寫成陣列）時載入失敗。
func TestLoadASRProvidersRejectsWrongShape(t *testing.T) {
	if _, err := loadASR(t, "asr:\n  providers: [a, b]\n"); err == nil {
		t.Fatal("providers 是陣列時應該載入失敗")
	}
}
