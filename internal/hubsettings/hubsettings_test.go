package hubsettings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

func open(t *testing.T, url string, cfgPrecheck bool) *Settings {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), clock.NewFake(time.Unix(0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	k, err := secrets.Open(ctx, st, "master-key", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	return &Settings{URL: url, ConfigPrecheck: cfgPrecheck, Store: st, Secrets: secrets.NewServices(st, k)}
}

func ptr[T any](v T) *T { return &v }

func TestTokenIsWriteOnlyAndPartial(t *testing.T) {
	ctx := context.Background()
	s := open(t, "https://hub.example.org", false)
	v, err := s.Put(ctx, Update{Token: ptr("nsh_secret")})
	if err != nil || !v.TokenSet || v.URL != "https://hub.example.org" {
		t.Fatalf("v = %+v %v", v, err)
	}
	if v, _ := s.Put(ctx, Update{}); !v.TokenSet {
		t.Error("省略 token 時不變")
	}
	if tok, ok, _ := s.Token(ctx); tok != "nsh_secret" || !ok {
		t.Error("token")
	}
	if v, _ := s.Put(ctx, Update{Token: ptr("")}); v.TokenSet {
		t.Error(`token: "" 要刪除`)
	}
	if _, err := s.Put(ctx, Update{Token: ptr("a\nb")}); !errors.Is(err, secrets.ErrInvalidHubToken) {
		t.Error(err)
	}
}

func TestPrecheckNeedsAcknowledgement(t *testing.T) {
	ctx := context.Background()
	s := open(t, "https://hub.example.org", false)
	v, _ := s.Get(ctx)
	if v.Precheck.Enabled || v.Precheck.DisclosureVersion != DisclosureVersion || !strings.Contains(v.Precheck.Disclosure, "https://hub.example.org") {
		t.Fatalf("預設關閉：%+v", v)
	}
	if _, err := s.Put(ctx, Update{Token: ptr("x"), Precheck: &PrecheckUpdate{Enabled: ptr(true)}}); !errors.Is(err, ErrAcknowledgementRequired) {
		t.Fatal(err)
	}
	if _, ok, _ := s.Token(ctx); ok {
		t.Error("驗證失敗時什麼都不寫入")
	}
	if _, err := s.Put(ctx, Update{Precheck: &PrecheckUpdate{Enabled: ptr(true), Acknowledge: "hub-precheck-v0"}}); !errors.Is(err, ErrAcknowledgementRequired) {
		t.Fatal("舊版本的確認不算")
	}
	if v, err := s.Put(ctx, Update{Precheck: &PrecheckUpdate{Enabled: ptr(true), Acknowledge: DisclosureVersion}}); err != nil || !v.Precheck.Enabled {
		t.Fatalf("v = %+v %v", v, err)
	}
	if v, _ := s.Put(ctx, Update{Precheck: &PrecheckUpdate{Enabled: ptr(false)}}); v.Precheck.Enabled {
		t.Error("關閉不需要確認")
	}
	if v, _ := s.Put(ctx, Update{Precheck: &PrecheckUpdate{Acknowledge: DisclosureVersion}}); v.Precheck.Enabled {
		t.Error("沒有 enabled 時不變")
	}
	// 存的是舊版本的確認時視為關閉。
	_ = s.Store.SetSetting(ctx, settingPrecheck, `{"enabled":true,"acknowledged":"old"}`)
	if on, _ := s.PrecheckEnabled(ctx); on {
		t.Error("舊版本的確認要重新確認")
	}
	_ = s.Store.SetSetting(ctx, settingPrecheck, `garbage`)
	if on, _ := s.PrecheckEnabled(ctx); on {
		t.Error("壞掉的設定視為關閉")
	}
}

func TestPrecheckFromConfigFile(t *testing.T) {
	s := open(t, "", true)
	if on, _ := s.PrecheckEnabled(context.Background()); !on {
		t.Error("設定檔開啟時生效")
	}
	if !strings.Contains(Disclosure(""), "尚未設定") {
		t.Error(Disclosure(""))
	}
	var nilS *Settings
	if on, _ := nilS.PrecheckEnabled(context.Background()); on {
		t.Error("nil")
	}
	if _, ok, _ := nilS.Token(context.Background()); ok {
		t.Error("nil")
	}
}
