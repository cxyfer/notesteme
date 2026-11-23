package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite/sqlcdb"
)

// 帳號、身分、等待中的登入、session 與稽核紀錄（M5-S1，HACC-R2～R8、R17）。

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 只在系統亂數來源失敗時出錯；沒有亂數就無法安全地繼續。
		panic(fmt.Errorf("hubstore/sqlite: 產生 ID 失敗：%w", err))
	}
	return id.String()
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func (s *Store) now() int64 { return ms(s.clock.Now()) }

// inTx 在寫入連線上執行 fn；fn 回傳錯誤或 panic 時 rollback。
func (s *Store) inTx(ctx context.Context, fn func(q *sqlcdb.Queries) error) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if v := recover(); v != nil {
			_ = tx.Rollback()
			panic(v)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(s.wq.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return hubstore.ErrNotFound
	}
	return err
}

func toUser(u sqlcdb.User) hubstore.User {
	out := hubstore.User{ID: u.ID, DisplayName: u.DisplayName, CreatedAt: fromMS(u.CreatedAt), SuspendedReason: u.SuspendedReason}
	if u.SuspendedAt != nil {
		t := fromMS(*u.SuspendedAt)
		out.SuspendedAt = &t
	}
	return out
}

func toIdentity(i sqlcdb.Identity) hubstore.Identity {
	var attrs hubstore.IdentityAttrs
	// attrs_json 只由這個套件寫入；解析失敗時當作沒有屬性（不影響登入，只影響自動規則）。
	_ = json.Unmarshal([]byte(i.AttrsJson), &attrs)
	return hubstore.Identity{
		ExternalIdentity: hubstore.ExternalIdentity{
			Provider: i.Provider, Subject: i.Subject, Login: i.Login, Email: i.Email,
			EmailVerified: i.EmailVerified != 0, Attrs: attrs,
		},
		ID: i.ID, UserID: i.UserID, LinkedAt: fromMS(i.LinkedAt), LastLoginAt: fromMS(i.LastLoginAt),
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func attrsJSON(a hubstore.IdentityAttrs) (string, error) {
	b, err := json.Marshal(a)
	return string(b), err
}

// refreshIdentity 更新既有身分在供應商端的屬性。
func refreshIdentity(ctx context.Context, q *sqlcdb.Queries, id string, in hubstore.ExternalIdentity, now int64) error {
	attrs, err := attrsJSON(in.Attrs)
	if err != nil {
		return err
	}
	return q.UpdateIdentityLogin(ctx, sqlcdb.UpdateIdentityLoginParams{
		Login: in.Login, Email: in.Email, EmailVerified: boolInt(in.EmailVerified), AttrsJson: attrs, LastLoginAt: now, ID: id,
	})
}

func createIdentity(ctx context.Context, q *sqlcdb.Queries, userID string, in hubstore.ExternalIdentity, now int64) (string, error) {
	attrs, err := attrsJSON(in.Attrs)
	if err != nil {
		return "", err
	}
	id := newID()
	return id, q.CreateIdentity(ctx, sqlcdb.CreateIdentityParams{
		ID: id, UserID: userID, Provider: in.Provider, Subject: in.Subject, Login: in.Login, Email: in.Email,
		EmailVerified: boolInt(in.EmailVerified), AttrsJson: attrs, LinkedAt: now, LastLoginAt: now,
	})
}

// SignIn 實作 hubstore.Store。
func (s *Store) SignIn(ctx context.Context, in hubstore.ExternalIdentity, displayName string) (hubstore.User, bool, error) {
	var (
		user    sqlcdb.User
		created bool
	)
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		ident, err := q.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: in.Provider, Subject: in.Subject})
		switch {
		case err == nil:
			if err := refreshIdentity(ctx, q, ident.ID, in, now); err != nil {
				return err
			}
			user, err = q.GetUser(ctx, ident.UserID)
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		// 第一次以這個身分登入：建立新帳號（不依 email 合併，HACC-R5）。
		user = sqlcdb.User{ID: newID(), DisplayName: displayName, CreatedAt: now}
		if err := q.CreateUser(ctx, sqlcdb.CreateUserParams{ID: user.ID, DisplayName: displayName, CreatedAt: now}); err != nil {
			return err
		}
		created = true
		_, err = createIdentity(ctx, q, user.ID, in, now)
		return err
	})
	if err != nil {
		return hubstore.User{}, false, err
	}
	return toUser(user), created, nil
}

