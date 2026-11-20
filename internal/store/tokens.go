package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ErrConflict 表示寫入違反唯一性限制（例如已有同名的有效 API token）。
var ErrConflict = errors.New("conflict")

// APIToken 是 API bearer token 的中繼資料；token_hash 永遠不回傳給呼叫端。
type APIToken struct {
	ID         string
	Name       string
	Prefix     string
	Scopes     []string
	ExpiresAt  *time.Time
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// CreateAPIToken 建立一筆 API token；已有同名的有效 token 時回傳 ErrConflict。
// 已撤銷或已到期的 token 不佔用名稱。
func (s *Store) CreateAPIToken(ctx context.Context, name, prefix string, scopes []string, tokenHash string, expiresAt *time.Time) (APIToken, error) {
	if scopes == nil {
		scopes = []string{}
	}
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return APIToken{}, fmt.Errorf("序列化 scopes 失敗：%w", err)
	}
	id := NewID()
	now := s.clock.Now().UTC()
	var expCopy *time.Time
	if expiresAt != nil {
		t := expiresAt.UTC()
		expCopy = &t
	}
	// 同名限制只算有效（未撤銷、未到期）的 token：到期時間取決於現在，partial index 表達不了，
	// 所以在同一個寫入交易中檢查（只有一個寫入連線，交易之間不會交錯）。
	err = s.Tx(ctx, func(tx *Repo) error {
		n, err := tx.q.CountValidAPITokensByName(ctx, sqlcdb.CountValidAPITokensByNameParams{Name: name, Now: ptr(toMs(now))})
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: 已有同名的有效 token %q", ErrConflict, name)
		}
		return tx.q.CreateAPIToken(ctx, sqlcdb.CreateAPITokenParams{
			ID:        id,
			Name:      name,
			Prefix:    prefix,
			Scopes:    string(scopesJSON),
			TokenHash: tokenHash,
			ExpiresAt: msPtr(expCopy),
			CreatedAt: toMs(now),
		})
	})
	if err != nil {
		if isUniqueConstraint(err) {
			return APIToken{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return APIToken{}, err
	}
	return APIToken{
		ID:        id,
		Name:      name,
		Prefix:    prefix,
		Scopes:    append([]string(nil), scopes...),
		ExpiresAt: expCopy,
		CreatedAt: now,
	}, nil
}

// APITokenByHash 以 token hash 查詢尚未撤銷且尚未到期的 token；不存在、已撤銷或已到期時回傳 ErrNotFound。
func (s *Store) APITokenByHash(ctx context.Context, tokenHash string) (APIToken, error) {
	row, err := s.rq.APITokenByHash(ctx, sqlcdb.APITokenByHashParams{
		TokenHash: tokenHash,
		Now:       ptr(s.nowMs()),
	})
	if err != nil {
		return APIToken{}, notFound(err)
	}
	return apiTokenFromFields(row.ID, row.Name, row.Prefix, row.Scopes, row.ExpiresAt, row.CreatedAt, row.LastUsedAt, row.RevokedAt)
}

// TouchAPIToken 更新 token 的 last_used_at 為目前時鐘時間。
func (s *Store) TouchAPIToken(ctx context.Context, id string) error {
	return s.wq.TouchAPIToken(ctx, sqlcdb.TouchAPITokenParams{
		LastUsedAt: s.nowMs(),
		ID:         id,
	})
}

// ListAPITokens 列出所有 token（含已撤銷與已過期），依建立時間由新到舊排序，不含 token_hash。
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.rq.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]APIToken, 0, len(rows))
	for _, row := range rows {
		tok, err := apiTokenFromFields(row.ID, row.Name, row.Prefix, row.Scopes, row.ExpiresAt, row.CreatedAt, row.LastUsedAt, row.RevokedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, nil
}

// RevokeAPIToken 撤銷指定的 token（比對未撤銷的 id，或有效 token 的 name），回傳是否真的撤銷了一筆。
func (s *Store) RevokeAPIToken(ctx context.Context, idOrName string) (bool, error) {
	n, err := s.wq.RevokeAPIToken(ctx, sqlcdb.RevokeAPITokenParams{
		RevokedAt: s.nowMs(),
		IDOrName:  idOrName,
	})
	return n > 0, err
}

// ExpireAPITokenByName 把指定的有效 token 的到期時間設為 expiresAt（測試與驗收 harness 使用）。
func (s *Store) ExpireAPITokenByName(ctx context.Context, name string, expiresAt time.Time) (bool, error) {
	n, err := s.wq.ExpireAPITokenByName(ctx, sqlcdb.ExpireAPITokenByNameParams{
		ExpiresAt: toMs(expiresAt),
		Name:      name,
		Now:       ptr(s.nowMs()),
	})
	return n > 0, err
}

func apiTokenFromFields(id, name, prefix, scopesJSON string, expiresAt *int64, createdAt int64, lastUsedAt, revokedAt *int64) (APIToken, error) {
	var scopes []string
	if err := json.Unmarshal([]byte(scopesJSON), &scopes); err != nil {
		return APIToken{}, fmt.Errorf("解析 token scopes 失敗：%w", err)
	}
	if scopes == nil {
		scopes = []string{}
	}
	return APIToken{
		ID:         id,
		Name:       name,
		Prefix:     prefix,
		Scopes:     scopes,
		ExpiresAt:  msToTime(expiresAt),
		CreatedAt:  fromMs(createdAt),
		LastUsedAt: msToTime(lastUsedAt),
		RevokedAt:  msToTime(revokedAt),
	}, nil
}

func isUniqueConstraint(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}
