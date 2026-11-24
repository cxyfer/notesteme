package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres/sqlcdb"
)

// 帳號、身分、等待中的登入、session 與稽核紀錄（同 sqlite/auth.go）。

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 只在系統亂數來源失敗時出錯；沒有亂數就無法安全地繼續。
		panic(fmt.Errorf("hubstore/postgres: 產生 ID 失敗：%w", err))
	}
	return id.String()
}

// ts 把寫入的時間截到毫秒並轉成 UTC（SQLite 存 Unix 毫秒；兩種 backend 的比較與 cursor 要一致）。
func ts(t time.Time) time.Time { return time.UnixMilli(t.UnixMilli()).UTC() }

// utc 是讀回來的時間。
func utc(t time.Time) time.Time { return t.UTC() }

func optTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

func (s *Store) now() time.Time { return ts(s.clock.Now()) }

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return hubstore.ErrNotFound
	}
	return err
}

func toUser(u sqlcdb.User) hubstore.User {
	return hubstore.User{
		ID: u.ID, DisplayName: u.DisplayName, CreatedAt: utc(u.CreatedAt), SuspendedReason: u.SuspendedReason,
		SuspendedAt: optTime(u.SuspendedAt),
	}
}

func toIdentity(i sqlcdb.Identity) hubstore.Identity {
	var attrs hubstore.IdentityAttrs
	// attrs_json 只由這個套件寫入；解析失敗時當作沒有屬性（不影響登入，只影響自動規則）。
	_ = json.Unmarshal(i.AttrsJson, &attrs)
	return hubstore.Identity{
		ExternalIdentity: hubstore.ExternalIdentity{
			Provider: i.Provider, Subject: i.Subject, Login: i.Login, Email: i.Email,
			EmailVerified: i.EmailVerified, Attrs: attrs,
		},
		ID: i.ID, UserID: i.UserID, LinkedAt: utc(i.LinkedAt), LastLoginAt: utc(i.LastLoginAt),
	}
}

func refreshIdentity(ctx context.Context, q *sqlcdb.Queries, id string, in hubstore.ExternalIdentity, now time.Time) error {
	attrs, err := json.Marshal(in.Attrs)
	if err != nil {
		return err
	}
	return q.UpdateIdentityLogin(ctx, sqlcdb.UpdateIdentityLoginParams{
		Login: in.Login, Email: in.Email, EmailVerified: in.EmailVerified, AttrsJson: attrs, LastLoginAt: now, ID: id,
	})
}

func createIdentity(ctx context.Context, q *sqlcdb.Queries, userID string, in hubstore.ExternalIdentity, now time.Time) (string, error) {
	attrs, err := json.Marshal(in.Attrs)
	if err != nil {
		return "", err
	}
	id := newID()
	return id, q.CreateIdentity(ctx, sqlcdb.CreateIdentityParams{
		ID: id, UserID: userID, Provider: in.Provider, Subject: in.Subject, Login: in.Login, Email: in.Email,
		EmailVerified: in.EmailVerified, AttrsJson: attrs, LinkedAt: now, LastLoginAt: now,
	})
}

// lockUser 鎖住帳號的列；帳號不存在時回 ErrNotFound。
func lockUser(ctx context.Context, q *sqlcdb.Queries, userID string) error {
	_, err := q.LockUser(ctx, userID)
	return notFound(err)
}

// SignIn 實作 hubstore.Store。兩個並行的第一次登入：後提交的一方撞到 (provider, subject) 的唯一鍵，重試時登入既有帳號。
func (s *Store) SignIn(ctx context.Context, in hubstore.ExternalIdentity, displayName string) (hubstore.User, bool, error) {
	var (
		user    sqlcdb.User
		created bool
	)
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		user, created = sqlcdb.User{}, false
		now := s.now()
		ident, err := q.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: in.Provider, Subject: in.Subject})
		switch {
		case err == nil:
			if err := refreshIdentity(ctx, q, ident.ID, in, now); err != nil {
				return err
			}
			user, err = q.GetUser(ctx, ident.UserID)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
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
		if err := lockUser(ctx, q, userID); err != nil {
			return err
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
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if _, err := q.IdentityOfUser(ctx, sqlcdb.IdentityOfUserParams{UserID: userID, Provider: in.Provider}); err == nil {
			return hubstore.ErrProviderLinked
		} else if !errors.Is(err, pgx.ErrNoRows) {
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

// UnlinkIdentity 實作 hubstore.Store。先鎖帳號：兩個並行的移除不會讓帳號失去全部的登入方式。
func (s *Store) UnlinkIdentity(ctx context.Context, userID, provider, keepSessionID string) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if err := lockUser(ctx, q, userID); err != nil {
			return err
		}
		ident, err := q.IdentityOfUser(ctx, sqlcdb.IdentityOfUserParams{UserID: userID, Provider: provider})
		if err != nil {
			return notFound(err)
		}
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
		// 移除的登入方式可能是被盜用後連結的：其他 session 與所有 Hub token 一起失效（安全修補 A）。
		now := s.now()
		if err := q.DeleteOtherUserSessions(ctx, sqlcdb.DeleteOtherUserSessionsParams{UserID: userID, ID: keepSessionID}); err != nil {
			return err
		}
		if err := q.RevokeUserPATs(ctx, sqlcdb.RevokeUserPATsParams{RevokedAt: &now, UserID: userID}); err != nil {
			return err
		}
		return audit(ctx, q, userID, hubstore.AuditIdentityUnlink, "identity", ident.ID, map[string]string{"provider": provider}, now)
	})
}

