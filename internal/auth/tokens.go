package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// API token 的格式與範圍常數（AUTH-R7～R9）。
const (
	TokenPrefix    = "nst_"
	tokenPrefixLen = 8
	// tokenTouchInterval 是更新 API token last_used_at 的最短間隔，避免每個請求都寫 DB。
	tokenTouchInterval = time.Hour

	ScopeRead   = "read"
	ScopeAgent  = "agent"
	ScopeIngest = "ingest"
	ScopeAdmin  = "admin"
)

var (
	// ErrTokenExists 表示已有同名的有效 token。
	ErrTokenExists = errors.New("已經有同名的有效 token。")
	// ErrTokenNotFound 表示找不到指定或仍有效的 token。
	ErrTokenNotFound = errors.New("找不到指定的 token。")
	// ErrInvalidToken 表示建立 token 的參數（名稱、scope 或到期時間）不合法。
	ErrInvalidToken = errors.New("token 參數不正確。")
)

// ValidScope 回報 s 是否為支援的 token scope。
func ValidScope(s string) bool {
	return s == ScopeRead || s == ScopeAgent || s == ScopeIngest || s == ScopeAdmin
}

// HasScope 回報身分是否具備指定 scope（AUTH-R8）。
// 非 token 身分（session／local）擁有全部權限；token 只比對明確列出的 scope，不隱含繼承。
func (p Principal) HasScope(scope string) bool {
	if p.Kind != "token" {
		return true
	}
	return slices.Contains(p.Scopes, scope)
}

// TokenInfo 是 API token 的中繼資料，永遠不含明文與 hash。
type TokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreatedToken 是建立 token 成功時的一次性回傳值；Token 只出現這一次（AUTH-R7）。
type CreatedToken struct {
	TokenInfo
	Token string `json:"token"`
}

// CreateToken 建立一個 API bearer token（AUTH-R7）。
// name 不可為空或含控制字元；scopes 必須非空且皆為有效 scope；expiresIn 若不為 nil 必須大於 0。
func (s *Service) CreateToken(ctx context.Context, name string, scopes []string, expiresIn *time.Duration) (CreatedToken, error) {
	if strings.TrimSpace(name) == "" || hasControlChar(name) {
		return CreatedToken{}, fmt.Errorf("%w: token 名稱不可為空或包含控制字元。", ErrInvalidToken)
	}
	cleanScopes, err := validateScopes(scopes)
	if err != nil {
		return CreatedToken{}, err
	}
	var expiresAt *time.Time
	if expiresIn != nil {
		if *expiresIn <= 0 {
			return CreatedToken{}, fmt.Errorf("%w: 到期時間必須大於 0。", ErrInvalidToken)
		}
		t := s.clock.Now().Add(*expiresIn).UTC()
		expiresAt = &t
	}

	raw, err := randomBearerToken()
	if err != nil {
		return CreatedToken{}, err
	}
	prefix := raw[:tokenPrefixLen]
	tok, err := s.store.CreateAPIToken(ctx, name, prefix, cleanScopes, hashToken(raw), expiresAt)
	if errors.Is(err, store.ErrConflict) {
		return CreatedToken{}, ErrTokenExists
	}
	if err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{
		TokenInfo: tokenInfoFromStore(tok),
		Token:     raw,
	}, nil
}

// AuthenticateBearer 驗證 bearer token，檢查前綴、撤銷與到期狀態，並節流更新 last_used_at（AUTH-R7～R9）。
func (s *Service) AuthenticateBearer(ctx context.Context, token string) (Principal, error) {
	if !strings.HasPrefix(token, TokenPrefix) || len(token) <= len(TokenPrefix) {
		return Principal{}, ErrUnauthenticated
	}
	tok, err := s.store.APITokenByHash(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	now := s.clock.Now()
	if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) >= tokenTouchInterval {
		if s.touchToken != nil {
			// 更新使用時間失敗不影響這次請求的驗證結果。
			if err := s.touchToken(ctx, tok.ID); err != nil {
				s.log.WarnContext(ctx, "更新 token 使用時間失敗", "error", err)
			}
		}
	}
	return Principal{Kind: "token", TokenID: tok.ID, Scopes: tok.Scopes}, nil
}

// ListTokens 列出所有 API token 的中繼資料，由新到舊排序，不含明文或 hash。
func (s *Service) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	rows, err := s.store.ListAPITokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TokenInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenInfoFromStore(r))
	}
	return out, nil
}

// RevokeToken 依 id 或有效的名稱撤銷 token（AUTH-R9）；找不到有效 token 時回傳 ErrTokenNotFound。
func (s *Service) RevokeToken(ctx context.Context, idOrName string) error {
	if strings.TrimSpace(idOrName) == "" {
		return ErrTokenNotFound
	}
	ok, err := s.store.RevokeAPIToken(ctx, idOrName)
	if err != nil {
		return err
	}
	if !ok {
		return ErrTokenNotFound
	}
	return nil
}

// ExpireTokenByName 把指定名稱的有效 token 設為在目前時鐘時間到期（測試與驗收 harness 使用）。
func (s *Service) ExpireTokenByName(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrTokenNotFound
	}
	ok, err := s.store.ExpireAPITokenByName(ctx, name, s.clock.Now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrTokenNotFound
	}
	return nil
}

func randomBearerToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func validateScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: 至少需要指定一個 scope。", ErrInvalidToken)
	}
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		if !ValidScope(sc) {
			return nil, fmt.Errorf("%w: 不支援的 scope %q。", ErrInvalidToken, sc)
		}
		if _, ok := seen[sc]; ok {
			continue
		}
		seen[sc] = struct{}{}
		out = append(out, sc)
	}
	return out, nil
}

func hasControlChar(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func tokenInfoFromStore(t store.APIToken) TokenInfo {
	return TokenInfo{
		ID:         t.ID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		Scopes:     append([]string(nil), t.Scopes...),
		ExpiresAt:  t.ExpiresAt,
		CreatedAt:  t.CreatedAt,
		LastUsedAt: t.LastUsedAt,
		RevokedAt:  t.RevokedAt,
	}
}
