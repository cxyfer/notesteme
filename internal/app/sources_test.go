package app

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/groq"
)

func TestConfigureSourcesSupadataNeedsAnAPIKey(t *testing.T) {
	s := &Server{}
	ctx := context.Background()

	var deps pipeline.Deps
	if err := s.configureSources(ctx, &deps, Env{Vars: map[string]string{}}, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if deps.Supadata != nil {
		t.Error("沒有 API key 時不該建立 Supadata")
	}

	deps = pipeline.Deps{}
	env := Env{Vars: map[string]string{config.EnvSupadataAPIKey: "k-123"}}
	if err := s.configureSources(ctx, &deps, env, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if deps.Supadata == nil {
		t.Error("有 API key 時要建立 Supadata")
	}
}

// Groq 的 client 一律建立：沒有 API key 時轉錄前回報沒有設定（SRC-R10），有 key 時使用預設的 base_url 與 model。
func TestConfigureSourcesGroqUsesDefaultsAndKey(t *testing.T) {
	s := &Server{}
	ctx := context.Background()

	var deps pipeline.Deps
	if err := s.configureSources(ctx, &deps, Env{Vars: map[string]string{}}, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if deps.Groq == nil || deps.Groq.Configured() {
		t.Fatalf("沒有 API key 時應建立未設定的 Groq：%+v", deps.Groq)
	}

	deps = pipeline.Deps{}
	env := Env{Vars: map[string]string{config.EnvGroqAPIKey: "k-123"}}
	if err := s.configureSources(ctx, &deps, env, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	base, model := deps.Groq.Endpoint()
	if !deps.Groq.Configured() || base != groq.DefaultBaseURL || model != groq.DefaultModel {
		t.Errorf("Groq = %v, %q, %q", deps.Groq.Configured(), base, model)
	}
}
