package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

// registerAuthSessions 註冊工作階段列表與撤銷（AUTH-R20）、token 輪替（AUTH-R21）。
func (rt *routes) registerAuthSessions(api huma.API) {
	registerScoped(api, huma.Operation{
		OperationID: "auth-sessions-list", Method: http.MethodGet, Path: "/api/auth/sessions",
		Summary: "列出登入的工作階段，依最後使用時間由新到舊；不含 session 的 secret（AUTH-R20）",
	}, auth.ScopeAdmin, rt.listSessions)
	registerScoped(api, huma.Operation{
		OperationID: "auth-sessions-revoke", Method: http.MethodDelete, Path: "/api/auth/sessions/{id}",
		Summary:       "撤銷一個工作階段；id 為 current 時撤銷目前的 session 並清除 cookie（等同登出，AUTH-R20）",
		DefaultStatus: http.StatusNoContent,
	}, auth.ScopeAdmin, rt.revokeSession)
	registerScoped(api, huma.Operation{
		OperationID: "tokens-rotate", Method: http.MethodPost, Path: "/api/tokens/{id}/rotate",
		Summary: "輪替 token 的 secret：名稱、scope 與到期時間不變，舊的 secret 立即失效；新 token 只回傳這一次（AUTH-R21）",
	}, auth.ScopeAdmin, rt.rotateToken)
}

type listSessionsOutput struct {
	Body struct {
		Sessions []auth.SessionInfo `json:"sessions"`
	}
}

type sessionPathInput struct {
	ID string `path:"id" maxLength:"128" doc:"session ID；current 代表送出請求的這個 session"`
}

type revokeSessionOutput struct {
	SetCookie []string `header:"Set-Cookie"`
}

func (rt *routes) listSessions(ctx context.Context, _ *struct{}) (*listSessionsOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Auth == nil {
		return nil, rt.noAuth(ctx)
	}
	p, _ := principalFrom(ctx)
	sessions, err := rt.d.Auth.ListSessions(ctx, p.SessionID)
	if err != nil {
		rt.d.Log.ErrorContext(ctx, "列出工作階段失敗", "error", err)
		return nil, asError(err)
	}
	out := &listSessionsOutput{}
	out.Body.Sessions = sessions
	return out, nil
}

func (rt *routes) revokeSession(ctx context.Context, in *sessionPathInput) (*revokeSessionOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Auth == nil {
		return nil, rt.noAuth(ctx)
	}
	p, _ := principalFrom(ctx)
	id := in.ID
	if id == "current" {
		// token 與本機呼叫沒有 session：找不到。
		id = p.SessionID
	}
	err := rt.d.Auth.RevokeSession(ctx, id)
	switch {
	case errors.Is(err, auth.ErrSessionNotFound):
		return nil, newError(http.StatusNotFound, CodeNotFound, err.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "撤銷工作階段失敗", "error", err)
		return nil, asError(err)
	}
	out := &revokeSessionOutput{}
	if p.SessionID != "" && id == p.SessionID {
		info := infoFrom(ctx)
		out.SetCookie = []string{
			expiredCookie(auth.SessionCookie, true, info.Secure).String(),
			expiredCookie(auth.CSRFCookie, false, info.Secure).String(),
		}
	}
	return out, nil
}

func (rt *routes) rotateToken(ctx context.Context, in *tokenPathInput) (*createTokenOutput, error) {
	if err := requireScope(ctx, auth.ScopeAdmin); err != nil {
		return nil, err
	}
	if rt.d.Auth == nil {
		return nil, rt.noAuth(ctx)
	}
	rotated, err := rt.d.Auth.RotateToken(ctx, in.ID)
	switch {
	case errors.Is(err, auth.ErrTokenNotFound):
		return nil, newError(http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, auth.ErrTokenInactive):
		return nil, newError(http.StatusConflict, CodeTokenInactive, err.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "輪替 API token 失敗", "error", err)
		return nil, asError(err)
	}
	return &createTokenOutput{Body: rotated}, nil
}
