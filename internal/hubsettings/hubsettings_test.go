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

// 安全修補 B：命中後 Client 會以版本 ID 下載內容，Hub 因此知道是哪一部影片；v1 的說明宣稱「無法得知」，
// 換成 v2 並要求重新確認。
func TestDisclosureV2StatesWhatAHitReveals(t *testing.T) {
	ctx := context.Background()
	if DisclosureVersion != "hub-precheck-v2" {
		t.Fatalf("DisclosureVersion = %q", DisclosureVersion)
	}
	d := Disclosure("https://hub.example.org")
	for _, want := range []string{"前 4 個十六進位字元", "會接著向 Hub 下載那個版本", "Hub 因此會知道你正在處理哪一部影片", "假的版本"} {
		if !strings.Contains(d, want) {
			t.Errorf("說明缺少「%s」：%s", want, d)
		}
	}
	if strings.Contains(d, "但無法從前綴得知是哪一部影片。") {
		t.Error("說明仍宣稱 Hub 無法得知是哪一部影片")
	}
	s := open(t, "https://hub.example.org", false)
	if _, err := s.Put(ctx, Update{Precheck: &PrecheckUpdate{Enabled: ptr(true), Acknowledge: "hub-precheck-v1"}}); !errors.Is(err, ErrAcknowledgementRequired) {
		t.Fatalf("v1 的確認 = %v", err)
	}
	// 之前確認過 v1 的使用者：查詢視為關閉，直到確認 v2。
	_ = s.Store.SetSetting(ctx, settingPrecheck, `{"enabled":true,"acknowledged":"hub-precheck-v1"}`)
	if on, _ := s.PrecheckEnabled(ctx); on {
		t.Error("確認過 v1 的查詢要關閉")
	}
	if v, err := s.Put(ctx, Update{Precheck: &PrecheckUpdate{Enabled: ptr(true), Acknowledge: "hub-precheck-v2"}}); err != nil || !v.Precheck.Enabled {
		t.Fatalf("確認 v2 = %+v %v", v, err)
	}
}
