package hubauth

import (
	"context"
	"encoding/base64"
	"slices"

	"github.com/cxyfer/notesteme/internal/hub/hubconfig"
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

// Groups 回傳帳號所屬的群組（M5-S1：每個登入的帳號都在 members；手動指派與自動規則由 S2 加入）。
func (s *Service) Groups(context.Context, *Principal) ([]string, error) {
	return []string{hubconfig.GroupMembers}, nil
}

// Permissions 回傳群組權限的聯集，依 hubconfig.Permissions 的順序（HACC-R10）；admin 包含全部權限。
func (s *Service) Permissions(groups []string) []string {
	held := map[string]bool{}
	for _, g := range groups {
		for _, p := range s.cfg.Groups[g].Permissions {
			held[p] = true
		}
	}
	if held[hubconfig.PermAdmin] {
		return slices.Clone(hubconfig.Permissions)
	}
	out := []string{}
	for _, p := range hubconfig.Permissions {
		if held[p] {
			out = append(out, p)
		}
	}
	return out
}

// Me 回傳目前帳號的資訊。
func (s *Service) Me(ctx context.Context, p *Principal) (Me, error) {
	ids, err := s.store.Identities(ctx, p.User.ID)
	if err != nil {
		return Me{}, err
	}
	groups, err := s.Groups(ctx, p)
	if err != nil {
		return Me{}, err
	}
	out := Me{ID: p.User.ID, DisplayName: p.User.DisplayName, Identities: []IdentityView{}, Groups: groups, Permissions: s.Permissions(groups)}
	for _, i := range ids {
		out.Identities = append(out.Identities, IdentityView{Provider: i.Provider, Login: i.Login})
	}
	return out, nil
}
