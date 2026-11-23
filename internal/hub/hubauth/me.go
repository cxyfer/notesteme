package hubauth

import (
	"context"
	"encoding/base64"
)

func encodeB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// IdentityView 是 GET /api/v1/me 的一個登入方式。
type IdentityView struct {
	Provider string `json:"provider" example:"github"`
	Login    string `json:"login" example:"alice"`
}

// Me 是 GET /api/v1/me 的內容（HACC 規格「介面」）。
type Me struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Identities  []IdentityView `json:"identities"`
	Groups      []string       `json:"groups"`
	Permissions []string       `json:"permissions"`
}

// Me 回傳目前帳號的資訊。
func (s *Service) Me(ctx context.Context, p *Principal) (Me, error) {
	ids, err := s.store.Identities(ctx, p.User.ID)
	if err != nil {
		return Me{}, err
	}
	groups, err := s.Groups(ctx, p.User.ID)
	if err != nil {
		return Me{}, err
	}
	// 用 Hub token 呼叫時 permissions 是這個 token 的有效權限（PAT ∩ 帳號），groups 仍是帳號的群組。
	perms, err := s.EffectivePermissions(ctx, p)
	if err != nil {
		return Me{}, err
	}
	out := Me{ID: p.User.ID, DisplayName: p.User.DisplayName, Identities: []IdentityView{}, Groups: groups, Permissions: perms}
	for _, i := range ids {
		out.Identities = append(out.Identities, IdentityView{Provider: i.Provider, Login: i.Login})
	}
	return out, nil
}
