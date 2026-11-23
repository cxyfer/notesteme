// Package storetest 是 hubstore.Store 的共用測試：SQLite 與 Postgres 兩種 backend 都要通過（ADR-0030 第 2 點）。
// 各切片加入 Store 的方法時，在這裡加對應的測試，而不是只寫在某一個 backend 的測試中。
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// Open 回傳一個新的、空的 Store，時間取自 clk。
type Open func(t *testing.T, clk clock.Clock) hubstore.Store

var epoch = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

func fresh(t *testing.T, open Open) (hubstore.Store, *clock.Fake) {
	clk := clock.NewFake(epoch)
	return open(t, clk), clk
}

func ident(provider, subject, login string) hubstore.ExternalIdentity {
	return hubstore.ExternalIdentity{Provider: provider, Subject: subject, Login: login, Email: login + "@example.com", EmailVerified: true}
}

// Run 對 open 回傳的 Store 執行全部的共用測試。
func Run(t *testing.T, open Open) {
	ctx := context.Background()
	t.Run("Empty", func(t *testing.T) {
		s, _ := fresh(t, open)
		if err := s.CheckWritable(ctx); err != nil {
			t.Fatalf("CheckWritable：%v", err)
		}
		n, err := s.CountUsers(ctx)
		if err != nil || n != 0 {
			t.Fatalf("CountUsers = %d, %v，預期 0", n, err)
		}
		v, err := s.SchemaVersion(ctx)
		if err != nil || v < 2 {
			t.Fatalf("SchemaVersion = %d, %v，預期已套用 migration", v, err)
		}
	})

	t.Run("SignInCreatesOnceAndRefreshes", func(t *testing.T) {
		s, _ := fresh(t, open)
		u, created, err := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		if err != nil || !created || u.DisplayName != "alice" || u.Suspended() {
			t.Fatalf("SignIn = %+v, %v, %v", u, created, err)
		}
		renamed := ident("github", "1", "alice-new")
		renamed.Attrs.Orgs = []string{"o"}
		u2, created, err := s.SignIn(ctx, renamed, "alice-new")
		if err != nil || created || u2.ID != u.ID || u2.DisplayName != "alice" {
			t.Fatalf("同一個身分再登入 = %+v, %v, %v，預期同一個帳號且不改 display_name", u2, created, err)
		}
		ids, err := s.Identities(ctx, u.ID)
		if err != nil || len(ids) != 1 || ids[0].Login != "alice-new" || len(ids[0].Attrs.Orgs) != 1 {
			t.Fatalf("Identities = %+v, %v，預期 login 與屬性已更新", ids, err)
		}
		// 同一個 email 在另一個供應商：新帳號（HACC-R5）。
		u3, created, err := s.SignIn(ctx, ident("google", "g-1", "alice"), "alice")
		if err != nil || !created || u3.ID == u.ID {
			t.Fatalf("另一個供應商 = %+v, %v, %v，預期新帳號", u3, created, err)
		}
		if n, _ := s.CountUsers(ctx); n != 2 {
			t.Fatalf("CountUsers = %d，預期 2", n)
		}
		if _, err := s.User(ctx, "missing"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("User(missing) = %v", err)
		}
	})

	t.Run("LinkAndUnlink", func(t *testing.T) {
		s, clk := fresh(t, open)
		alice, _, _ := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		bob, _, _ := s.SignIn(ctx, ident("linuxdo", "2", "bob"), "bob")
		if err := s.UnlinkIdentity(ctx, alice.ID, "github"); !errors.Is(err, hubstore.ErrLastIdentity) {
			t.Fatalf("移除唯一的身分 = %v，預期 ErrLastIdentity", err)
		}
		if err := s.UnlinkIdentity(ctx, alice.ID, "google"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("移除沒有的身分 = %v，預期 ErrNotFound", err)
		}
		if _, err := s.LinkIdentity(ctx, bob.ID, ident("github", "1", "alice")); !errors.Is(err, hubstore.ErrIdentityInUse) {
			t.Fatalf("連結別人的身分 = %v，預期 ErrIdentityInUse", err)
		}
		if _, err := s.LinkIdentity(ctx, alice.ID, ident("github", "9", "alice-work")); !errors.Is(err, hubstore.ErrProviderLinked) {
			t.Fatalf("連結同一個供應商的第二個身分 = %v，預期 ErrProviderLinked", err)
		}
		// 同一毫秒內連結：順序仍依連結的先後。
		if _, err := s.LinkIdentity(ctx, alice.ID, ident("google", "g-a", "alice")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.LinkIdentity(ctx, alice.ID, ident("linuxdo", "3", "alice")); err != nil {
			t.Fatal(err)
		}
		ids, _ := s.Identities(ctx, alice.ID)
		if len(ids) != 3 || ids[0].Provider != "github" || ids[1].Provider != "google" || ids[2].Provider != "linuxdo" {
			t.Fatalf("Identities = %+v，預期 github、google、linuxdo", ids)
		}
		// 連結過的身分登入同一個帳號。
		if u, created, _ := s.SignIn(ctx, ident("google", "g-a", "alice"), "x"); created || u.ID != alice.ID {
			t.Fatalf("以連結的身分登入 = %+v, %v", u, created)
		}
		clk.Advance(time.Second)
		if err := s.UnlinkIdentity(ctx, alice.ID, "google"); err != nil {
			t.Fatal(err)
		}
		if u, created, _ := s.SignIn(ctx, ident("google", "g-a", "alice"), "alice"); !created || u.ID == alice.ID {
			t.Fatalf("移除後再以該身分登入 = %+v, %v，預期新帳號", u, created)
		}
		entries, err := s.AuditEntries(ctx, 10)
		if err != nil || len(entries) != 3 || entries[0].Action != hubstore.AuditIdentityUnlink || entries[0].Actor != "alice" ||
			entries[0].TargetKind != "identity" {
			t.Fatalf("AuditEntries = %+v, %v，預期最新一筆是 alice 的 identity.unlink", entries, err)
		}
		// 失敗的連結不寫入稽核紀錄、不改變帳號數。
		if n, _ := s.CountUsers(ctx); n != 3 {
			t.Fatalf("CountUsers = %d，預期 3", n)
		}
	})

	t.Run("OAuthStateIsUsedOnceAndExpires", func(t *testing.T) {
		s, clk := fresh(t, open)
		st := hubstore.OAuthState{
			StateHash: "h1", Provider: "github", Intent: hubstore.IntentLogin, VerifierNonce: []byte{1}, VerifierSealed: []byte{2},
			CreatedAt: clk.Now(), ExpiresAt: clk.Now().Add(10 * time.Minute),
		}
		if err := s.CreateOAuthState(ctx, st); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConsumeOAuthState(ctx, "h1", "google"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("另一個供應商取用 = %v，預期 ErrNotFound", err)
		}
		got, err := s.ConsumeOAuthState(ctx, "h1", "github")
		if err != nil || got.Intent != hubstore.IntentLogin || string(got.VerifierSealed) != "\x02" {
			t.Fatalf("ConsumeOAuthState = %+v, %v", got, err)
		}
		if _, err := s.ConsumeOAuthState(ctx, "h1", "github"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("第二次取用 = %v，預期 ErrNotFound", err)
		}
		u, _, _ := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		st2 := st
		st2.StateHash, st2.Intent, st2.UserID = "h2", hubstore.IntentLink, u.ID
		if err := s.CreateOAuthState(ctx, st2); err != nil {
			t.Fatal(err)
		}
		clk.Advance(10 * time.Minute)
		if _, err := s.ConsumeOAuthState(ctx, "h2", "github"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("過期後取用 = %v，預期 ErrNotFound", err)
		}
		st3 := st2
		st3.StateHash, st3.CreatedAt, st3.ExpiresAt = "h3", clk.Now(), clk.Now().Add(time.Minute)
		if err := s.CreateOAuthState(ctx, st3); err != nil {
			t.Fatal(err)
		}
		got, err = s.ConsumeOAuthState(ctx, "h3", "github")
		if err != nil || got.UserID != u.ID || got.Intent != hubstore.IntentLink {
			t.Fatalf("連結的 state = %+v, %v", got, err)
		}
	})

	t.Run("Sessions", func(t *testing.T) {
		s, clk := fresh(t, open)
		u, _, _ := s.SignIn(ctx, ident("github", "1", "alice"), "alice")
		se := hubstore.Session{ID: "s1", UserID: u.ID, TokenHash: "th", CreatedAt: clk.Now(), AuthenticatedAt: clk.Now(), ExpiresAt: clk.Now().Add(time.Hour)}
		if err := s.CreateSession(ctx, se); err != nil {
			t.Fatal(err)
		}
		got, err := s.SessionByHash(ctx, "th")
		if err != nil || got.UserID != u.ID || !got.AuthenticatedAt.Equal(clk.Now()) {
			t.Fatalf("SessionByHash = %+v, %v", got, err)
		}
		clk.Advance(30 * time.Minute)
		if err := s.TouchSession(ctx, "s1", clk.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		clk.Advance(45 * time.Minute)
		if _, err := s.SessionByHash(ctx, "th"); err != nil {
			t.Fatalf("延長後 = %v，預期仍有效", err)
		}
		clk.Advance(time.Hour)
		if _, err := s.SessionByHash(ctx, "th"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("過期後 = %v，預期 ErrNotFound", err)
		}
		se.ID, se.TokenHash, se.ExpiresAt = "s2", "th2", clk.Now().Add(time.Hour)
		if err := s.CreateSession(ctx, se); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteSession(ctx, "th2"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SessionByHash(ctx, "th2"); !errors.Is(err, hubstore.ErrNotFound) {
			t.Fatalf("刪除後 = %v，預期 ErrNotFound", err)
		}
		if err := s.DeleteSession(ctx, "missing"); err != nil {
			t.Fatalf("刪除不存在的 session = %v，預期不算錯誤", err)
		}
	})
}