// LinkIdentity 實作 hubstore.Store。
func (s *Store) LinkIdentity(ctx context.Context, userID string, in hubstore.ExternalIdentity) (hubstore.Identity, error) {
	var out sqlcdb.Identity
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		now := s.now()
		if _, err := q.GetUser(ctx, userID); err != nil {
			return notFound(err)
		}
		ident, err := q.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: in.Provider, Subject: in.Subject})
		switch {
		case err == nil && ident.UserID != userID:
			return hubstore.ErrIdentityInUse
		case err == nil:
			// 已經連結在這個帳號：只更新屬性。
			if err := refreshIdentity(ctx, q, ident.ID, in, now); err != nil {
				return err
			}
			out, err = q.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: in.Provider, Subject: in.Subject})
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := q.IdentityOfUser(ctx, sqlcdb.IdentityOfUserParams{UserID: userID, Provider: in.Provider}); err == nil {
			return hubstore.ErrProviderLinked
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		id, err := createIdentity(ctx, q, userID, in, now)
		if err != nil {
			return err
		}
		if err := audit(ctx, q, userID, hubstore.AuditIdentityLink, "identity", id, map[string]string{"provider": in.Provider}, now); err != nil {
			return err
		}
		out, err = q.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: in.Provider, Subject: in.Subject})
		return err
	})
	if err != nil {
		return hubstore.Identity{}, err
	}
	return toIdentity(out), nil
}

// UnlinkIdentity 實作 hubstore.Store。
func (s *Store) UnlinkIdentity(ctx context.Context, userID, provider string) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		ident, err := q.IdentityOfUser(ctx, sqlcdb.IdentityOfUserParams{UserID: userID, Provider: provider})
		if err != nil {
			return notFound(err)
		}
		// 計數與刪除在同一個 transaction：兩個並行的移除不會讓帳號失去全部的登入方式。
		n, err := q.CountIdentities(ctx, userID)
		if err != nil {
			return err
		}
		if n <= 1 {
			return hubstore.ErrLastIdentity
		}
		if err := q.DeleteIdentity(ctx, ident.ID); err != nil {
			return err
		}
		return audit(ctx, q, userID, hubstore.AuditIdentityUnlink, "identity", ident.ID, map[string]string{"provider": provider}, s.now())
	})
}

// audit 寫入一筆稽核紀錄；actor 為 hubstore.CLIActorID 時是命令列（notesteme-hub admin）的動作，actor_user_id 存 NULL。
func audit(ctx context.Context, q *sqlcdb.Queries, actor, action, kind, target string, detail any, now int64) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	var actorID *string
	if actor != hubstore.CLIActorID {
		actorID = &actor
	}
	return q.CreateAuditEntry(ctx, sqlcdb.CreateAuditEntryParams{
		ID: newID(), ActorUserID: actorID, Action: action, TargetKind: kind, TargetID: target, DetailJson: string(b), At: now,
	})
}

// User 實作 hubstore.Store。
func (s *Store) User(ctx context.Context, id string) (hubstore.User, error) {
	u, err := s.rq.GetUser(ctx, id)
	if err != nil {
		return hubstore.User{}, notFound(err)
	}
	return toUser(u), nil
}

// Identities 實作 hubstore.Store。
func (s *Store) Identities(ctx context.Context, userID string) ([]hubstore.Identity, error) {
	rows, err := s.rq.ListIdentities(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.Identity, 0, len(rows))
	for _, r := range rows {
		out = append(out, toIdentity(r))
	}
	return out, nil
}

