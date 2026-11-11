package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(Config{Store: st, Clock: clk, Params: TestParams}), st, clk
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := hashPassword("correct horse", TestParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") || strings.Contains(h, "correct horse") {
		t.Fatalf("hash = %q", h)
	}
	if ok, err := verifyPassword("correct horse", h); err != nil || !ok {
		t.Errorf("verify correct = %v, %v", ok, err)
	}
	if ok, _ := verifyPassword("wrong", h); ok {
		t.Error("verify wrong = true")
	}
	if _, err := verifyPassword("x", "not-a-hash"); err == nil {
		t.Error("malformed hash should fail")
	}
}

func TestProductionParamsAreNotLowered(t *testing.T) {
	if ProductionParams.Memory < 19*1024 || ProductionParams.Time < 2 {
		t.Errorf("ProductionParams too weak: %+v", ProductionParams)
	}
	if got := New(Config{}).params; got != ProductionParams {
		t.Errorf("zero Params should select ProductionParams, got %+v", got)
	}
}

func TestEnsurePassword(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)

	if err := svc.EnsurePassword(ctx, ""); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("no password err = %v", err)
	}
	if err := svc.EnsurePassword(ctx, "first"); err != nil {
		t.Fatal(err)
	}
	// 之後環境變數不同也不會覆寫，空值也可以啟動。
	if err := svc.EnsurePassword(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsurePassword(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "second", "", ""); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("env password must not overwrite the stored one, err = %v", err)
	}
	if _, err := svc.Login(ctx, "first", "", ""); err != nil {
		t.Errorf("login with original password: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, st, clk := newService(t)
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	cred, err := svc.Login(ctx, "pw", "127.0.0.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	// DB 只存 hash，不存 token 明文。
	if _, err := st.SessionByHash(ctx, cred.Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("token plaintext must not be a key in the DB, err = %v", err)
	}
	if p, err := svc.AuthenticateSession(ctx, cred.Token); err != nil || p.Kind != "session" {
		t.Fatalf("authenticate = %+v, %v", p, err)
	}

	// 閒置期限內有使用就會延長。
	clk.Advance(20 * 24 * time.Hour)
	if _, err := svc.AuthenticateSession(ctx, cred.Token); err != nil {
		t.Fatal(err)
	}
	clk.Advance(20 * 24 * time.Hour)
	if _, err := svc.AuthenticateSession(ctx, cred.Token); err != nil {
		t.Fatalf("session should have been extended: %v", err)
	}

	if err := svc.Logout(ctx, cred.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateSession(ctx, cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after logout err = %v", err)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	ctx := context.Background()
	svc, _, clk := newService(t)
	_ = svc.EnsurePassword(ctx, "pw")
	cred, _ := svc.Login(ctx, "pw", "", "")
	clk.Advance(SessionIdleTimeout + time.Second)
	if _, err := svc.AuthenticateSession(ctx, cred.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("idle session err = %v", err)
	}
}

func TestCheckCSRF(t *testing.T) {
	tok := "session-token"
	good := csrfFor(tok)
	if err := CheckCSRF(tok, good, good); err != nil {
		t.Errorf("valid csrf: %v", err)
	}
	for name, c := range map[string][3]string{
		"缺標頭":        {tok, good, ""},
		"缺 cookie":   {tok, "", good},
		"標頭不同":       {tok, good, "other"},
		"自訂但一致":      {tok, "forged", "forged"},
		"沒有 session": {"", good, good},
	} {
		if err := CheckCSRF(c[0], c[1], c[2]); !errors.Is(err, ErrCSRF) {
			t.Errorf("%s: err = %v, want ErrCSRF", name, err)
		}
	}
}

func TestBearerNotYetSupported(t *testing.T) {
	svc, _, _ := newService(t)
	if _, err := svc.AuthenticateBearer(context.Background(), "anything"); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("err = %v", err)
	}
}

func TestLoginLockout(t *testing.T) {
	ctx := context.Background()
	svc, _, clk := newService(t)
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := svc.Login(ctx, "wrong", "", ""); !errors.Is(err, ErrInvalidPassword) {
			t.Fatalf("attempt %d: err = %v", i+1, err)
		}
	}
	// 鎖定期間連正確的密碼也拒絕，並回報剩餘時間。
	var locked *LockedError
	_, err := svc.Login(ctx, "pw", "", "")
	if !errors.As(err, &locked) || locked.RetryAfter != time.Minute || err.Error() != "密碼不正確。" {
		t.Fatalf("err = %v (%+v)", err, locked)
	}
	clk.Advance(20 * time.Second)
	if _, err := svc.Login(ctx, "pw", "", ""); !errors.As(err, &locked) || locked.RetryAfter != 40*time.Second {
		t.Fatalf("after 20s: err = %v (%+v)", err, locked)
	}
	clk.Advance(41 * time.Second)
	if _, err := svc.Login(ctx, "pw", "", ""); err != nil {
		t.Fatalf("after the lock expires: %v", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	_ = svc.EnsurePassword(ctx, "pw")
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			_, _ = svc.Login(ctx, "wrong", "", "")
		}
		if _, err := svc.Login(ctx, "pw", "", ""); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}
