package api

import (
	"context"
	"errors"
	"math"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/auth"
)

type changePasswordInput struct {
	Session string `cookie:"notesteme_session"`
	Body    struct {
		CurrentPassword string `json:"current_password" doc:"目前的密碼"`
		NewPassword     string `json:"new_password" doc:"新密碼（不可以是空的）"`
	}
}

type changePasswordOutput struct {
	Body struct {
		Changed bool `json:"changed"`
	}
}

// registerAuthPassword 註冊修改密碼（AUTH-R2「設定頁修改密碼」）。只有 owner（session 或本機）可以使用，
// token 即使有 admin scope 也拒絕：密碼等同登入憑證，和 cookie 上傳一樣（SEC 規格的 POST /api/cookies）。
func (rt *routes) registerAuthPassword(api huma.API) {
	registerOwnerOnly(api, huma.Operation{
		OperationID: "auth-password", Method: http.MethodPost, Path: "/api/auth/password",
		Summary: "修改登入密碼：目前的密碼錯誤回 422 invalid_password（和登入共用鎖定，AUTH-R6）；成功後撤銷目前以外的所有 session",
	}, "修改密碼", rt.changePassword)
}

func (rt *routes) changePassword(ctx context.Context, in *changePasswordInput) (*changePasswordOutput, error) {
	if err := requireOwner(ctx, "修改密碼"); err != nil {
		return nil, err
	}
	info := infoFrom(ctx)
	err := rt.d.Auth.ChangePassword(ctx, in.Body.CurrentPassword, in.Body.NewPassword, info.IP, in.Session)
	var locked *auth.LockedError
	switch {
	case errors.Is(err, auth.ErrInvalidPassword):
		// 不用 401：401 unauthorized 代表 session 失效，前端會導到登入頁。
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidPass, auth.ErrInvalidPassword.Error())
	case errors.Is(err, auth.ErrEmptyNewPassword):
		return nil, newError(http.StatusUnprocessableEntity, CodeInvalidInput, auth.ErrEmptyNewPassword.Error())
	case errors.As(err, &locked):
		secs := int(math.Ceil(locked.RetryAfter.Seconds()))
		e := newError(http.StatusTooManyRequests, CodeLoginLocked, locked.Error())
		e.RetryAfterS = &secs
		return nil, e
	case errors.Is(err, auth.ErrNoPassword):
		return nil, newError(http.StatusConflict, CodeNoPassword, err.Error())
	case err != nil:
		rt.d.Log.ErrorContext(ctx, "修改密碼時發生錯誤", "error", err)
		return nil, asError(err)
	}
	out := &changePasswordOutput{}
	out.Body.Changed = true
	return out, nil
}
