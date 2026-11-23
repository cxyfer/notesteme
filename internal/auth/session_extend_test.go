package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// 延長 session 失敗不應讓有效的請求失敗：記 log 後繼續。
func TestExtendSessionFailureDoesNotFailAuthentication(t *testing.T) {
	ctx := context.Background()
	svc, _, clk := newService(t)
	if err := svc.EnsurePassword(ctx, "pw"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	svc.log = slog.New(slog.NewTextHandler(&logs, nil))
	cred, err := svc.Login(ctx, "pw", "", "")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	svc.touch = func(context.Context, string, time.Time, time.Time) error {
		called = true
		return errors.New("database is locked")
	}

	clk.Advance(2 * time.Hour) // 超過延長的最短間隔
	p, err := svc.AuthenticateSession(ctx, cred.Token)
	if err != nil || p.Kind != "session" {
		t.Fatalf("authenticate = %+v, %v", p, err)
	}
	if !called {
		t.Fatal("extend should have been attempted")
	}
	if !strings.Contains(logs.String(), "database is locked") {
		t.Errorf("failure should be logged, got %q", logs.String())
	}
}
