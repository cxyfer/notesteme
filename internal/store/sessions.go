package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Session 的登入方式（AUTH-R14、R19、R20）。
const (
	SessionPassword = "password"
	SessionProxy    = "proxy"
	SessionOIDC     = "oidc"
)

// Session 是一個登入 session；token 只以 hash 存放。
type Session struct {
	ID        string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
	// Method 是 password、proxy 或 oidc；ProxyUser 是 proxy session 綁定的標頭值（AUTH-R14）。
	Method     string
	ProxyUser  string
	LastSeenAt time.Time
}

// NewSession 是建立 session 的參數。
type NewSession struct {
	TokenHash string
	TTL       time.Duration
	IP        string
	UserAgent string
	// Method 空字串視為 password。
	Method    string
	ProxyUser string
}

// CreateSession 建立密碼登入的 session，到期時間為現在加上 ttl。
func (s *Store) CreateSession(ctx context.Context, tokenHash string, ttl time.Duration, ip, userAgent string) (Session, error) {
	return s.CreateSessionWith(ctx, NewSession{TokenHash: tokenHash, TTL: ttl, IP: ip, UserAgent: userAgent})
}

// CreateSessionWith 建立 session；last_seen_at 等於建立時間。
func (s *Store) CreateSessionWith(ctx context.Context, n NewSession) (Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Session{}, err
	}
	method := n.Method
	if method == "" {
		method = SessionPassword
	}
	now := s.clock.Now().UTC()
	sess := Session{
		ID:         id.String(),
		TokenHash:  n.TokenHash,
		CreatedAt:  now,
		ExpiresAt:  now.Add(n.TTL).UTC(),
		IP:         n.IP,
		UserAgent:  n.UserAgent,
		Method:     method,
		ProxyUser:  n.ProxyUser,
		LastSeenAt: now,
	}
	err = s.wq.CreateSession(ctx, sqlcdb.CreateSessionParams{
		ID:        sess.ID,
		TokenHash: sess.TokenHash,
		CreatedAt: toMs(sess.CreatedAt),
		ExpiresAt: toMs(sess.ExpiresAt),
		Ip:        n.IP,
		UserAgent: n.UserAgent,
		Method:    method,
		ProxyUser: n.ProxyUser,
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
		ID:         row.ID,
		TokenHash:  row.TokenHash,
		CreatedAt:  fromMs(row.CreatedAt),
		ExpiresAt:  fromMs(row.ExpiresAt),
		IP:         row.Ip,
		UserAgent:  row.UserAgent,
		Method:     row.Method,
		ProxyUser:  row.ProxyUser,
		LastSeenAt: fromMs(row.LastSeenAt),
	}, nil
}

// TouchSession 同時更新 last_seen_at 與閒置到期時間（AUTH-R4、R20）；節流由呼叫端負責。
func (s *Store) TouchSession(ctx context.Context, id string, lastSeen, expiresAt time.Time) error {
	return s.wq.TouchSession(ctx, sqlcdb.TouchSessionParams{LastSeenAt: toMs(lastSeen), ExpiresAt: toMs(expiresAt), ID: id})
}

// DeleteSessionByID 刪除 session，回傳是否真的刪除了一筆。
func (s *Store) DeleteSessionByID(ctx context.Context, id string) (bool, error) {
	n, err := s.wq.DeleteSessionByID(ctx, id)
	return n > 0, err
}

// ListSessions 列出尚未到期的 session，依 last_seen_at 由新到舊；不含 token hash。
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.rq.ListSessions(ctx, s.nowMs())
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{
			ID: r.ID, Method: r.Method, CreatedAt: fromMs(r.CreatedAt), LastSeenAt: fromMs(r.LastSeenAt),
			IP: r.Ip, UserAgent: r.UserAgent,
		})
	}
	return out, nil
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

// ReplacePasswordHash 在同一個交易中寫入新的密碼 hash（settings 的 key），並刪除 token hash 不是 keepTokenHash 的
// 所有 session（修改密碼後其他裝置立即登出）；keepTokenHash 是空字串時刪除全部。回傳刪除的 session 數。
func (s *Store) ReplacePasswordHash(ctx context.Context, key, hash, keepTokenHash string) (int64, error) {
	var n int64
	err := s.Tx(ctx, func(r *Repo) error {
		if err := r.q.SetSetting(ctx, sqlcdb.SetSettingParams{Key: key, Value: hash, UpdatedAt: s.nowMs()}); err != nil {
			return err
		}
		var err error
		n, err = r.q.DeleteSessionsExcept(ctx, keepTokenHash)
		return err
	})
	return n, err
}
