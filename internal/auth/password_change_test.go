package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChangePassword(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	if err := svc.EnsurePassword(ctx, "old-pass"); err != nil {
		t.Fatal(err)
	}
	keep, err := svc.Login(ctx, "old-pass", "10.0.0.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Login(ctx, "old-pass", "10.0.0.2", "ua")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ChangePassword(ctx, "wrong", "new-pass", "10.0.0.1", keep.Token); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("wrong current err = %v", err)
	}
	if err := svc.ChangePassword(ctx, "old-pass", "", "10.0.0.1", keep.Token); !errors.Is(err, ErrEmptyNewPassword) {
		t.Fatalf("empty new err = %v", err)
	}
	if err := svc.ChangePassword(ctx, "old-pass", "new-pass", "10.0.0.1", keep.Token); err != nil {
		t.Fatalf("change err = %v", err)
	}

	// 舊密碼失效、新密碼可以登入
	if _, err := svc.Login(ctx, "old-pass", "10.0.0.3", "ua"); !errors.Is(err, ErrInvalidPassword) {
		t.Errorf("old password login err = %v", err)
	}
	if _, err := svc.Login(ctx, "new-pass", "10.0.0.3", "ua"); err != nil {
		t.Errorf("new password login err = %v", err)
	}
	// 目前的 session 保留，其他的撤銷
	if _, err := svc.AuthenticateSession(ctx, keep.Token); err != nil {
		t.Errorf("current session err = %v", err)
	}
	if _, err := svc.AuthenticateSession(ctx, other.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("other session err = %v", err)
	}
}

func TestChangePasswordWithoutSessionRevokesAll(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newService(t)
	if err := svc.EnsurePassword(ctx, "old-pass"); err != nil {
		t.Fatal(err)
	}
	c, err := svc.Login(ctx, "old-pass", "10.0.0.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(ctx, "old-pass", "new-pass", "local", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthenticateSession(ctx, c.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("session err = %v", err)
	}
}

// 目前密碼的驗證和登入共用鎖定（AUTH-R6）：修改密碼的錯誤會讓登入也被鎖，反之亦然。
func TestChangePasswordSharesLoginLockout(t *testing.T) {
	ctx := context.Background()
	svc, _, clk := newService(t)
	if err := svc.EnsurePassword(ctx, "old-pass"); err != nil {
		t.Fatal(err)
	}
	const ip = "10.0.0.9"
	for i := 0; i < 3; i++ {
		_ = svc.ChangePassword(ctx, "wrong", "x", ip, "")
	}
	for i := 0; i < 2; i++ {
		_, _ = svc.Login(ctx, "wrong", ip, "ua")
	}
	var locked *LockedError
	if err := svc.ChangePassword(ctx, "old-pass", "new-pass", ip, ""); !errors.As(err, &locked) {
		t.Fatalf("change while locked err = %v", err)
	}
	if _, err := svc.Login(ctx, "old-pass", ip, "ua"); !errors.As(err, &locked) {
		t.Fatalf("login while locked err = %v", err)
	}
	clk.Advance(lockDuration + time.Second)
	if err := svc.ChangePassword(ctx, "old-pass", "new-pass", ip, ""); err != nil {
		t.Fatalf("after lock err = %v", err)
	}
}
