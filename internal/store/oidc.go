package store

import (
	"context"
	"errors"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ErrTooMany 表示待完成的 OIDC 登入已經達到上限（AUTH-R17 的 start 是免驗證端點，必須限制寫入量）。
var ErrTooMany = errors.New("too many pending rows")

// OIDCLogin 是一次尚未完成的 OIDC 登入（AUTH-R17）。state 與 notesteme_oidc cookie 只存 hash。
type OIDCLogin struct {
	StateHash    string
	CookieHash   string
	Nonce        string
	CodeVerifier string
	Next         string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// CreateOIDCLogin 先清掉已到期的登入，再在 pending 數量低於 limit 時寫入一筆；達到上限時回傳 ErrTooMany。
func (s *Store) CreateOIDCLogin(ctx context.Context, l OIDCLogin, limit int64) error {
	return s.Tx(ctx, func(r *Repo) error {
		now := r.nowMs()
		if _, err := r.q.DeleteExpiredOIDCLogins(ctx, now); err != nil {
			return err
		}
		n, err := r.q.CountOIDCLogins(ctx)
		if err != nil {
			return err
		}
		if n >= limit {
			return ErrTooMany
		}
		return r.q.CreateOIDCLogin(ctx, sqlcdb.CreateOIDCLoginParams{
			StateHash: l.StateHash, CookieHash: l.CookieHash, Nonce: l.Nonce, CodeVerifier: l.CodeVerifier,
			Next: l.Next, CreatedAt: toMs(l.CreatedAt), ExpiresAt: toMs(l.ExpiresAt),
		})
	})
}

// TakeOIDCLogin 取出並刪除 state hash 對應的登入（只能用一次）；不存在時回傳 ErrNotFound。
// 到期與 cookie 的比對由呼叫端在取出後檢查，取出本身就讓這個 state 失效。
func (s *Store) TakeOIDCLogin(ctx context.Context, stateHash string) (OIDCLogin, error) {
	row, err := s.wq.TakeOIDCLogin(ctx, stateHash)
	if err != nil {
		return OIDCLogin{}, notFound(err)
	}
	return OIDCLogin{
		StateHash: row.StateHash, CookieHash: row.CookieHash, Nonce: row.Nonce, CodeVerifier: row.CodeVerifier,
		Next: row.Next, CreatedAt: fromMs(row.CreatedAt), ExpiresAt: fromMs(row.ExpiresAt),
	}, nil
}

// RecordOIDCIdentity 記錄 (issuer, subject) 最後一次登入的時間（AUTH-R19）。
func (s *Store) RecordOIDCIdentity(ctx context.Context, issuer, subject string) error {
	return s.wq.UpsertOIDCIdentity(ctx, sqlcdb.UpsertOIDCIdentityParams{Issuer: issuer, Subject: subject, LastLoginAt: s.nowMs()})
}
