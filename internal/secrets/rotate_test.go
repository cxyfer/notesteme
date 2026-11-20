package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

func ptrStr(s string) *string { return &s }

func TestServicesPutGetAndProviderKey(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	kr, err := Open(ctx, st, "key-one", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewServices(st, kr)

	// 初始為空。
	got, err := svc.Get(ctx)
	if err != nil || len(got.Providers) != 0 {
		t.Fatalf("initial Get = %+v, %v", got, err)
	}
	if k, ok, err := svc.ProviderKey(ctx, "groq"); err != nil || ok || k != "" {
		t.Fatalf("initial ProviderKey = %q, %v, %v", k, ok, err)
	}

	// 寫入兩組 API key。
	got, err = svc.Put(ctx, ServiceSettingsUpdate{
		Providers: map[string]ServiceProviderUpdate{
			"groq":              {APIKey: ptrStr("gsk_supersecret1234")},
			"openai-compatible": {APIKey: ptrStr("ab")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Providers["groq"].APIKeyLast4 != "1234" || got.Providers["openai-compatible"].APIKeyLast4 != "ab" {
		t.Fatalf("after Put = %+v", got)
	}
	if k, ok, err := svc.ProviderKey(ctx, "groq"); err != nil || !ok || k != "gsk_supersecret1234" {
		t.Fatalf("ProviderKey(groq) = %q, %v, %v", k, ok, err)
	}

	// nil 保留既有 key；"" 刪除 key。
	got, err = svc.Put(ctx, ServiceSettingsUpdate{
		Providers: map[string]ServiceProviderUpdate{
			"groq":              {APIKey: nil},
			"openai-compatible": {APIKey: ptrStr("")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Providers["groq"].APIKeyLast4 != "1234" {
		t.Errorf("nil should preserve groq: %+v", got)
	}
	if _, exists := got.Providers["openai-compatible"]; exists {
		t.Errorf("empty string should delete openai-compatible: %+v", got)
	}
	if _, ok, err := svc.ProviderKey(ctx, "openai-compatible"); err != nil || ok {
		t.Errorf("deleted ProviderKey = %v, %v", ok, err)
	}

	// 拒絕不支援的服務商與控制字元，錯誤訊息不含 key 原文，且失敗不留下部分更新。
	secretVal := "bad\nsecret_value"
	_, err = svc.Put(ctx, ServiceSettingsUpdate{
		Providers: map[string]ServiceProviderUpdate{
			"supadata": {APIKey: ptrStr("sup_valid9999")},
			"groq":     {APIKey: &secretVal},
		},
	})
	if !errors.Is(err, ErrInvalidAPIKey) {
		t.Fatalf("control char err = %v, want ErrInvalidAPIKey", err)
	}
	if strings.Contains(err.Error(), "secret_value") {
		t.Errorf("error leaked secret: %s", err)
	}
	if _, ok, _ := svc.ProviderKey(ctx, "supadata"); ok {
		t.Errorf("atomic Put should not persist supadata on failure")
	}

	if _, err := svc.Put(ctx, ServiceSettingsUpdate{
		Providers: map[string]ServiceProviderUpdate{
			"unknown-provider": {APIKey: ptrStr("xyz")},
		},
	}); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("unknown provider err = %v, want ErrUnknownProvider", err)
	}
	if _, _, err := svc.ProviderKey(ctx, "unknown-provider"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("ProviderKey unknown err = %v, want ErrUnknownProvider", err)
	}
}

func TestRotateMasterKey(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	if err := RotateMasterKey(ctx, st, "key-one", "key-two", true); !errors.Is(err, ErrNoDataKey) {
		t.Fatalf("rotate before init err = %v, want ErrNoDataKey", err)
	}

	kr, err := Open(ctx, st, "key-one", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewServices(st, kr)
	if _, err := svc.Put(ctx, ServiceSettingsUpdate{
		Providers: map[string]ServiceProviderUpdate{
			"groq": {APIKey: ptrStr("gsk_supersecret1234")},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// 錯誤的現有 master key 應回傳 ErrWrongMasterKey，舊 key 仍可使用。
	if err := RotateMasterKey(ctx, st, "wrong-key", "key-two", true); !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("wrong current master err = %v, want ErrWrongMasterKey", err)
	}
	if _, err := Open(ctx, st, "key-one", TestKDF); err != nil {
		t.Fatalf("old key should still open after failed rotate: %v", err)
	}

	// 正確輪替後，舊 key 不能開、新 key 可以開且原本 secret 完整保留。
	if err := RotateMasterKey(ctx, st, "key-one", "key-two", true); err != nil {
		t.Fatalf("RotateMasterKey: %v", err)
	}
	if _, err := Open(ctx, st, "key-one", TestKDF); !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("old key after rotate err = %v, want ErrWrongMasterKey", err)
	}
	kr2, err := Open(ctx, st, "key-two", TestKDF)
	if err != nil {
		t.Fatalf("new key open: %v", err)
	}
	svc2 := NewServices(st, kr2)
	if k, ok, err := svc2.ProviderKey(ctx, "groq"); err != nil || !ok || k != "gsk_supersecret1234" {
		t.Fatalf("secret after rotate = %q, %v, %v", k, ok, err)
	}
	if got, err := svc2.Get(ctx); err != nil || got.Providers["groq"].APIKeyLast4 != "1234" {
		t.Fatalf("Get after rotate = %+v, %v", got, err)
	}
}

func TestResetSecrets(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	kr, err := Open(ctx, st, "key-lost", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewServices(st, kr)
	if _, err := svc.Put(ctx, ServiceSettingsUpdate{
		Providers: map[string]ServiceProviderUpdate{
			"groq": {APIKey: ptrStr("gsk_supersecret1234")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "owner_password_hash", "argon2-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAPIToken(ctx, "cli", "nst_abcd", []string{"read"}, "hash-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Write().InsertItemIfAbsent(ctx, store.NewItem{
		Platform: "youtube", ExternalID: "abc123xxxxx", UntilStage: "normalize", URL: "https://youtube.com/watch?v=abc123xxxxx",
	}); err != nil {
		t.Fatal(err)
	}

	// 不知道原本的 master key 也能用新的 master key 重設。
	if err := ResetSecrets(ctx, st, "key-fresh", true); err != nil {
		t.Fatalf("ResetSecrets: %v", err)
	}
	krNew, err := Open(ctx, st, "key-fresh", TestKDF)
	if err != nil {
		t.Fatalf("Open after reset: %v", err)
	}
	svcNew := NewServices(st, krNew)
	if got, err := svcNew.Get(ctx); err != nil || len(got.Providers) != 0 {
		t.Fatalf("services after reset = %+v, %v; want empty", got, err)
	}

	// 非 secret 資料全部保留。
	if pw, ok, err := st.GetSetting(ctx, "owner_password_hash"); err != nil || !ok || pw != "argon2-hash" {
		t.Errorf("password lost: %q, %v, %v", pw, ok, err)
	}
	if _, err := st.APITokenByHash(ctx, "hash-1"); err != nil {
		t.Errorf("api_token lost: %v", err)
	}
	if items, err := st.Read().ListItems(ctx); err != nil || len(items) != 1 {
		t.Errorf("items lost: %d, %v", len(items), err)
	}
}
