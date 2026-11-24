package secrets

import (
	"context"
	"strings"
	"testing"
)

func TestHubToken(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	k, err := Open(ctx, st, "key-one", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServices(st, k)
	if _, ok, err := s.HubToken(ctx); ok || err != nil {
		t.Fatalf("一開始沒有 token：%v %v", ok, err)
	}
	if err := s.SetHubToken(ctx, "nsh_abc"); err != nil {
		t.Fatal(err)
	}
	if tok, ok, err := s.HubToken(ctx); tok != "nsh_abc" || !ok || err != nil {
		t.Fatalf("token = %q %v %v", tok, ok, err)
	}
	sec, _ := st.Read().Secret(ctx, HubKind, HubTokenName)
	if strings.Contains(string(sec.Ciphertext), "nsh_abc") {
		t.Error("token 要加密存放")
	}
	for _, bad := range []string{"a\nb", " x", strings.Repeat("x", maxHubToken+1)} {
		if err := s.SetHubToken(ctx, bad); err != ErrInvalidHubToken {
			t.Errorf("%q：%v", bad, err)
		}
	}
	if err := s.SetHubToken(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.HubToken(ctx); ok {
		t.Error("空字串要刪除 token")
	}
	if got, _ := s.Get(ctx); len(got.Providers) != 0 {
		t.Error("Hub token 不是服務商的 API key")
	}
}

// Values 給洩漏掃描用：回傳所有 secret 的明文（Hub token、API key、cookie…）。
func TestValuesDecryptsEverySecret(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	k, err := Open(ctx, st, "key-one", TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServices(st, k)
	if v, err := s.Values(ctx); err != nil || len(v) != 0 {
		t.Fatalf("沒有 secret：%v %v", v, err)
	}
	if err := s.SetHubToken(ctx, "nsh_hub_token"); err != nil {
		t.Fatal(err)
	}
	n, c, err := k.Seal("cookie", "bilibili", []byte("SESSDATA=abc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Write().UpsertSecret(ctx, "cookie", "bilibili", n, c); err != nil {
		t.Fatal(err)
	}
	v, err := s.Values(ctx)
	if err != nil || strings.Join(v, ",") != "SESSDATA=abc,nsh_hub_token" {
		t.Fatalf("Values = %v %v", v, err)
	}
	var nilS *Services
	if v, err := nilS.Values(ctx); v != nil || err != nil {
		t.Error("nil")
	}
	// 解不開時回錯誤（不能默默略過，掃描會漏掉）。
	if err := st.Write().UpsertSecret(ctx, "cookie", "broken", []byte("123456789012"), []byte("garbage")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Values(ctx); err == nil {
		t.Error("壞掉的 secret 要回錯誤")
	}
}
