package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/oidc"
	"github.com/cxyfer/notesteme/internal/store"
)

func newM4Service(t *testing.T, cfg Config) (*Service, *store.Store, *clock.Fake, *bytes.Buffer) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	var logs bytes.Buffer
	cfg.Store, cfg.Clock, cfg.Params = st, clk, TestParams
	cfg.Log = slog.New(slog.NewTextHandler(&logs, nil))
	svc := New(cfg)
	if err := svc.EnsurePassword(context.Background(), "pw"); err != nil {
		t.Fatal(err)
	}
	return svc, st, clk, &logs
}

func proxyCfg() Config {
	return Config{
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32"), netip.MustParsePrefix("fd00::/64")},
		Proxy:          ProxyConfig{UserHeader: "Remote-User", AllowedUsers: []string{"alice", "Bob"}},
	}
}

func TestProxyUserTrustsOnlyThePeer(t *testing.T) {
	svc, _, clk, logs := newM4Service(t, proxyCfg())
	ctx := context.Background()
	cases := []struct {
		remote string
		values []string
		want   string
	}{
		{"10.0.0.5:4000", []string{"alice"}, "alice"},
		{"[::ffff:10.0.0.5]:4000", []string{"alice"}, "alice"},
		{"[fd00::1]:4000", []string{"alice"}, "alice"},
		{"10.0.0.6:4000", []string{"alice"}, ""},
		{"203.0.113.9:4000", []string{"alice"}, ""},
		{"not-an-ip", []string{"alice"}, ""},
		{"10.0.0.5:4000", nil, ""},
		{"10.0.0.5:4000", []string{"alice", "alice"}, ""},
		{"10.0.0.5:4000", []string{""}, ""},
		{"10.0.0.5:4000", []string{strings.Repeat("a", MaxProxyUserBytes+1)}, ""},
		{"10.0.0.5:4000", []string{strings.Repeat("a", MaxProxyUserBytes)}, strings.Repeat("a", MaxProxyUserBytes)},
		{"10.0.0.5:4000", []string{"ali\nce"}, ""},
		{"10.0.0.5:4000", []string{"ali\x00ce"}, ""},
		{"10.0.0.5:4000", []string{"ali\x7fce"}, ""},
		{"10.0.0.5:4000", []string{"ali\u0085ce"}, ""},
		{"10.0.0.5:4000", []string{"ali\xffce"}, ""},
		{"10.0.0.5:4000", []string{"愛麗絲"}, "愛麗絲"},
	}
	for i, c := range cases {
		clk.Advance(2 * time.Minute) // 不讓 log 節流影響判斷
		if got := svc.ProxyUser(ctx, c.remote, c.values); got != c.want {
			t.Errorf("case %d (%s %q): got %q, want %q", i, c.remote, c.values, got, c.want)
		}
	}
	if strings.Contains(logs.String(), "alice") || strings.Contains(logs.String(), "ali") {
		t.Errorf("header value leaked into logs: %s", logs)
	}
	if !strings.Contains(logs.String(), "忽略來自不受信任位址 10.0.0.6 的 Remote-User 標頭。") {
		t.Errorf("missing untrusted log: %s", logs)
	}
}

func TestProxyUserIgnoredWhenDisabled(t *testing.T) {
	svc, _, _, logs := newM4Service(t, Config{TrustedProxies: proxyCfg().TrustedProxies})
	if got := svc.ProxyUser(context.Background(), "10.0.0.5:1", []string{"alice"}); got != "" {
		t.Fatalf("disabled proxy auth returned %q", got)
	}
	if logs.Len() != 0 {
		t.Errorf("disabled proxy auth should not log: %s", logs)
	}
}

func TestProxyLogIsRateLimitedPerAddress(t *testing.T) {
	svc, _, clk, logs := newM4Service(t, proxyCfg())
	ctx := context.Background()
	for range 5 {
		svc.ProxyUser(ctx, "203.0.113.1:1", []string{"x"})
		svc.ProxyUser(ctx, "203.0.113.2:1", []string{"x"})
	}
	if n := strings.Count(logs.String(), "203.0.113.1"); n != 1 {
		t.Fatalf("got %d log lines for one address within a minute, want 1", n)
	}
	if n := strings.Count(logs.String(), "203.0.113.2"); n != 1 {
		t.Fatalf("other address: %d lines", n)
	}
	clk.Advance(time.Minute)
	svc.ProxyUser(ctx, "203.0.113.1:1", []string{"x"})
	if n := strings.Count(logs.String(), "203.0.113.1"); n != 2 {
		t.Fatalf("after a minute: %d lines, want 2", n)
	}
}

