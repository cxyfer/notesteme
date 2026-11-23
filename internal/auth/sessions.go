package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// ErrSessionNotFound 表示找不到指定的 session（AUTH-R20）。
var ErrSessionNotFound = errors.New("找不到指定的工作階段。")

// ErrTokenInactive 表示要輪替的 token 已撤銷或過期（AUTH-R21）。
var ErrTokenInactive = errors.New("這個 token 已撤銷或過期，請建立新的 token。")

// SessionInfo 是工作階段列表的一筆（AUTH-R20）；不含 session 的 secret 或 hash。
type SessionInfo struct {
	ID         string    `json:"id"`
	Method     string    `json:"method" enum:"password,proxy,oidc"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
}

// ListSessions 列出尚未到期的 session，依 last_seen_at 由新到舊；currentID 那一筆標為 current。
func (s *Service) ListSessions(ctx context.Context, currentID string) ([]SessionInfo, error) {
	rows, err := s.store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, SessionInfo{
			ID: r.ID, Method: r.Method, CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt,
			IP: r.IP, UserAgent: r.UserAgent, Current: currentID != "" && r.ID == currentID,
		})
	}
	return out, nil
}

// RevokeSession 撤銷指定的 session，之後用它的請求立即回 401（AUTH-R20）；找不到時回傳 ErrSessionNotFound。
func (s *Service) RevokeSession(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrSessionNotFound
	}
	ok, err := s.store.DeleteSessionByID(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSessionNotFound
	}
	s.log.InfoContext(ctx, "撤銷工作階段", "session_id", id)
	return nil
}

// RotateToken 產生新的 secret（AUTH-R21）：名稱、scope、到期時間不變，舊的 secret 在同一個交易中失效。
// 已撤銷或過期的 token 回傳 ErrTokenInactive，找不到時回傳 ErrTokenNotFound。
func (s *Service) RotateToken(ctx context.Context, idOrName string) (CreatedToken, error) {
	if strings.TrimSpace(idOrName) == "" {
		return CreatedToken{}, ErrTokenNotFound
	}
	raw, err := randomBearerToken()
	if err != nil {
		return CreatedToken{}, err
	}
	tok, err := s.store.RotateAPIToken(ctx, idOrName, hashToken(raw), raw[:tokenPrefixLen])
	switch {
	case errors.Is(err, store.ErrNotFound):
		return CreatedToken{}, ErrTokenNotFound
	case errors.Is(err, store.ErrTokenInactive):
		return CreatedToken{}, ErrTokenInactive
	case err != nil:
		return CreatedToken{}, err
	}
	s.log.InfoContext(ctx, "輪替 API token", "token_id", tok.ID, "name", tok.Name)
	return CreatedToken{TokenInfo: tokenInfoFromStore(tok), Token: raw}, nil
}