// CreateOAuthState 實作 hubstore.Store。
func (s *Store) CreateOAuthState(ctx context.Context, st hubstore.OAuthState) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		// 每次寫入順便清掉過期的紀錄，等待中的登入不會無限累積。
		if err := q.DeleteExpiredOAuthStates(ctx, s.now()); err != nil {
			return err
		}
		var user *string
		if st.UserID != "" {
			user = &st.UserID
		}
		return q.CreateOAuthState(ctx, sqlcdb.CreateOAuthStateParams{
			StateHash: st.StateHash, Provider: st.Provider, Intent: st.Intent, UserID: user,
			VerifierNonce: st.VerifierNonce, VerifierSealed: st.VerifierSealed, Nonce: st.Nonce,
			CreatedAt: ms(st.CreatedAt), ExpiresAt: ms(st.ExpiresAt),
		})
	})
}

// ConsumeOAuthState 實作 hubstore.Store。
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash, provider string) (hubstore.OAuthState, error) {
	now := s.now()
	r, err := s.wq.ConsumeOAuthState(ctx, sqlcdb.ConsumeOAuthStateParams{Now: &now, StateHash: stateHash, Provider: provider})
	if err != nil {
		return hubstore.OAuthState{}, notFound(err)
	}
	out := hubstore.OAuthState{
		StateHash: r.StateHash, Provider: r.Provider, Intent: r.Intent, VerifierNonce: r.VerifierNonce,
		VerifierSealed: r.VerifierSealed, Nonce: r.Nonce, CreatedAt: fromMS(r.CreatedAt), ExpiresAt: fromMS(r.ExpiresAt),
	}
	if r.UserID != nil {
		out.UserID = *r.UserID
	}
	return out, nil
}

// CreateSession 實作 hubstore.Store。
func (s *Store) CreateSession(ctx context.Context, se hubstore.Session) error {
	return s.wq.CreateSession(ctx, sqlcdb.CreateSessionParams{
		ID: se.ID, UserID: se.UserID, TokenHash: se.TokenHash, CreatedAt: ms(se.CreatedAt),
		AuthenticatedAt: ms(se.AuthenticatedAt), ExpiresAt: ms(se.ExpiresAt),
	})
}

// SessionByHash 實作 hubstore.Store。
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (hubstore.Session, error) {
	r, err := s.rq.SessionByHash(ctx, sqlcdb.SessionByHashParams{TokenHash: tokenHash, Now: s.now()})
	if err != nil {
		return hubstore.Session{}, notFound(err)
	}
	return hubstore.Session{
		ID: r.ID, UserID: r.UserID, TokenHash: r.TokenHash, CreatedAt: fromMS(r.CreatedAt),
		AuthenticatedAt: fromMS(r.AuthenticatedAt), ExpiresAt: fromMS(r.ExpiresAt),
	}, nil
}

// TouchSession 實作 hubstore.Store。
func (s *Store) TouchSession(ctx context.Context, id string, expiresAt time.Time) error {
	return s.wq.TouchSession(ctx, sqlcdb.TouchSessionParams{ExpiresAt: ms(expiresAt), ID: id})
}

// DeleteSession 實作 hubstore.Store。
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	return s.wq.DeleteSessionByHash(ctx, tokenHash)
}

// AuditEntries 實作 hubstore.Store。
func (s *Store) AuditEntries(ctx context.Context, limit int) ([]hubstore.AuditEntry, error) {
	rows, err := s.rq.ListAuditEntries(ctx, int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.AuditEntry, 0, len(rows))
	for _, r := range rows {
		e := hubstore.AuditEntry{Actor: hubstore.CLIActor}
		if r.ActorUserID != nil {
			e.ActorUserID = *r.ActorUserID
			e.Actor = ""
			if r.Actor != nil {
				e.Actor = *r.Actor
			}
		}
		out = append(out, hubstore.AuditEntry{
			ID: r.ID, ActorUserID: e.ActorUserID, Actor: e.Actor, Action: r.Action,
			TargetKind: r.TargetKind, TargetID: r.TargetID, At: fromMS(r.At),
		})
	}
	return out, nil
}
