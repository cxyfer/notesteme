package app

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestRotateMasterKeyAndResetSecrets(t *testing.T) {
	ctx := t.Context()
	env := testEnv(t)

	// 啟動 server，存入一個 secret 與一筆 token、一筆設定。
	srv, err := StartServer(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.services.Put(ctx, secrets.ServiceSettingsUpdate{
		Providers: map[string]secrets.ServiceProviderUpdate{
			"groq": {APIKey: ptrStr("gsk_rotate_test_9999")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ct, err := srv.auth.CreateToken(ctx, "kept", []string{auth.ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetSetting(ctx, "marker", "keep-me"); err != nil {
		t.Fatal(err)
	}

	// serve 執行中時 rotate 與 reset 都要因 lock 而失敗。
	env.Vars["NEW_KEY"] = "mk-two"
	if err := RotateMasterKey(ctx, env, "NEW_KEY"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("rotate while running = %v, want store.ErrLocked", err)
	}
	if err := ResetSecrets(ctx, env); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("reset while running = %v, want store.ErrLocked", err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// 舊 key 錯時 RotateMasterKey 回 ErrWrongMasterKey。
	wrongEnv := testEnv(t)
	wrongEnv.Vars["NOTESTEME_DATA"] = env.Vars["NOTESTEME_DATA"]
	wrongEnv.Vars["NOTESTEME_MASTER_KEY"] = "wrong"
	wrongEnv.Vars["NEW_KEY"] = "mk-two"
	if err := RotateMasterKey(ctx, wrongEnv, "NEW_KEY"); !errors.Is(err, secrets.ErrWrongMasterKey) {
		t.Fatalf("rotate wrong key = %v, want ErrWrongMasterKey", err)
	}

	// 正確輪替後，舊 key 不能開、新 key 可以開且 secret 保留。
	if err := RotateMasterKey(ctx, env, "NEW_KEY"); err != nil {
		t.Fatalf("RotateMasterKey: %v", err)
	}
	if _, err := StartServer(ctx, env); !errors.Is(err, secrets.ErrWrongMasterKey) {
		t.Fatalf("start with old key = %v, want ErrWrongMasterKey", err)
	}
	env.Vars["NOTESTEME_MASTER_KEY"] = "mk-two"
	srv2, err := StartServer(ctx, env)
	if err != nil {
		t.Fatalf("start with new key: %v", err)
	}
	got, err := srv2.services.Get(ctx)
	if err != nil || got.Providers["groq"].APIKeyLast4 != "9999" {
		t.Fatalf("secrets after rotate = %+v, %v", got, err)
	}
	if err := srv2.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// ResetSecrets：即使原本的 master key 遺失，也能用新 key 重設，清空 secrets 但保留 token 與設定。
	resetEnv := testEnv(t)
	resetEnv.Vars["NOTESTEME_DATA"] = env.Vars["NOTESTEME_DATA"]
	resetEnv.Vars["NOTESTEME_MASTER_KEY"] = "mk-lost-reset"
	if err := ResetSecrets(ctx, resetEnv); err != nil {
		t.Fatalf("ResetSecrets: %v", err)
	}
	srv3, err := StartServer(ctx, resetEnv)
	if err != nil {
		t.Fatalf("start after reset: %v", err)
	}
	defer srv3.Kill()

	got3, err := srv3.services.Get(ctx)
	if err != nil || len(got3.Providers) != 0 {
		t.Fatalf("secrets after reset = %+v, %v, want empty", got3, err)
	}
	if v, ok, err := srv3.store.GetSetting(ctx, "marker"); err != nil || !ok || v != "keep-me" {
		t.Errorf("setting lost after reset: %q %v %v", v, ok, err)
	}
	if _, err := srv3.auth.AuthenticateBearer(ctx, ct.Token); err != nil {
		t.Errorf("token lost after reset: %v", err)
	}

	// ExpireToken 讓 token 立即失效。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv3.Addr()+"/api/items", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ct.Token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("before ExpireToken = %d", resp.StatusCode)
	}
	if err := srv3.ExpireToken(ctx, "kept"); err != nil {
		t.Fatalf("ExpireToken: %v", err)
	}
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv3.Addr()+"/api/items", nil)
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Authorization", "Bearer "+ct.Token)
	resp2, err := (&http.Client{}).Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 401 {
		t.Errorf("after ExpireToken = %d, want 401", resp2.StatusCode)
	}
}

func ptrStr(s string) *string { return &s }
