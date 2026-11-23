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
