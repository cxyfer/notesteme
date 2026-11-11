package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
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
	_, err = s.w.ExecContext(ctx,
		`INSERT INTO sessions (id, token_hash, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.TokenHash, toMs(sess.CreatedAt), toMs(sess.ExpiresAt), ip, userAgent)
	return sess, err
}

// SessionByHash 以 token hash 查詢尚未到期的 session；不存在或已到期時回傳 ErrNotFound。
func (s *Store) SessionByHash(ctx context.Context, tokenHash string) (Session, error) {
	var (
		sess             Session
		createdMs, expMs int64
	)
	err := s.r.QueryRowContext(ctx,
		`SELECT id, token_hash, created_at, expires_at, ip, user_agent
		   FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, s.nowMs()).
		Scan(&sess.ID, &sess.TokenHash, &createdMs, &expMs, &sess.IP, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	sess.CreatedAt, sess.ExpiresAt = fromMs(createdMs), fromMs(expMs)
	return sess, nil
}

// ExtendSession 把 session 的到期時間設為 expiresAt。
func (s *Store) ExtendSession(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.w.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`, toMs(expiresAt), id)
	return err
}

// DeleteSessionByHash 刪除 session，立即失效；不存在時不算錯誤。
func (s *Store) DeleteSessionByHash(ctx context.Context, tokenHash string) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteExpiredSessions 清除已到期的 session，回傳刪除的筆數。
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.w.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, s.nowMs())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