func TestLogLimiterIsBounded(t *testing.T) {
	l := newLogLimiter(2)
	now := time.Unix(0, 0)
	if !l.allow("a", now) || !l.allow("b", now) {
		t.Fatal("first two keys should be allowed")
	}
	if l.allow("c", now) {
		t.Fatal("a third key must not grow the map past its limit")
	}
	if !l.allow("c", now.Add(time.Minute)) {
		t.Fatal("expired keys should be evicted")
	}
	if len(l.last) > 2 {
		t.Fatalf("map grew to %d", len(l.last))
	}
}

func TestProxyUserAllowedAndShown(t *testing.T) {
	svc, _, _, _ := newM4Service(t, proxyCfg())
	for v, want := range map[string]bool{"alice": true, "Bob": true, "bob": false, "Alice": false, "alice ": false, "": false} {
		if got := svc.ProxyUserAllowed(v); got != want {
			t.Errorf("ProxyUserAllowed(%q) = %v", v, got)
		}
	}
	long := strings.Repeat("界", 70)
	if got := ProxyUserShown(long); got != strings.Repeat("界", 64) {
		t.Errorf("ProxyUserShown cut to %d runes", len([]rune(got)))
	}
	if got := ProxyUserShown("mallory"); got != "mallory" {
		t.Errorf("short value changed: %q", got)
	}
}

