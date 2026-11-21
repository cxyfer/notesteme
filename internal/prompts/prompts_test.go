package prompts

import (
	"context"
	"errors"
	"testing"
)

func TestBodySHA256(t *testing.T) {
	// printf 'abc' | sha256sum
	if got, want := BodySHA256("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("BodySHA256 = %s，預期 %s", got, want)
	}
}

func TestContractLists(t *testing.T) {
	if len(Kinds) != 7 || len(Variables) != 7 || len(BuiltinIDs) != 5 {
		t.Errorf("Kinds=%d Variables=%d BuiltinIDs=%d，契約是 7、7、5", len(Kinds), len(Variables), len(BuiltinIDs))
	}
}

// 契約階段（Wave 0）的 stub 全部回傳 ErrNotImplemented；WP-3 實作時刪除這個測試。
func TestStubsAreNotImplemented(t *testing.T) {
	ctx := context.Background()
	s := New(Deps{})
	checks := map[string]error{}
	_, err := s.List(ctx)
	checks["List"] = err
	_, _, err = s.Create(ctx, "x", "correct", "b")
	checks["Create"] = err
	_, err = s.AddVersion(ctx, "x", "b")
	checks["AddVersion"] = err
	_, err = s.Versions(ctx, "x")
	checks["Versions"] = err
	_, err = s.SetDefault(ctx, "x", 1)
	checks["SetDefault"] = err
	checks["Delete"] = s.Delete(ctx, "x")
	_, err = s.Resolve(ctx, "x")
	checks["Resolve"] = err
	_, err = ParseRef("x")
	checks["ParseRef"] = err
	_, _, err = ParseVariables("b")
	checks["ParseVariables"] = err
	_, err = Render("b", nil)
	checks["Render"] = err
	for name, err := range checks {
		if !errors.Is(err, ErrNotImplemented) {
			t.Errorf("%s = %v，預期 ErrNotImplemented", name, err)
		}
	}
}
