package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Session 是一個登入 session；token 只以 hash 存放。
type Session struct {
	ID        string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// CreateSession 建立 session，到期時間為現在加上 ttl。
func (s *Store) CreateSession(ctx context.Context, tokenHash string, ttl time.Duration, ip, userAgent string) (Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	sess := Session{
		ID:        id.String(),
		TokenHash: tokenHash,
		CreatedAt: now.UTC(),
		ExpiresAt: now.Add(ttl).UTC(),
		IP:        ip,
		UserAgent: userAgent,
	}
	err = s.wq.CreateSession(ctx, sqlcdb.CreateSessionParams{
		ID:        sess.ID,
		TokenHash: sess.TokenHash,
		CreatedAt: toMs(sess.CreatedAt),
		ExpiresAt: toMs(sess.ExpiresAt),
		Ip:        ip,
		UserAgent: userAgent,
	})
	return sess, err
}

// SessionByHash 以 token hash 查詢尚未到期的 session；不存在或已到期時回傳 ErrNotFound。
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (Session, error) {
	row, err := s.rq.SessionByHash(ctx, sqlcdb.SessionByHashParams{TokenHash: tokenHash, Now: s.nowMs()})
	if err != nil {
		return Session{}, notFound(err)
	}
	return Session{
		ID:        row.ID,
		TokenHash: row.TokenHash,
		CreatedAt: fromMs(row.CreatedAt),
		ExpiresAt: fromMs(row.ExpiresAt),
		IP:        row.Ip,
		UserAgent: row.UserAgent,
	}, nil
}

// ExtendSession 把 session 的到期時間設為 expiresAt。
func (s *Store) ExtendSession(ctx context.Context, id string, expiresAt time.Time) error {
	return s.wq.ExtendSession(ctx, sqlcdb.ExtendSessionParams{ExpiresAt: toMs(expiresAt), ID: id})
}

// DeleteSessionByHash 刪除 session，立即失效；不存在時不算錯誤。
func (s *Store) DeleteSessionByHash(ctx context.Context, tokenHash string) error {
	return s.wq.DeleteSessionByHash(ctx, tokenHash)
}

// DeleteExpiredSessions 清除已到期的 session，回傳刪除的筆數。
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	return s.wq.DeleteExpiredSessions(ctx, s.nowMs())
}