func TestProxySessionIsBoundToTheHeader(t *testing.T) {
	svc, _, _, _ := newM4Service(t, proxyCfg())
	ctx := context.Background()
	if _, err := svc.ProxyLogin(ctx, "mallory", "10.0.0.5", "ua"); !errors.Is(err, ErrProxyUserNotAllowed) {
		t.Fatalf("not allowed user: %v", err)
	}
	cred, err := svc.ProxyLogin(ctx, "alice", "10.0.0.5", "ua")
	if err != nil {
		t.Fatal(err)
	}
	// 沒有判斷過標頭（例如 /mcp）：拒絕但不撤銷。
	if _, err := svc.AuthenticateSession(ctx, cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unjudged: %v", err)
	}
	p, err := svc.AuthenticateSession(WithProxyUser(ctx, "alice"), cred.Token)
	if err != nil || p.Method != store.SessionProxy || p.SessionID != cred.SessionID {
		t.Fatalf("same user: %+v, %v", p, err)
	}
	// 換成別的值：立即撤銷，之後用原本的值也不行。
	if _, err := svc.AuthenticateSession(WithProxyUser(ctx, "Bob"), cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("other user: %v", err)
	}
	if _, err := svc.AuthenticateSession(WithProxyUser(ctx, "alice"), cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session still works: %v", err)
	}

	// 標頭消失：同樣撤銷。
	cred, _ = svc.ProxyLogin(ctx, "alice", "10.0.0.5", "ua")
	if _, err := svc.AuthenticateSession(WithProxyUser(ctx, ""), cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("header gone: %v", err)
	}
	if _, err := svc.AuthenticateSession(WithProxyUser(ctx, "alice"), cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session should be revoked after the header disappeared: %v", err)
	}

	// 密碼登入的 session 不受標頭影響。
	pw, err := svc.Login(ctx, "pw", "1.2.3.4", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := svc.AuthenticateSession(WithProxyUser(ctx, ""), pw.Token); err != nil || p.Method != store.SessionPassword {
		t.Fatalf("password session: %+v %v", p, err)
	}
}

func TestProxySessionDiesWhenProxyAuthIsTurnedOff(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := proxyCfg()
	cfg.Store, cfg.Clock, cfg.Params = st, clk, TestParams
	on := New(cfg)
	cred, err := on.ProxyLogin(context.Background(), "alice", "10.0.0.5", "")
	if err != nil {
		t.Fatal(err)
	}
	off := New(Config{Store: st, Clock: clk, Params: TestParams})
	if _, err := off.AuthenticateSession(WithProxyUser(context.Background(), "alice"), cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("proxy session survived turning proxy auth off: %v", err)
	}
	if _, err := on.AuthenticateSession(WithProxyUser(context.Background(), "alice"), cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("session should have been deleted: %v", err)
	}
}

func TestSessionsListAndRevoke(t *testing.T) {
	svc, _, clk, _ := newM4Service(t, Config{})
	ctx := context.Background()
	a, _ := svc.Login(ctx, "pw", "1.1.1.1", "ua-a")
	clk.Advance(time.Second)
	b, _ := svc.Login(ctx, "pw", "2.2.2.2", "ua-b")
	clk.Advance(2 * time.Minute)
	// a 最近使用過：排在前面；last_seen_at 每分鐘最多更新一次。
	if _, err := svc.AuthenticateSession(ctx, a.Token); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListSessions(ctx, b.SessionID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].ID != a.SessionID || list[0].Current || !list[1].Current || list[1].IP != "2.2.2.2" || list[0].Method != "password" {
		t.Fatalf("order or fields wrong: %+v", list)
	}
	if !list[0].LastSeenAt.Equal(clk.Now()) {
		t.Errorf("last_seen_at = %v, want %v", list[0].LastSeenAt, clk.Now())
	}
	seen := list[0].LastSeenAt
	clk.Advance(30 * time.Second)
	_, _ = svc.AuthenticateSession(ctx, a.Token)
	list, _ = svc.ListSessions(ctx, "")
	if !list[0].LastSeenAt.Equal(seen) {
		t.Errorf("last_seen_at updated within a minute")
	}
	if err := svc.RevokeSession(ctx, a.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateSession(ctx, a.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session still valid: %v", err)
	}
	for _, id := range []string{a.SessionID, "", "nope"} {
		if err := svc.RevokeSession(ctx, id); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("RevokeSession(%q) = %v", id, err)
		}
	}
	if _, err := svc.AuthenticateSession(ctx, b.Token); err != nil {
		t.Fatalf("other session affected: %v", err)
	}
}

func TestRotateToken(t *testing.T) {
	svc, _, clk, _ := newM4Service(t, Config{})
	ctx := context.Background()
	exp := 48 * time.Hour
	orig, err := svc.CreateToken(ctx, "agent", []string{"read", "agent"}, &exp)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	rot, err := svc.RotateToken(ctx, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if rot.ID != orig.ID || rot.Name != "agent" || !rot.ExpiresAt.Equal(*orig.ExpiresAt) ||
		strings.Join(rot.Scopes, ",") != "read,agent" || rot.Token == orig.Token || rot.Prefix != rot.Token[:tokenPrefixLen] {
		t.Fatalf("rotated = %+v (orig %+v)", rot, orig)
	}
	if _, err := svc.AuthenticateBearer(ctx, orig.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old secret still works: %v", err)
	}
	if p, err := svc.AuthenticateBearer(ctx, rot.Token); err != nil || p.TokenID != orig.ID {
		t.Fatalf("new secret: %+v %v", p, err)
	}
	// 依 ID 輪替
	if _, err := svc.RotateToken(ctx, orig.ID); err != nil {
		t.Fatal(err)
	}
	// 已撤銷、已到期、找不到
	if err := svc.RevokeToken(ctx, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RotateToken(ctx, "agent"); !errors.Is(err, ErrTokenInactive) {
		t.Errorf("revoked by name: %v", err)
	}
	if _, err := svc.RotateToken(ctx, orig.ID); !errors.Is(err, ErrTokenInactive) {
		t.Errorf("revoked by id: %v", err)
	}
	short := time.Hour
	tmp, _ := svc.CreateToken(ctx, "tmp", []string{"read"}, &short)
	clk.Advance(2 * time.Hour)
	if _, err := svc.RotateToken(ctx, tmp.ID); !errors.Is(err, ErrTokenInactive) {
		t.Errorf("expired: %v", err)
	}
	for _, x := range []string{"missing", "", "  "} {
		if _, err := svc.RotateToken(ctx, x); !errors.Is(err, ErrTokenNotFound) {
			t.Errorf("RotateToken(%q) = %v", x, err)
		}
	}
	// 撤銷後重建同名 token：名稱指向有效的那一個
	again, _ := svc.CreateToken(ctx, "agent", []string{"read"}, nil)
	rot2, err := svc.RotateToken(ctx, "agent")
	if err != nil || rot2.ID != again.ID {
		t.Fatalf("rotate re-created name: %+v %v", rot2, err)
	}
}

// fakeIdP 實作 OIDCProvider，記錄收到的參數。
type fakeIdP struct {
	issuer      string
	authErr     error
	exchangeErr error
	verifyErr   error
	sub         string
	gotVerifier string
	gotNonce    string
	lastReq     oidc.AuthRequest
}

func (f *fakeIdP) Issuer() string { return f.issuer }
func (f *fakeIdP) AuthURL(_ context.Context, r oidc.AuthRequest) (string, error) {
	f.lastReq = r
	return "https://idp.example/authorize?state=" + r.State, f.authErr
}
func (f *fakeIdP) Exchange(_ context.Context, code, verifier string) (string, error) {
	f.gotVerifier = verifier
	return "id-token-for-" + code, f.exchangeErr
}
func (f *fakeIdP) Verify(_ context.Context, raw, nonce string) (oidc.Claims, error) {
	f.gotNonce = nonce
	return oidc.Claims{Issuer: f.issuer, Subject: f.sub}, f.verifyErr
}

func newOIDCService(t *testing.T) (*Service, *fakeIdP, *clock.Fake, *bytes.Buffer) {
	t.Helper()
	idp := &fakeIdP{issuer: "https://idp.example", sub: "user-1"}
	svc, _, clk, logs := newM4Service(t, Config{OIDC: OIDCConfig{Provider: idp, AllowedSubjects: []string{"user-1"}}})
	return svc, idp, clk, logs
}

func TestOIDCHappyPathAndSingleUse(t *testing.T) {
	svc, idp, _, logs := newOIDCService(t)
	ctx := context.Background()
	st, err := svc.StartOIDC(ctx, "/items/x?t=1")
	if err != nil {
		t.Fatal(err)
	}
	r := idp.lastReq
	if len(r.State) < 43 || len(r.Nonce) < 43 || len(r.Verifier) < 43 || r.State == r.Nonce || st.Cookie == r.State {
		t.Fatalf("weak or reused random values: %+v cookie=%q", r, st.Cookie)
	}
	cb := OIDCCallback{State: r.State, Cookie: st.Cookie, Code: "c1", IP: "1.2.3.4"}
	cred, next, err := svc.FinishOIDC(ctx, cb)
	if err != nil || next != "/items/x?t=1" || cred.Token == "" {
		t.Fatalf("FinishOIDC = %+v %q %v", cred, next, err)
	}
	if idp.gotVerifier != r.Verifier || idp.gotNonce != r.Nonce {
		t.Fatal("exchange or verify did not get the stored verifier and nonce")
	}
	p, err := svc.AuthenticateSession(ctx, cred.Token)
	if err != nil || p.Method != store.SessionOIDC {
		t.Fatalf("session: %+v %v", p, err)
	}
	if _, _, err := svc.FinishOIDC(ctx, cb); !errors.Is(err, ErrOIDCState) {
		t.Fatalf("replay: %v", err)
	}
	for _, secret := range []string{r.State, r.Nonce, r.Verifier, st.Cookie, "c1", cred.Token} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log leaks a secret value: %s", logs)
		}
	}
}

func TestOIDCStateChecks(t *testing.T) {
	ctx := context.Background()
	start := func(t *testing.T) (*Service, *fakeIdP, *clock.Fake, OIDCStart) {
		svc, idp, clk, _ := newOIDCService(t)
		st, err := svc.StartOIDC(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		return svc, idp, clk, st
	}
	t.Run("missing state", func(t *testing.T) {
		svc, _, _, st := start(t)
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{Cookie: st.Cookie, Code: "c"}); !errors.Is(err, ErrOIDCState) {
			t.Fatal(err)
		}
	})
	t.Run("wrong cookie consumes the state", func(t *testing.T) {
		svc, idp, _, st := start(t)
		state := idp.lastReq.State
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: state, Cookie: "other", Code: "c"}); !errors.Is(err, ErrOIDCState) {
			t.Fatal(err)
		}
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: state, Cookie: st.Cookie, Code: "c"}); !errors.Is(err, ErrOIDCState) {
			t.Fatalf("state should be single use even after a mismatch: %v", err)
		}
	})
	t.Run("no cookie", func(t *testing.T) {
		svc, idp, _, _ := start(t)
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Code: "c"}); !errors.Is(err, ErrOIDCState) {
			t.Fatal(err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		svc, idp, clk, st := start(t)
		clk.Advance(OIDCLoginTTL)
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie, Code: "c"}); !errors.Is(err, ErrOIDCState) {
			t.Fatal(err)
		}
	})
	t.Run("idp error is sanitized and consumes the state", func(t *testing.T) {
		svc, idp, _, st := start(t)
		var denied *OIDCDeniedError
		_, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie, IdPError: "<script>"})
		if !errors.As(err, &denied) || denied.Code != "unknown" {
			t.Fatalf("err = %v", err)
		}
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie, Code: "c"}); !errors.Is(err, ErrOIDCState) {
			t.Fatal(err)
		}
	})
	t.Run("exchange and verify failures", func(t *testing.T) {
		for _, mod := range []func(*fakeIdP){
			func(f *fakeIdP) { f.exchangeErr = oidc.ErrExchange },
			func(f *fakeIdP) { f.verifyErr = oidc.ErrSignature },
		} {
			svc, idp, _, st := start(t)
			mod(idp)
			var failed *OIDCFailedError
			if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie, Code: "c"}); !errors.As(err, &failed) {
				t.Fatalf("err = %v", err)
			}
		}
	})
	t.Run("missing code", func(t *testing.T) {
		svc, idp, _, st := start(t)
		var failed *OIDCFailedError
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie}); !errors.As(err, &failed) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("subject not allowed", func(t *testing.T) {
		svc, idp, _, st := start(t)
		idp.sub = "user-1 " // 完全相等才算
		if _, _, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie, Code: "c"}); !errors.Is(err, ErrOIDCNotAllowed) {
			t.Fatal(err)
		}
		list, _ := svc.ListSessions(ctx, "")
		if len(list) != 0 {
			t.Fatalf("a session was created: %+v", list)
		}
	})
}

