package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

func (rt *routes) registerTokens(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "tokens-list", Method: http.MethodGet, Path: "/api/tokens",
		Summary: "列出所有 API token 的中繼資料（不含明文與 hash，AUTH-R7）",
	}, auth.ScopeAdmin, rt.listTokens)
	registerScoped(api, huma.Operation{
		OperationID: "tokens-create", Method: http.MethodPost, Path: "/api/tokens",
		Summary:       "建立 API token，只在建立時回傳一次明文 token（AUTH-R7）",
		DefaultStatus: http.StatusCreated,
	}, auth.ScopeAdmin, rt.createToken)
	registerScoped(api, huma.Operation{
		OperationID: "tokens-revoke", Method: http.MethodDelete, Path: "/api/tokens/{id}",
		Summary: "撤銷 API token（依 id 或有效的名稱，AUTH-R9）",
	}, auth.ScopeAdmin, rt.revokeToken)
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
	// token 不隱含繼承 scope（AUTH-R8）：用 token 建立 token 時，只能發出自己有的 scope，
	// 否則只有 admin 的 token 可以替自己發出帶 ingest、agent 的 token。owner（session、本機）不受限制。
	if p, ok := principalFrom(ctx); ok && p.Kind == "token" {
		for _, sc := range in.Body.Scopes {
			if auth.ValidScope(sc) && !p.HasScope(sc) {
				return nil, errForbiddenScope(sc)
			}
		}
	}
	var expiresIn *time.Duration
	if in.Body.ExpiresIn != "" {
		d, err := auth.ParseTokenDuration(in.Body.ExpiresIn)
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
