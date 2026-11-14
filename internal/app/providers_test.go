package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/api"
	"github.com/cxyfer/notesteme/internal/config"
)

func TestConfigProvidersGetIsReadOnlyAndHidesSecrets(t *testing.T) {
	reg := configProviders{
		"whisperx": {
			ID: "whisperx", Type: config.ASRTypeCommand,
			Command:      []string{"whisperx", "--token", "hf_supersecret"},
			EnvAllowlist: []string{"HF_TOKEN"},
		},
	}
	info, ok := reg.Get(context.Background(), "whisperx")
	if !ok {
		t.Fatal("找不到 whisperx")
	}
	want := api.ProviderInfo{ID: "whisperx", Type: "command", Editable: false, DefinedIn: "config_file"}
	if info != want {
		t.Fatalf("info = %+v，預期 %+v", info, want)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "hf_supersecret") || strings.Contains(string(raw), "HF_TOKEN") {
		t.Fatalf("回應含有 command 的內容：%s", raw)
	}
	if _, ok := reg.Get(context.Background(), "nope"); ok {
		t.Fatal("不存在的 id 應該找不到")
	}
	// 沒有設定任何 provider（nil map）時查詢也安全。
	if _, ok := (configProviders)(nil).Get(context.Background(), "x"); ok {
		t.Fatal("nil map 不該找到任何 provider")
	}
}
