package app

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/pipeline"
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
