package hubapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cxyfer/notesteme/internal/hub/hubauth"
	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
	"github.com/cxyfer/notesteme/internal/hub/hubstore"
)

// Hub token 與管理的路由（M5-S2，HACC-R9、R12、R17）。

// TokenView 是 Hub token 的公開資訊（不含 token 本身）。
type TokenView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func tokenView(p hubstore.PAT) TokenView {
	perms := p.Permissions
	if perms == nil {
		perms = []string{}
	}
	return TokenView{ID: p.ID, Name: p.Name, Permissions: perms, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}
}

type tokensOutput struct {
	Body struct {
		Tokens []TokenView `json:"tokens"`
	}
}

func (rt *routes) listTokens(ctx context.Context, _ *struct{}) (*tokensOutput, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	pats, err := rt.d.Auth.Tokens(ctx, p)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &tokensOutput{}
	o.Body.Tokens = make([]TokenView, 0, len(pats))
	for _, x := range pats {
		o.Body.Tokens = append(o.Body.Tokens, tokenView(x))
	}
	return o, nil
}

// 欄位的範圍刻意不寫成 schema 的限制：由 hubauth 驗證，422 的 errors[].path 才會指到欄位（HACC-R9）。
type createTokenInput struct {
	Body struct {
		Name          string   `json:"name" required:"false" doc:"token 的名稱（1～100 個字元）"`
		Permissions   []string `json:"permissions" required:"false" doc:"權限，必須是帳號目前擁有的權限（read、download、regenerate、upload、moderate、admin）"`
		ExpiresInDays *int     `json:"expires_in_days,omitempty" required:"false" doc:"有效天數 1～365，預設 90"`
	}
}

type createTokenOutput struct {
	Body struct {
		TokenView
		Token string `json:"token" doc:"token 本身（nsh_ 開頭），只顯示這一次"`
	}
}

func (rt *routes) createToken(ctx context.Context, in *createTokenInput) (*createTokenOutput, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	c, err := rt.d.Auth.CreateToken(ctx, p, hubauth.NewToken{Name: in.Body.Name, Permissions: in.Body.Permissions, ExpiresInDays: in.Body.ExpiresInDays})
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &createTokenOutput{}
	o.Body.TokenView = tokenView(c.PAT)
	o.Body.Token = c.Token
	return o, nil
}

type idPath struct {
	ID string `path:"id" maxLength:"64" doc:"ID"`
}

func (rt *routes) revokeToken(ctx context.Context, in *idPath) (*struct{}, error) {
	p, err := mustPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if err := rt.d.Auth.RevokeToken(ctx, p, in.ID); err != nil {
		return nil, rt.apiError(ctx, err)
	}
	return nil, nil
}

type groupPath struct {
	ID    string `path:"id" maxLength:"64" doc:"使用者 ID"`
	Group string `path:"group" maxLength:"64" doc:"群組名稱"`
}

func (rt *routes) setGroup(member bool) func(context.Context, *groupPath) (*struct{}, error) {
	return func(ctx context.Context, in *groupPath) (*struct{}, error) {
		p, err := mustPrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := rt.d.Auth.SetGroup(ctx, p, in.ID, in.Group, member); err != nil {
			return nil, rt.apiError(ctx, err)
		}
		return nil, nil
	}
}

// AuditView 是一筆稽核紀錄（HACC-R17）。
type AuditView struct {
	Actor      string    `json:"actor" doc:"執行者當下的 display_name"`
	Action     string    `json:"action"`
	TargetKind string    `json:"target_kind"`
	TargetID   string    `json:"target_id"`
	At         time.Time `json:"at"`
}

type auditInput struct {
	Limit int `query:"limit" minimum:"1" maximum:"500" default:"100" doc:"最多幾筆"`
}

type auditOutput struct {
	Body struct {
		Entries []AuditView `json:"entries"`
	}
}

func (rt *routes) audit(ctx context.Context, in *auditInput) (*auditOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := rt.d.Store.AuditEntries(ctx, limit)
	if err != nil {
		return nil, rt.apiError(ctx, err)
	}
	o := &auditOutput{}
	o.Body.Entries = make([]AuditView, 0, len(rows))
	for _, r := range rows {
		o.Body.Entries = append(o.Body.Entries, AuditView{Actor: r.Actor, Action: r.Action, TargetKind: r.TargetKind, TargetID: r.TargetID, At: r.At})
	}
	return o, nil
}

// registerAccounts 註冊 M5-S2 實作的路由（Access 同權限矩陣，HACC 規格「介面」）。
func registerAccounts(api huma.API, rt *routes) {
	reg := func(op huma.Operation, access Access, spec string) huma.Operation {
		op.Description = "規則：" + spec + "。"
		op.Extensions = map[string]any{AccessExtension: string(access)}
		return op
	}
	authErrs := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests}
	huma.Register(api, reg(huma.Operation{
		OperationID: "me-tokens-list", Method: http.MethodGet, Path: "/api/v1/me/tokens",
		Summary: "列出 Hub token（未撤銷的，含已過期的）", Errors: authErrs,
	}, AccessSession, "HACC-R9"), rt.listTokens)
	huma.Register(api, reg(huma.Operation{
		OperationID: "me-tokens-create", Method: http.MethodPost, Path: "/api/v1/me/tokens", DefaultStatus: http.StatusCreated,
		Summary: "建立 Hub token，token 只顯示這一次", Errors: append(authErrs, http.StatusUnprocessableEntity),
	}, AccessSession, "HACC-R9"), rt.createToken)
	huma.Register(api, reg(huma.Operation{
		OperationID: "me-tokens-revoke", Method: http.MethodDelete, Path: "/api/v1/me/tokens/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "撤銷 Hub token", Errors: append(authErrs, http.StatusNotFound),
	}, AccessSession, "HACC-R9"), rt.revokeToken)
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-users-group-add", Method: http.MethodPut, Path: "/api/v1/admin/users/{id}/groups/{group}", DefaultStatus: http.StatusNoContent,
		Summary: "把使用者加入群組（source: manual；admins 只能在設定檔指定）", Errors: append(authErrs, http.StatusNotFound, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermAdmin), "HACC-R12"), rt.setGroup(true))
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-users-group-remove", Method: http.MethodDelete, Path: "/api/v1/admin/users/{id}/groups/{group}", DefaultStatus: http.StatusNoContent,
		Summary: "把使用者移出群組（只移除 source: manual 的成員資格）", Errors: append(authErrs, http.StatusNotFound, http.StatusUnprocessableEntity),
	}, perm(hubconfig.PermAdmin), "HACC-R12"), rt.setGroup(false))
	huma.Register(api, reg(huma.Operation{
		OperationID: "admin-audit", Method: http.MethodGet, Path: "/api/v1/admin/audit",
		Summary: "稽核紀錄（依時間倒序）", Errors: authErrs,
	}, perm(hubconfig.PermAdmin), "HACC-R17"), rt.audit)
}