func TestOIDCStartLimitsAndNext(t *testing.T) {
	svc, idp, _, _ := newOIDCService(t)
	ctx := context.Background()
	for _, next := range []string{"//evil", "https://evil", "/\\evil", "javascript:x"} {
		st, err := svc.StartOIDC(ctx, next)
		if err != nil {
			t.Fatal(err)
		}
		_, got, err := svc.FinishOIDC(ctx, OIDCCallback{State: idp.lastReq.State, Cookie: st.Cookie, Code: "c"})
		if err != nil || got != "/" {
			t.Errorf("next %q -> %q, %v", next, got, err)
		}
	}
	idp.authErr = errors.New("discovery failed")
	var failed *OIDCFailedError
	if _, err := svc.StartOIDC(ctx, ""); !errors.As(err, &failed) {
		t.Fatalf("discovery failure: %v", err)
	}
	idp.authErr = nil
	for i := 0; i < MaxPendingOIDCLogins; i++ {
		if _, err := svc.StartOIDC(ctx, ""); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := svc.StartOIDC(ctx, ""); !errors.Is(err, ErrOIDCBusy) {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestOIDCPendingLoginsExpireBeforeTheLimit(t *testing.T) {
	svc, _, clk, _ := newOIDCService(t)
	ctx := context.Background()
	for i := 0; i < MaxPendingOIDCLogins; i++ {
		if _, err := svc.StartOIDC(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(OIDCLoginTTL)
	if _, err := svc.StartOIDC(ctx, ""); err != nil {
		t.Fatalf("expired logins should be cleared: %v", err)
	}
}

func TestOIDCDisabled(t *testing.T) {
	svc, _, _, _ := newM4Service(t, Config{})
	if svc.OIDCEnabled() || svc.OIDCIssuer() != "" {
		t.Fatal("OIDC should be disabled")
	}
	if _, err := svc.StartOIDC(context.Background(), ""); !errors.Is(err, ErrOIDCDisabled) {
		t.Fatal(err)
	}
	if _, _, err := svc.FinishOIDC(context.Background(), OIDCCallback{State: "x"}); !errors.Is(err, ErrOIDCDisabled) {
		t.Fatal(err)
	}
}

func TestSanitizeOIDCError(t *testing.T) {
	for in, want := range map[string]string{
		"access_denied": "access_denied", "": "unknown", "Access_Denied": "unknown", "a b": "unknown",
		"x\ny": "unknown", strings.Repeat("a", 64): strings.Repeat("a", 64), strings.Repeat("a", 65): "unknown",
		"interaction_required": "interaction_required", "日本": "unknown",
	} {
		if got := SanitizeOIDCError(in); got != want {
			t.Errorf("SanitizeOIDCError(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzProxyValue(f *testing.F) {
	for _, s := range []string{"alice", "", "a\x00b", "\xff", strings.Repeat("x", 300), "界"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, v string) {
		if !validProxyValue(v) {
			return
		}
		if len(v) == 0 || len(v) > MaxProxyUserBytes || strings.ContainsAny(v, "\r\n\x00\x7f") {
			t.Fatalf("accepted %q", v)
		}
		shown := ProxyUserShown(v)
		if !strings.HasPrefix(v, shown) || len([]rune(shown)) > 64 {
			t.Fatalf("ProxyUserShown(%q) = %q", v, shown)
		}
	})
}

func FuzzSanitizeOIDCError(f *testing.F) {
	f.Add("access_denied")
	f.Add("<b>")
	f.Fuzz(func(t *testing.T, v string) {
		got := SanitizeOIDCError(v)
		if got == "unknown" {
			return
		}
		if got != v || len(got) > 64 || strings.ContainsFunc(got, func(r rune) bool { return (r < 'a' || r > 'z') && r != '_' }) {
			t.Fatalf("SanitizeOIDCError(%q) = %q", v, got)
		}
	})
}
