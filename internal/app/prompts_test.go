package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/prompts"
	"github.com/cxyfer/notesteme/internal/store"
)

// promptResolver 滿足 pipeline.PromptResolver，並把 prompts 的錯誤轉成 pipeline 的錯誤。
func TestPromptResolver(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ps := prompts.New(prompts.Deps{Store: st, Clock: clk})
	if _, _, err := ps.Create(ctx, "my-correct", "correct", "詞彙表：{{glossary}}"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.AddVersion(ctx, "my-correct", "新版 {{glossary}}"); err != nil {
		t.Fatal(err)
	}
	var r pipeline.PromptResolver = promptResolver{ps}

	// 成功：不帶版本跟隨預設，@N 固定版本，內建的也可以解析；內文不外流（ResolvedPrompt 沒有 Body）。
	for ref, want := range map[string]pipeline.ResolvedPrompt{
		"my-correct":                  {ID: "my-correct", Kind: "correct", Version: 1},
		"my-correct@2":                {ID: "my-correct", Kind: "correct", Version: 2},
		"localize-zh-hant-tw@default": {ID: "localize-zh-hant-tw", Kind: "translate", Version: 1},
	} {
		got, err := r.ResolvePrompt(ctx, ref)
		if err != nil || got.ID != want.ID || got.Kind != want.Kind || got.Version != want.Version || len(got.SHA256) != 64 {
			t.Errorf("ResolvePrompt(%q) = %+v, %v；預期 %+v", ref, got, err, want)
		}
	}

	if _, err := r.ResolvePrompt(ctx, "nope"); !errors.Is(err, pipeline.ErrPromptNotFound) {
		t.Errorf("不存在：err = %v，預期 ErrPromptNotFound", err)
	}
	if _, err := r.ResolvePrompt(ctx, "my-correct@9"); !errors.Is(err, pipeline.ErrPromptVersionNotFound) {
		t.Errorf("版本不存在：err = %v，預期 ErrPromptVersionNotFound", err)
	}
	_, err = r.ResolvePrompt(ctx, "my-correct@0")
	var inv *pipeline.InvalidError
	if !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Path != "prompt" {
		t.Errorf("格式錯誤：err = %v，預期帶欄位錯誤的 *pipeline.InvalidError", err)
	}
}
