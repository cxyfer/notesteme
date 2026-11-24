package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/providers"
)

func TestProviderRegistryIsReadOnlyAndHidesSecrets(t *testing.T) {
	reg := catalogRegistry{&providers.Catalog{ASR: map[string]config.ASRProvider{
		"whisperx": {
			ID: "whisperx", Type: config.ASRTypeCommand,
			Command:      []string{"whisperx", "--token", "hf_supersecret"},
			EnvAllowlist: []string{"HF_TOKEN"},
		},
	}}}
	info, ok := reg.Get(context.Background(), "whisperx", "")
	if !ok {
		t.Fatal("找不到 whisperx")
	}
	if info.ID != "whisperx" || info.Type != "command" || info.Kind != "asr" || info.Editable || info.DefinedIn != "config_file" || !info.Testable {
		t.Fatalf("info = %+v", info)
	}
	if info.Deprecations == nil {
		t.Fatal("deprecations 應該是空陣列，不是 nil")
	}
	raw, _ := json.Marshal(reg.List(context.Background()))
	if strings.Contains(string(raw), "hf_supersecret") || strings.Contains(string(raw), "HF_TOKEN") {
		t.Fatalf("回應含有 command 的內容：%s", raw)
	}
	if _, ok := reg.Get(context.Background(), "nope", ""); ok {
		t.Fatal("不存在的 id 應該找不到")
	}
	// 沒有設定任何 provider（nil map）時查詢也安全。
	if _, ok := (catalogRegistry{&providers.Catalog{}}).Get(context.Background(), "x", ""); ok {
		t.Fatal("沒有設定時不該找到 x")
	}
}
