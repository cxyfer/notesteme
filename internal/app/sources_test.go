package app

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
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

// API key 前後的空白（例如 .env 的結尾換行）要去掉；只有空白視為未設定，與 doctor 一致。
func TestConfigureSourcesTrimsAPIKeys(t *testing.T) {
	s := &Server{}
	ctx := context.Background()

	var deps pipeline.Deps
	env := Env{Vars: map[string]string{config.EnvSupadataAPIKey: "  sk-1 \n", config.EnvGroqAPIKey: "\tgsk-2\r\n"}}
	if err := s.configureSources(ctx, &deps, env, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if sd, ok := deps.Supadata.(*supadata.Client); !ok || sd.APIKey != "sk-1" {
		t.Errorf("Supadata = %#v", deps.Supadata)
	}
	if g, ok := deps.Groq.(*groq.Client); !ok || g.APIKey != "gsk-2" {
		t.Errorf("Groq = %#v", deps.Groq)
	}

	deps = pipeline.Deps{}
	env = Env{Vars: map[string]string{config.EnvSupadataAPIKey: "  \n", config.EnvGroqAPIKey: " \t"}}
	if err := s.configureSources(ctx, &deps, env, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if deps.Supadata != nil || deps.Groq.Configured() {
		t.Errorf("只有空白應視為未設定：Supadata = %#v，Groq 已設定 = %v", deps.Supadata, deps.Groq.Configured())
	}
}
