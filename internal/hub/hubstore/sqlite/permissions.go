package sqlite

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/sqlite/sqlcdb"
)

// 群組成員資格與 Hub token（M5-S2，HACC-R9～R12）。

// IdentityBySubject 實作 hubstore.Store。
func (s *Store) IdentityBySubject(ctx context.Context, provider, subject string) (hubstore.Identity, error) {
	r, err := s.rq.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: provider, Subject: subject})
	if err != nil {
		return hubstore.Identity{}, notFound(err)
	}
	return toIdentity(r), nil
}

// GroupMemberships 實作 hubstore.Store。
func (s *Store) GroupMemberships(ctx context.Context, userID string) ([]hubstore.GroupMembership, error) {
	rows, err := s.rq.ListGroupMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.GroupMembership, 0, len(rows))
	for _, r := range rows {
		out = append(out, hubstore.GroupMembership{Group: r.GroupName, Source: r.Source, RuleProvider: r.RuleProvider, AddedAt: fromMS(r.AddedAt)})
	}
	return out, nil
}

// SetManualGroup 實作 hubstore.Store。
func (s *Store) SetManualGroup(ctx context.Context, actorUserID, userID, group string, member bool) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if _, err := q.GetUser(ctx, userID); err != nil {
			return notFound(err)
		}
		now := s.now()
		var (
			n      int64
			err    error
			action = hubstore.AuditGroupAdd
		)
		if member {
			n, err = q.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceManual, AddedAt: now})
		} else {
			action = hubstore.AuditGroupRemove
			n, err = q.RemoveGroupMember(ctx, sqlcdb.RemoveGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceManual})
		}
		if err != nil || n == 0 {
			return err
		}
		changed = true
		if actorUserID == "" {
			return nil
		}
		return audit(ctx, q, actorUserID, action, "user", userID, map[string]string{"group": group}, now)
	})
	return changed, err
}

// SyncRuleGroups 實作 hubstore.Store。
func (s *Store) SyncRuleGroups(ctx context.Context, userID, provider string, groups []string) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if err := q.DeleteRuleMemberships(ctx, sqlcdb.DeleteRuleMembershipsParams{UserID: userID, RuleProvider: provider}); err != nil {
			return err
		}
		now := s.now()
		for _, g := range groups {
			if _, err := q.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{
				UserID: userID, GroupName: g, Source: hubstore.SourceRule, RuleProvider: provider, AddedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetConfigGroup 實作 hubstore.Store。
func (s *Store) SetConfigGroup(ctx context.Context, userID, group string, member bool) error {
	if member {
		_, err := s.wq.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceConfig, AddedAt: s.now()})
		return err
	}
	_, err := s.wq.RemoveGroupMember(ctx, sqlcdb.RemoveGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceConfig})
	return err
}

// ReplaceConfigGroup 實作 hubstore.Store。
func (s *Store) ReplaceConfigGroup(ctx context.Context, group string, userIDs []string) error {
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if err := q.DeleteConfigMemberships(ctx, group); err != nil {
			return err
		}
		now := s.now()
		for _, id := range userIDs {
			if _, err := q.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{UserID: id, GroupName: group, Source: hubstore.SourceConfig, AddedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
}

// CreatePAT 實作 hubstore.Store。
func (s *Store) CreatePAT(ctx context.Context, p hubstore.PAT) error {
	perms, err := json.Marshal(p.Permissions)
	if err != nil {
		return err
	}
	// 建立與稽核紀錄在同一個交易中（安全修補 A）。
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		if err := q.CreatePAT(ctx, sqlcdb.CreatePATParams{
			ID: p.ID, UserID: p.UserID, Name: p.Name, TokenHash: p.TokenHash, Permissions: string(perms),
			CreatedAt: ms(p.CreatedAt), ExpiresAt: ms(p.ExpiresAt),
		}); err != nil {
			return err
		}
		return audit(ctx, q, p.UserID, hubstore.AuditTokenCreate, "token", p.ID, map[string]any{"name": p.Name, "permissions": p.Permissions}, s.now())
	})
}

func toPAT(r sqlcdb.Pat) hubstore.PAT {
	out := hubstore.PAT{
		ID: r.ID, UserID: r.UserID, Name: r.Name, TokenHash: r.TokenHash,
		CreatedAt: fromMS(r.CreatedAt), ExpiresAt: fromMS(r.ExpiresAt),
	}
	// permissions 只由這個套件寫入；解析失敗時當作沒有權限（預設拒絕）。
	if err := json.Unmarshal([]byte(r.Permissions), &out.Permissions); err != nil {
		out.Permissions = nil
	}
	out.Permissions = slices.Clip(out.Permissions)
	if r.RevokedAt != nil {
		t := fromMS(*r.RevokedAt)
		out.RevokedAt = &t
	}
	return out
}

// PATByHash 實作 hubstore.Store。
func (s *Store) PATByHash(ctx context.Context, tokenHash string) (hubstore.PAT, error) {
	r, err := s.rq.PATByHash(ctx, tokenHash)
	if err != nil {
		return hubstore.PAT{}, notFound(err)
	}
	return toPAT(r), nil
}

// PATs 實作 hubstore.Store。
func (s *Store) PATs(ctx context.Context, userID string) ([]hubstore.PAT, error) {
	rows, err := s.rq.ListPATs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.PAT, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPAT(r))
	}
	return out, nil
}

// RevokePAT 實作 hubstore.Store。
func (s *Store) RevokePAT(ctx context.Context, userID, id string) error {
	now := s.now()
	return s.inTx(ctx, func(q *sqlcdb.Queries) error {
		changed, err := q.RevokeActivePAT(ctx, sqlcdb.RevokeActivePATParams{Now: &now, ID: id, UserID: userID})
		if err != nil {
			return err
		}
		if changed > 0 {
			return audit(ctx, q, userID, hubstore.AuditTokenRevoke, "token", id, map[string]any{}, now)
		}
		n, err := q.RevokePAT(ctx, sqlcdb.RevokePATParams{Now: &now, ID: id, UserID: userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return hubstore.ErrNotFound
		}
		return nil
	})
}
