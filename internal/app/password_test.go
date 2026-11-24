package app

import (
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/auth"
	"github.com/cxyfer/notesteme/internal/store"
)

func TestSetPasswordRefusesWhileServing(t *testing.T) {
	ctx := t.Context()
	env := testEnv(t)
	srv, err := StartServer(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetPassword(ctx, env, "new-pass"); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("serve 執行中 = %v，預期 store.ErrLocked", err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSetPassword(t *testing.T) {
	ctx := t.Context()
	env := testEnv(t)
	if _, err := SetPassword(ctx, env, ""); !errors.Is(err, auth.ErrEmptyNewPassword) {
		t.Fatalf("空密碼 = %v", err)
	}
	if _, err := SetPassword(ctx, Env{Vars: env.Vars}, "x"); err == nil {
		t.Fatal("沒有 Clock 應該失敗")
	}

	// 啟動一次建立 NOTESTEME_PASSWORD 的密碼與一個 session。
	srv, err := StartServer(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := srv.auth.Login(ctx, "pw", "10.0.0.1", "ua")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	n, err := SetPassword(ctx, env, "new-pass")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("撤銷的 session = %d，預期 1", n)
	}

	srv, err = StartServer(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(ctx) }()
	if _, err := srv.auth.AuthenticateSession(ctx, cred.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("舊 session = %v", err)
	}
	if _, err := srv.auth.Login(ctx, "pw", "10.0.0.2", "ua"); !errors.Is(err, auth.ErrInvalidPassword) {
		t.Errorf("舊密碼 = %v", err)
	}
	if _, err := srv.auth.Login(ctx, "new-pass", "10.0.0.2", "ua"); err != nil {
		t.Errorf("新密碼 = %v", err)
	}
}
