package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

func (rt *routes) registerTokens(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "tokens-list", Method: http.MethodGet, Path: "/api/tokens",
		Summary: "列出所有 API token 的中繼資料（不含明文與 hash，AUTH-R7）",
	}, rt.listTokens)
	huma.Register(api, huma.Operation{
		OperationID: "tokens-create", Method: http.MethodPost, Path: "/api/tokens",
		Summary:       "建立 API token，只在建立時回傳一次明文 token（AUTH-R7）",
		DefaultStatus: http.StatusCreated,
	}, rt.createToken)
	huma.Register(api, huma.Operation{
		OperationID: "tokens-revoke", Method: http.MethodDelete, Path: "/api/tokens/{id}",
		Summary: "撤銷 API token（依 id 或有效的名稱，AUTH-R9）",
	}, rt.revokeToken)
}

type listTokensOutput struct {
	Body struct {
		Tokens []auth.TokenInfo `json:"tokens"`
	}
}

type createTokenInput struct {
	Body struct {
		Name      string   `json:"name" doc:"token 名稱（有效的 token 中不可重複）"`
		Scopes    []string `json:"scopes" doc:"允許的 scope：read、agent、ingest、admin"`
		ExpiresIn string   `json:"expires_in,omitempty" doc:"有效期間，例如 30d、24h；省略代表永不到期"`
	}
}

type createTokenOutput struct {
	Body auth.CreatedToken
}

type tokenPathInput struct {
	ID string `path:"id" doc:"token ID 或有效的名稱"`
}

type revokeTokenOutput struct {
	Body struct {
		Revoked bool `json:"revoked"`
	}
}

func (rt *routes) listTokens(ctx context.Context, _ *struct{}) (*listTokensOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Auth == nil {
		return nil, rt.noAuth(ctx)
	}
	tokens, err := rt.d.Auth.ListTokens(ctx)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "列出 API token 失敗", "error", err)
		return nil, asError(err)
	}
	out := &listTokensOutput{}
	out.Body.Tokens = tokens
	return out, nil
}

func (rt *routes) createToken(ctx context.Context, in *createTokenInput) (*createTokenOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Auth == nil {
		return nil, rt.noAuth(ctx)
	}
	var expiresIn *time.Duration
	if in.Body.ExpiresIn != "" {
		d, err := parseTokenDuration(in.Body.ExpiresIn)
		if err != nil {
			return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error())
		}
		expiresIn = &d
	}
	created, err := rt.d.Auth.CreateToken(ctx, in.Body.Name, in.Body.Scopes, expiresIn)
	switch {
	case errors.Is(err, auth.ErrInvalidToken):
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, err.Error())
	case errors.Is(err, auth.ErrTokenExists):
		return nil, newError(http.StatusConflict, CodeTokenExists, err.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "建立 API token 失敗", "error", err)
		return nil, asError(err)
	}
	return &createTokenOutput{Body: created}, nil
}

func (rt *routes) revokeToken(ctx context.Context, in *tokenPathInput) (*revokeTokenOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Auth == nil {
		return nil, rt.noAuth(ctx)
	}
	err := rt.d.Auth.RevokeToken(ctx, in.ID)
	switch {
	case errors.Is(err, auth.ErrTokenNotFound):
		return nil, newError(http.StatusNotFound, CodeNotFound, err.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "撤銷 API token 失敗", "error", err)
		return nil, asError(err)
	}
	out := &revokeTokenOutput{}
	out.Body.Revoked = true
	return out, nil
}

func (rt *routes) noAuth(ctx context.Context) error {
	rt.d.Log.ErrorContext(ctx, "處理請求時發生錯誤", "error", "api: 沒有設定 Auth")
	return newError(http.StatusInternalServerError, CodeInternal, defaultMessages[http.StatusInternalServerError].msg)
}

// parseTokenDuration 解析 token 有效期間字串，支援 d（天 = 24h）及 Go 標準 duration（h、m、s）。
func parseTokenDuration(s string) (time.Duration, error) {
	if s == "" || strings.TrimSpace(s) != s {
		return 0, errors.New("token 有效期間格式不正確。")
	}
	if i := strings.IndexByte(s, 'd'); i >= 0 {
		days, err := strconv.ParseInt(s[:i], 10, 64)
		if err != nil || days <= 0 {
			return 0, errors.New("token 有效期間必須大於 0。")
		}
		if days > int64(math.MaxInt64/(24*time.Hour)) {
			return 0, errors.New("token 有效期間過長。")
		}
		total := time.Duration(days) * 24 * time.Hour
		if rest := s[i+1:]; rest != "" {
			sub, err := time.ParseDuration(rest)
			if err != nil || sub < 0 || total > math.MaxInt64-sub {
				return 0, errors.New("token 有效期間格式不正確。")
			}
			total += sub
		}
		return total, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("token 有效期間格式不正確。")
	}
	if d <= 0 {
		return 0, errors.New("token 有效期間必須大於 0。")
	}
	return d, nil
}
