package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

func TestTokenCreateAuthRevokeExpire(t *testing.T) {
	ctx := context.Background()
	svc, st, clk := newService(t)

	ttl := 2 * time.Hour
	created, err := svc.CreateToken(ctx, "agent", []string{ScopeRead, ScopeAgent, ScopeRead}, &ttl)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Token) != 47 || !strings.HasPrefix(created.Token, TokenPrefix) {
		t.Fatalf("token = %q (len %d), want 47 chars starting with %q", created.Token, len(created.Token), TokenPrefix)
	}
	if created.Prefix != created.Token[:8] {
		t.Errorf("prefix = %q, want %q", created.Prefix, created.Token[:8])
	}
	if !slices.Equal(created.Scopes, []string{ScopeRead, ScopeAgent}) {
		t.Errorf("scopes = %v, want [read agent]", created.Scopes)
	}
	if created.ExpiresAt == nil || !created.ExpiresAt.Equal(clk.Now().Add(ttl)) {
		t.Errorf("expires_at = %v", created.ExpiresAt)
	}
	// DB 只存 hash，明文無法查到。
	if _, err := st.APITokenByHash(ctx, created.Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("raw token must not match DB hash, err = %v", err)
	}

	// 重複名稱拒絕。
	if _, err := svc.CreateToken(ctx, "agent", []string{ScopeRead}, nil); !errors.Is(err, ErrTokenExists) {
		t.Fatalf("duplicate name err = %v, want ErrTokenExists", err)
	}

	// 驗證成功並回傳 Principal。
	p, err := svc.AuthenticateBearer(ctx, created.Token)
	if err != nil {
		t.Fatalf("AuthenticateBearer: %v", err)
	}
	if p.Kind != "token" || p.TokenID != created.ID || !slices.Equal(p.Scopes, []string{ScopeRead, ScopeAgent}) {
		t.Errorf("principal = %+v", p)
	}
	if !p.HasScope(ScopeRead) || !p.HasScope(ScopeAgent) || p.HasScope(ScopeIngest) || p.HasScope(ScopeAdmin) {
		t.Errorf("HasScope unexpected for %+v", p)
	}

	// 沒有 nst_ 前綴直接拒絕。
	if _, err := svc.AuthenticateBearer(ctx, "bad_token"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("missing prefix err = %v", err)
	}

	// 到期後立即回傳 ErrUnauthenticated。
	clk.Advance(ttl)
	if _, err := svc.AuthenticateBearer(ctx, created.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired token err = %v, want ErrUnauthenticated", err)
	}

	// 建一個不會過期的 token，測試撤銷與名稱重用。
	c2, err := svc.CreateToken(ctx, "old", []string{ScopeAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := svc.AuthenticateBearer(ctx, c2.Token)
	if err != nil {
		t.Fatal(err)
	}
	// admin scope 不隱含 read 或 ingest。
	if !p2.HasScope(ScopeAdmin) || p2.HasScope(ScopeRead) || p2.HasScope(ScopeIngest) || p2.HasScope(ScopeAgent) {
		t.Errorf("admin must not imply other scopes: %+v", p2)
	}
	if err := svc.RevokeToken(ctx, "old"); err != nil {
		t.Fatalf("RevokeToken by name: %v", err)
	}
	if err := svc.RevokeToken(ctx, "old"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("second RevokeToken err = %v, want ErrTokenNotFound", err)
	}
	if _, err := svc.AuthenticateBearer(ctx, c2.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked token err = %v, want ErrUnauthenticated", err)
	}

	// 撤銷後可以同名重建，且可以用 ID 撤銷。
	c3, err := svc.CreateToken(ctx, "old", []string{ScopeIngest}, nil)
	if err != nil {
		t.Fatalf("recreate after revoke: %v", err)
	}
	if err := svc.RevokeToken(ctx, c3.ID); err != nil {
		t.Fatalf("RevokeToken by id: %v", err)
	}

	// ExpireTokenByName 測試。
	c4, err := svc.CreateToken(ctx, "temp", []string{ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ExpireTokenByName(ctx, "temp"); err != nil {
		t.Fatalf("ExpireTokenByName: %v", err)
	}
	if _, err := svc.AuthenticateBearer(ctx, c4.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("after ExpireTokenByName err = %v, want ErrUnauthenticated", err)
	}
	if err := svc.ExpireTokenByName(ctx, "missing"); !errors.Is(err, ErrTokenNotFound) {
		t.Errorf("ExpireTokenByName missing err = %v, want ErrTokenNotFound", err)
	}

	list, err := svc.ListTokens(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("ListTokens = %d, %v", len(list), err)
	}
}

func TestTokenTouchThrottled(t *testing.T) {
	ctx := context.Background()
	svc, _, clk := newService(t)

	touches := 0
	orig := svc.touchToken
	svc.touchToken = func(ctx context.Context, id string) error {
		touches++
		return orig(ctx, id)
	}

	c, err := svc.CreateToken(ctx, "cli", []string{ScopeRead}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 第一次驗證一定更新。
	if _, err := svc.AuthenticateBearer(ctx, c.Token); err != nil {
		t.Fatal(err)
	}
	if touches != 1 {
		t.Fatalf("touches = %d, want 1", touches)
	}

	// 未滿 1 小時不重複寫入。
	clk.Advance(30 * time.Minute)
	if _, err := svc.AuthenticateBearer(ctx, c.Token); err != nil {
		t.Fatal(err)
	}
	if touches != 1 {
		t.Fatalf("touches before interval = %d, want 1", touches)
	}

	// 滿 1 小時後再更新。
	clk.Advance(30 * time.Minute)
	if _, err := svc.AuthenticateBearer(ctx, c.Token); err != nil {
		t.Fatal(err)
	}
	if touches != 2 {
		t.Fatalf("touches after interval = %d, want 2", touches)
	}
}

func TestTokenValidationAndPrincipalScopes(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)

	zero := time.Duration(0)
	neg := -time.Second
	cases := []struct {
		name      string
		scopes    []string
		expiresIn *time.Duration
	}{
		{"", []string{ScopeRead}, nil},
		{"   ", []string{ScopeRead}, nil},
		{"bad\nname", []string{ScopeRead}, nil},
		{"ok", nil, nil},
		{"ok", []string{"unknown"}, nil},
		{"ok", []string{ScopeRead}, &zero},
		{"ok", []string{ScopeRead}, &neg},
	}
	for i, c := range cases {
		if _, err := svc.CreateToken(ctx, c.name, c.scopes, c.expiresIn); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("case %d: err = %v, want ErrInvalidToken", i, err)
		}
	}

	// 非 token 身分（session／local）永遠回傳 true。
	for _, kind := range []string{"session", "local"} {
		p := Principal{Kind: kind}
		for _, sc := range []string{ScopeRead, ScopeAgent, ScopeIngest, ScopeAdmin} {
			if !p.HasScope(sc) {
				t.Errorf("%s should have scope %s", kind, sc)
			}
		}
	}
}
