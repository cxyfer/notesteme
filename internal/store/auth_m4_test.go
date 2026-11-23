package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOIDCLoginsAreSingleUseAndBounded(t *testing.T) {
	s, clk := openTest(t)
	ctx := context.Background()
	now := clk.Now()
	l := OIDCLogin{StateHash: "h1", CookieHash: "c1", Nonce: "n", CodeVerifier: "v", Next: "/x", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := s.CreateOIDCLogin(ctx, l, 2); err != nil {
		t.Fatal(err)
	}
	l2 := l
	l2.StateHash = "h2"
	if err := s.CreateOIDCLogin(ctx, l2, 2); err != nil {
		t.Fatal(err)
	}
	l3 := l
	l3.StateHash = "h3"
	if err := s.CreateOIDCLogin(ctx, l3, 2); !errors.Is(err, ErrTooMany) {
		t.Fatalf("over the limit: %v", err)
	}
	got, err := s.TakeOIDCLogin(ctx, "h1")
	if err != nil || got.CookieHash != "c1" || got.Next != "/x" || !got.ExpiresAt.Equal(l.ExpiresAt.Truncate(time.Millisecond)) {
		t.Fatalf("take = %+v, %v", got, err)
	}
	if _, err := s.TakeOIDCLogin(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second take: %v", err)
	}
	// 到期的登入在下一次建立時清掉，不佔名額。
	clk.Advance(10 * time.Minute)
	if err := s.CreateOIDCLogin(ctx, l3, 1); err != nil {
		t.Fatalf("expired rows should not count: %v", err)
	}
	if err := s.RecordOIDCIdentity(ctx, "https://idp", "sub"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordOIDCIdentity(ctx, "https://idp", "sub"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

func TestSessionMetadata(t *testing.T) {
	s, clk := openTest(t)
	ctx := context.Background()
	a, err := s.CreateSessionWith(ctx, NewSession{TokenHash: "ha", TTL: time.Hour, IP: "1.1.1.1", Method: SessionProxy, ProxyUser: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateSession(ctx, "hb", time.Hour, "2.2.2.2", "ua")
	if err != nil || b.Method != SessionPassword {
		t.Fatalf("default method: %+v %v", b, err)
	}
	got, err := s.SessionByHash(ctx, "ha")
	if err != nil || got.Method != SessionProxy || got.ProxyUser != "alice" || !got.LastSeenAt.Equal(a.LastSeenAt.Truncate(time.Millisecond)) {
		t.Fatalf("SessionByHash = %+v %v", got, err)
	}
	clk.Advance(time.Minute)
	if err := s.TouchSession(ctx, a.ID, clk.Now(), clk.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSessions(ctx)
	if err != nil || len(list) != 2 || list[0].ID != a.ID {
		t.Fatalf("list = %+v %v", list, err)
	}
	clk.Advance(time.Hour) // b 到期，a 剛好到期
	list, _ = s.ListSessions(ctx)
	if len(list) != 0 {
		t.Fatalf("expired sessions listed: %+v", list)
	}
	if ok, err := s.DeleteSessionByID(ctx, a.ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if ok, _ := s.DeleteSessionByID(ctx, a.ID); ok {
		t.Fatal("deleted twice")
	}
}

func TestRotateAPITokenStore(t *testing.T) {
	s, clk := openTest(t)
	ctx := context.Background()
	exp := clk.Now().Add(time.Hour)
	tok, err := s.CreateAPIToken(ctx, "n", "nst_aaaa", []string{"read"}, "old", &exp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.RotateAPIToken(ctx, "n", "new", "nst_bbbb")
	if err != nil || got.ID != tok.ID || got.Prefix != "nst_bbbb" || !got.ExpiresAt.Equal(*tok.ExpiresAt) {
		t.Fatalf("rotate = %+v %v", got, err)
	}
	if _, err := s.APITokenByHash(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old hash: %v", err)
	}
	if _, err := s.APITokenByHash(ctx, "new"); err != nil {
		t.Fatalf("new hash: %v", err)
	}
	// 別的 token 的 hash 撞到（UNIQUE）時是 ErrConflict，不會覆寫。
	other, _ := s.CreateAPIToken(ctx, "o", "nst_cccc", []string{"read"}, "other", nil)
	if _, err := s.RotateAPIToken(ctx, other.ID, "new", "nst_dddd"); !errors.Is(err, ErrConflict) {
		t.Fatalf("hash collision: %v", err)
	}
	clk.Advance(time.Hour)
	if _, err := s.RotateAPIToken(ctx, "n", "x", "nst_x"); !errors.Is(err, ErrTokenInactive) {
		t.Fatalf("expired by name: %v", err)
	}
	if _, err := s.RotateAPIToken(ctx, tok.ID, "x", "nst_x"); !errors.Is(err, ErrTokenInactive) {
		t.Fatalf("expired by id: %v", err)
	}
	if _, err := s.RotateAPIToken(ctx, "missing", "x", "nst_x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