// audit 寫入一筆稽核紀錄；actor 為 hubstore.CLIActorID 時是命令列（notesteme-hub admin）的動作，actor_user_id 存 NULL。
func audit(ctx context.Context, q *sqlcdb.Queries, actor, action, kind, target string, detail any, now time.Time) error {
	b, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	var actorID *string
	if actor != hubstore.CLIActorID {
		actorID = &actor
	}
	return q.CreateAuditEntry(ctx, sqlcdb.CreateAuditEntryParams{
		ID: newID(), ActorUserID: actorID, Action: action, TargetKind: kind, TargetID: target, DetailJson: b, At: now,
	})
}

// User 實作 hubstore.Store。
func (s *Store) User(ctx context.Context, id string) (hubstore.User, error) {
	u, err := s.q.GetUser(ctx, id)
	if err != nil {
		return hubstore.User{}, notFound(err)
	}
	return toUser(u), nil
}

// Identities 實作 hubstore.Store。
func (s *Store) Identities(ctx context.Context, userID string) ([]hubstore.Identity, error) {
	rows, err := s.q.ListIdentities(ctx, userID)
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
			CreatedAt: ts(st.CreatedAt), ExpiresAt: ts(st.ExpiresAt),
		})
	})
}

// ConsumeOAuthState 實作 hubstore.Store。單一的條件式 UPDATE：並行的取用只有一個會成功。
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash, provider string) (hubstore.OAuthState, error) {
	now := s.now()
	r, err := s.q.ConsumeOAuthState(ctx, sqlcdb.ConsumeOAuthStateParams{Now: &now, StateHash: stateHash, Provider: provider})
	if err != nil {
		return hubstore.OAuthState{}, notFound(err)
	}
	out := hubstore.OAuthState{
		StateHash: r.StateHash, Provider: r.Provider, Intent: r.Intent, VerifierNonce: r.VerifierNonce,
		VerifierSealed: r.VerifierSealed, Nonce: r.Nonce, CreatedAt: utc(r.CreatedAt), ExpiresAt: utc(r.ExpiresAt),
	}
	if r.UserID != nil {
		out.UserID = *r.UserID
	}
	return out, nil
}

// CreateSession 實作 hubstore.Store。
func (s *Store) CreateSession(ctx context.Context, se hubstore.Session) error {
	return s.q.CreateSession(ctx, sqlcdb.CreateSessionParams{
		ID: se.ID, UserID: se.UserID, TokenHash: se.TokenHash, CreatedAt: ts(se.CreatedAt),
		AuthenticatedAt: ts(se.AuthenticatedAt), ExpiresAt: ts(se.ExpiresAt),
	})
}

// SessionByHash 實作 hubstore.Store。
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (hubstore.Session, error) {
	r, err := s.q.SessionByHash(ctx, sqlcdb.SessionByHashParams{TokenHash: tokenHash, Now: s.now()})
	if err != nil {
		return hubstore.Session{}, notFound(err)
	}
	return hubstore.Session{
		ID: r.ID, UserID: r.UserID, TokenHash: r.TokenHash, CreatedAt: utc(r.CreatedAt),
		AuthenticatedAt: utc(r.AuthenticatedAt), ExpiresAt: utc(r.ExpiresAt),
	}, nil
}

// TouchSession 實作 hubstore.Store。
func (s *Store) TouchSession(ctx context.Context, id string, expiresAt time.Time) error {
	return s.q.TouchSession(ctx, sqlcdb.TouchSessionParams{ExpiresAt: ts(expiresAt), ID: id})
}

// DeleteSession 實作 hubstore.Store。
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	return s.q.DeleteSessionByHash(ctx, tokenHash)
}

// AuditEntries 實作 hubstore.Store。
func (s *Store) AuditEntries(ctx context.Context, limit int) ([]hubstore.AuditEntry, error) {
	rows, err := s.q.ListAuditEntries(ctx, int32(clampLimit(limit)))
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.AuditEntry, 0, len(rows))
	for _, r := range rows {
		e := hubstore.AuditEntry{
			ID: r.ID, Actor: hubstore.CLIActor, Action: r.Action, TargetKind: r.TargetKind, TargetID: r.TargetID, At: utc(r.At),
		}
		if r.ActorUserID != nil {
			e.ActorUserID = *r.ActorUserID
			e.Actor = ""
			if r.Actor != nil {
				e.Actor = *r.Actor
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// clampLimit 把 limit 限制在 int32 的範圍內（LIMIT 是 integer；負數同 SQLite 的「不限」）。
func clampLimit(limit int) int {
	const maxLimit = 1<<31 - 1
	if limit < 0 || limit > maxLimit {
		return maxLimit
	}
	return limit
}
