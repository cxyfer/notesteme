package pipeline

import (
	"context"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// PRF-R2 的完整例子：內建預設 → 全域 → 處理方案 → 頻道 → 影音 → 重跑，後面的優先；陣列整個取代，物件逐欄位合併。
func TestCompose_LayerOrder(t *testing.T) {
	p := &Pipeline{}
	l := Layers{
		Global: map[string]any{
			"language": map[string]any{"targets": []any{"en"}, "fallback": []any{"supadata", "groq"}},
			"timing":   map[string]any{"split_timing": "free"},
		},
		Profile: ProfileLayer{Name: "standard", Version: 1, Config: map[string]any{
			"language": map[string]any{"targets": []any{"zh-Hant-TW"}},
			"timing":   map[string]any{"split_timing": "when_needed"},
		}},
		Channel: ChannelLayer{ID: "ch", Overrides: map[string]any{
			"language": map[string]any{"targets": []any{"ja"}, "fallback": []any{"groq"}},
		}},
		Item: map[string]any{"timing": map[string]any{"split_timing": "when_needed"}},
		Run:  map[string]any{"timing": map[string]any{"split_timing": "forbid"}},
	}
	eff, err := p.Compose(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	lang, err := eff.Language()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lang.Targets, []string{"ja"}) || !reflect.DeepEqual(lang.Fallback, []string{"groq"}) {
		t.Errorf("language = %+v，預期 targets=[ja]、fallback=[groq]", lang)
	}
	if got := eff["timing"].(map[string]any)["split_timing"]; got != "forbid" {
		t.Errorf("split_timing = %v，預期 forbid（重跑層優先）", got)
	}
	// 沒有被任何一層設定的內建預設仍在。
	if lang.VariantStrategy != "convert" {
		t.Errorf("variant_strategy = %q，預期內建預設 convert", lang.VariantStrategy)
	}

	// 拿掉後面的層，結果就退回前一層的值。
	for _, c := range []struct {
		name   string
		mutate func(*Layers)
		want   string
	}{
		{"沒有重跑層", func(l *Layers) { l.Run = nil }, "when_needed"},
		{"沒有影音與重跑層", func(l *Layers) { l.Run, l.Item = nil, nil }, "when_needed"},
		{"只有全域", func(l *Layers) { *l = Layers{Global: l.Global} }, "free"},
	} {
		x := l
		c.mutate(&x)
		e, err := p.Compose(context.Background(), x)
		if err != nil {
			t.Fatal(err)
		}
		if got := e["timing"].(map[string]any)["split_timing"]; got != c.want {
			t.Errorf("%s：split_timing = %v，預期 %s", c.name, got, c.want)
		}
	}
}

// 陣列整個取代，不是串接也不是依索引合併（PRF-R2）。
func TestCompose_ListsReplaced(t *testing.T) {
	p := &Pipeline{}
	eff, err := p.Compose(context.Background(), Layers{
		Global:  map[string]any{"language": map[string]any{"targets": []any{"en", "fr", "de"}}},
		Profile: ProfileLayer{Config: map[string]any{"language": map[string]any{"targets": []any{"ja"}}}},
		Channel: ChannelLayer{Overrides: map[string]any{"outputs": []any{map[string]any{"target": "sidecar"}}}},
		Item:    map[string]any{"outputs": []any{map[string]any{"target": "folder"}, map[string]any{"target": "sidecar"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lang, _ := eff.Language()
	if !reflect.DeepEqual(lang.Targets, []string{"ja"}) {
		t.Errorf("targets = %v，預期 [ja]", lang.Targets)
	}
	outs, err := eff.Outputs()
	if err != nil || len(outs) != 2 || outs[0]["target"] != "folder" {
		t.Errorf("outputs = %v, %v，預期影音層的兩項", outs, err)
	}
}

// 內建的 default 處理方案是空的一層：疊上既有的三層（內建預設、全域、影音覆寫）時，
// config_json 與 S0 的 BuildEffective(global, overrides) 逐位元組相同（M1 相容）。
func TestCompose_DefaultProfileIsIdentityForM1(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, err := r.p.SetSettings(ctx, "language", map[string]any{"targets": []any{"en", "ja"}, "fallback": []any{"groq"}}); err != nil {
		t.Fatal(err)
	}
	overrides := map[string]any{"language": map[string]any{"allow_platform_translated": true}, "supadata": map[string]any{"mode": "native"}}

	global, err := r.p.globalConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s0, err := BuildEffective(global, overrides)
	if err != nil {
		t.Fatal(err)
	}
	_, wantJSON, err := s0.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	l, err := r.p.currentLayers(ctx, r.st.Read(), "", "", overrides, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l.Profile.Name != BuiltinProfileName || l.Profile.Version != 1 || len(l.Profile.Config) != 0 || l.Channel.ID != "" {
		t.Fatalf("layers = %+v，預期內建 default v1、沒有頻道", l)
	}
	eff, err := r.p.Compose(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	_, gotJSON, _, err := SnapshotOf(eff, l)
	if err != nil {
		t.Fatal(err)
	}
	if gotJSON != wantJSON {
		t.Errorf("config_json 與 S0 不同：\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

// fakePrompts 是 PromptResolver 的替身：記下被解析的引用；missing 裡的 id 回 ErrPromptNotFound。
type fakePrompts struct {
	mu      sync.Mutex
	refs    []string
	missing map[string]bool
}

func (f *fakePrompts) ResolvePrompt(_ context.Context, ref string) (ResolvedPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs = append(f.refs, ref)
	id, ver, _ := strings.Cut(ref, "@")
	if f.missing[id] {
		return ResolvedPrompt{}, ErrPromptNotFound
	}
	v := 1
	if n, err := strconv.Atoi(ver); err == nil {
		v = n
	}
	return ResolvedPrompt{ID: id, Kind: "any", Version: v, SHA256: "sha-" + ref}, nil
}

// 只解析會執行的 LLM 步驟（api 且有 provider 與 model、agent）：skipped（沒設定、off）與 opencc 不解析；
// 解析失敗時留空，不讓 Compose 失敗；沒有 resolver 時快照不帶 prompt 版本（D11）。
func TestCompose_ResolvesPromptOnlyForRunnableStages(t *testing.T) {
	ctx := context.Background()
	fp := &fakePrompts{missing: map[string]bool{"gone": true}}
	p := &Pipeline{prompts: fp, log: slog.New(slog.DiscardHandler)}
	cfg := map[string]any{"stages": map[string]any{
		"segment":   map[string]any{"provider": "anthropic", "model": "m", "prompt": "my-seg@2"},
		"correct":   map[string]any{"executor": "agent", "prompt": "my-cor"},
		"translate": map[string]any{"executor": "opencc", "prompt": "my-tr"},
	}}
	eff, err := p.Compose(ctx, Layers{Profile: ProfileLayer{Config: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"my-seg@2", "my-cor"}; !reflect.DeepEqual(fp.refs, want) {
		t.Errorf("解析的引用 = %v，預期 %v（opencc 的 translate 不解析）", fp.refs, want)
	}
	seg, _ := eff.Stage(StageSegment)
	cor, _ := eff.Stage(StageCorrect)
	tr, _ := eff.Stage(StageTranslate)
	if seg.PromptVersion != 2 || seg.PromptSHA256 != "sha-my-seg@2" || cor.PromptVersion != 1 || cor.PromptSHA256 != "sha-my-cor" {
		t.Errorf("segment = %+v、correct = %+v", seg, cor)
	}
	if tr.PromptVersion != 0 || tr.PromptSHA256 != "" {
		t.Errorf("translate 不會執行 prompt，快照不該有版本：%+v", tr)
	}

	// 預設的 stages 沒有 provider 與 model：全部 skipped，一個都不解析。
	fp.refs = nil
	if _, err := p.Compose(ctx, Layers{}); err != nil || len(fp.refs) != 0 {
		t.Errorf("沒有設定任何步驟：refs = %v, %v，預期不解析", fp.refs, err)
	}

	// off 的步驟不解析。
	fp.refs = nil
	off := map[string]any{"stages": map[string]any{"segment": map[string]any{"executor": "off", "provider": "anthropic", "model": "m"}}}
	if _, err := p.Compose(ctx, Layers{Profile: ProfileLayer{Config: off}}); err != nil || len(fp.refs) != 0 {
		t.Errorf("executor: off：refs = %v, %v，預期不解析", fp.refs, err)
	}

	// 解析失敗：留空，Compose 仍成功。
	gone := map[string]any{"stages": map[string]any{"correct": map[string]any{"executor": "agent", "prompt": "gone"}}}
	eff, err = p.Compose(ctx, Layers{Profile: ProfileLayer{Config: gone}})
	if err != nil {
		t.Fatalf("prompt 不存在不該讓 Compose 失敗：%v", err)
	}
	if c, _ := eff.Stage(StageCorrect); c.PromptVersion != 0 || c.PromptSHA256 != "" {
		t.Errorf("解析失敗時應留空：%+v", c)
	}

	// 沒有 resolver：不解析、快照沒有版本。
	eff, err = (&Pipeline{}).Compose(ctx, Layers{Profile: ProfileLayer{Config: cfg}})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := eff.Stage(StageCorrect); c.PromptVersion != 0 || c.PromptSHA256 != "" {
		t.Errorf("沒有 resolver 時快照不該有版本：%+v", c)
	}
}

// Compose 不修改輸入的各層（解析 prompt 時複製 stages）。
func TestCompose_DoesNotMutateLayers(t *testing.T) {
	p := &Pipeline{prompts: &fakePrompts{}, log: slog.New(slog.DiscardHandler)}
	cfg := map[string]any{"stages": map[string]any{"correct": map[string]any{"executor": "agent"}}}
	if _, err := p.Compose(context.Background(), Layers{Profile: ProfileLayer{Config: cfg}}); err != nil {
		t.Fatal(err)
	}
	if got := cfg["stages"].(map[string]any)["correct"].(map[string]any); len(got) != 1 {
		t.Errorf("輸入的層被改動：%v", got)
	}
}
