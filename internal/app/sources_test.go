package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/source/supadata"
	"github.com/cxyfer/notesteme/internal/store"
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
	if deps.Groq == nil || mustConfigured(t, deps.Groq) {
		t.Fatalf("沒有 API key 時應建立未設定的 Groq：%+v", deps.Groq)
	}

	deps = pipeline.Deps{}
	env := Env{Vars: map[string]string{config.EnvGroqAPIKey: "k-123"}}
	if err := s.configureSources(ctx, &deps, env, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	base, model := deps.Groq.Endpoint()
	if !mustConfigured(t, deps.Groq) || base != groq.DefaultBaseURL || model != groq.DefaultModel {
		t.Errorf("Groq = %v, %q, %q", mustConfigured(t, deps.Groq), base, model)
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
	if deps.Supadata != nil || mustConfigured(t, deps.Groq) {
		t.Errorf("只有空白應視為未設定：Supadata = %#v，Groq 已設定 = %v", deps.Supadata, mustConfigured(t, deps.Groq))
	}
}

func TestConfigureSourcesDynamicDBFallbackAndEnvOverride(t *testing.T) {
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := secrets.Open(ctx, st, "mk", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	svc := secrets.NewServices(st, keys)
	s := &Server{store: st, keys: keys, services: svc}

	var gotSupaHeader string
	supaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSupaHeader = r.Header.Get("x-api-key")
		_, _ = w.Write([]byte(`{"lang":"en","content":[{"text":"hi","offset":0,"duration":1000}]}`))
	}))
	defer supaSrv.Close()

	// 沒有環境變數時，Supadata 與 Groq 都建好但動態查 DB；尚未存入時未設定。
	deps := pipeline.Deps{HTTP: supaSrv.Client()}
	if err := s.configureSources(ctx, &deps, Env{Vars: map[string]string{}}, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	if deps.Supadata == nil || deps.Groq == nil {
		t.Fatalf("有 services 時兩個來源都應建立：supadata=%v groq=%v", deps.Supadata, deps.Groq)
	}
	sd := deps.Supadata.(*supadata.Client)
	sd.BaseURL = supaSrv.URL
	if mustConfigured(t, deps.Groq) {
		t.Fatal("DB 還沒存 key 前 Groq 應為未設定")
	}
	if _, err := sd.Fetch(ctx, "https://youtu.be/abc123xxxxx", supadata.ModeAuto); !errors.Is(err, errclass.ErrNotConfigured) {
		t.Fatalf("DB 還沒存 key 前 Supadata 應回 ErrNotConfigured，got %v", err)
	}

	// 透過 secrets.Services 存入 DB key 後，不用重啟就立刻生效。
	kGroq := "gsk_db_key_1"
	kSupa := "sup_db_key_1"
	if _, err := svc.Put(ctx, secrets.ServiceSettingsUpdate{
		Providers: map[string]secrets.ServiceProviderUpdate{
			"groq":     {APIKey: &kGroq},
			"supadata": {APIKey: &kSupa},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !mustConfigured(t, deps.Groq) {
		t.Error("存入 DB key 後 Groq.Configured 應為 true")
	}
	if _, err := sd.Fetch(ctx, "https://youtu.be/abc123xxxxx", supadata.ModeAuto); err != nil || gotSupaHeader != kSupa {
		t.Errorf("Supadata DB key 未生效：err=%v header=%q", err, gotSupaHeader)
	}

	// SEC-R1：環境變數優先於 DB key。
	depsEnv := pipeline.Deps{HTTP: supaSrv.Client()}
	env := Env{Vars: map[string]string{
		config.EnvSupadataAPIKey: "sup_env_wins",
		config.EnvGroqAPIKey:     "gsk_env_wins",
	}}
	if err := s.configureSources(ctx, &depsEnv, env, &config.Config{}); err != nil {
		t.Fatal(err)
	}
	sdEnv := depsEnv.Supadata.(*supadata.Client)
	sdEnv.BaseURL = supaSrv.URL
	if _, err := sdEnv.Fetch(ctx, "https://youtu.be/abc123xxxxx", supadata.ModeAuto); err != nil || gotSupaHeader != "sup_env_wins" {
		t.Errorf("環境變數應覆寫 DB key：err=%v header=%q", err, gotSupaHeader)
	}
	if gEnv := depsEnv.Groq.(*groq.Client); gEnv.APIKey != "gsk_env_wins" {
		t.Errorf("Groq APIKey = %q, want gsk_env_wins", gEnv.APIKey)
	}
}

// mustConfigured 呼叫 Configured，讀取 key 失敗時讓測試失敗。
func mustConfigured(t *testing.T, c interface {
	Configured(context.Context) (bool, error)
}) bool {
	t.Helper()
	ok, err := c.Configured(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}
