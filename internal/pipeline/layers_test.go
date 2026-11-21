package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// Compose 的契約版本與 BuildEffective 逐位元組相同（M2 S0 行為不變）；層的順序是全域 → 處理方案 → 頻道 → 影音 → 重跑。
func TestCompose_WrapsBuildEffective(t *testing.T) {
	p := &Pipeline{}
	global := map[string]any{"language": map[string]any{"targets": []any{"en"}}}
	profile := map[string]any{"language": map[string]any{"targets": []any{"ja"}, "fallback": []any{"groq"}}}
	channel := map[string]any{"language": map[string]any{"fallback": []any{"supadata"}}}
	item := map[string]any{"supadata": map[string]any{"mode": "native"}}
	run := map[string]any{"language": map[string]any{"targets": []any{"fr"}}}

	got, err := p.Compose(context.Background(), Layers{
		Global: global, Profile: ProfileLayer{Name: "standard", Config: profile},
		Channel: ChannelLayer{Overrides: channel}, Item: item, Run: run,
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := BuildEffective(global, profile, channel, item, run)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Compose = %v，預期 %v", got, want)
	}
	lang, err := got.Language()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lang.Targets, []string{"fr"}) || !reflect.DeepEqual(lang.Fallback, []string{"supadata"}) {
		t.Errorf("language = %+v，預期 targets=[fr]、fallback=[supadata]", lang)
	}

	// 沒有處理方案與頻道層（nil）時，與 S0 的 BuildEffective(global, overrides) 相同。
	a, err := p.Compose(context.Background(), Layers{Global: global, Item: item})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := BuildEffective(global, item)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("沒有處理方案與頻道層：%v，預期 %v", a, b)
	}
}

func TestSnapshotOf(t *testing.T) {
	eff, err := BuildEffective(nil)
	if err != nil {
		t.Fatal(err)
	}
	l := Layers{Profile: ProfileLayer{Name: "default", Version: 1, Config: map[string]any{}}}
	sha, cfg, layers, err := SnapshotOf(eff, l)
	if err != nil {
		t.Fatal(err)
	}
	wantCfg, _ := json.Marshal(map[string]any(eff))
	if cfg != string(wantCfg) {
		t.Errorf("config JSON 不是 eff 的序列化：%s", cfg)
	}
	var back Layers
	if err := json.Unmarshal([]byte(layers), &back); err != nil || back.Profile.Name != "default" || back.Profile.Version != 1 {
		t.Errorf("layers JSON 讀不回來：%s（%v）", layers, err)
	}
	if len(sha) != 64 {
		t.Errorf("sha256 長度 = %d", len(sha))
	}
	// 決定性：相同輸入相同結果；不同的層（即使 config 相同）sha 不同。
	sha2, _, _, _ := SnapshotOf(eff, l)
	if sha != sha2 {
		t.Error("相同輸入的 sha256 不同")
	}
	l2 := l
	l2.Profile.Version = 2
	if sha3, _, _, _ := SnapshotOf(eff, l2); sha3 == sha {
		t.Error("不同的層應該有不同的 sha256")
	}
}

// 契約階段的 currentLayers 是 stub：回傳 501 類型的錯誤，WP-2 實作。
func TestCurrentLayersStubIsNotImplemented(t *testing.T) {
	_, err := (&Pipeline{}).currentLayers(context.Background(), nil, "", "", nil, nil)
	var ni *NotImplementedError
	if !errors.As(err, &ni) {
		t.Errorf("currentLayers = %v，預期 *NotImplementedError", err)
	}
}
