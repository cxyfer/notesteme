package postgres

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/cxyfer/notesteme/internal/hub/hubstore"
	"github.com/cxyfer/notesteme/internal/hub/hubstore/postgres/sqlcdb"
)

// 群組成員資格與 Hub token（同 sqlite/permissions.go）。

// IdentityBySubject 實作 hubstore.Store。
func (s *Store) IdentityBySubject(ctx context.Context, provider, subject string) (hubstore.Identity, error) {
	r, err := s.q.IdentityBySubject(ctx, sqlcdb.IdentityBySubjectParams{Provider: provider, Subject: subject})
	if err != nil {
		return hubstore.Identity{}, notFound(err)
	}
	return toIdentity(r), nil
}

// GroupMemberships 實作 hubstore.Store。
func (s *Store) GroupMemberships(ctx context.Context, userID string) ([]hubstore.GroupMembership, error) {
	rows, err := s.q.ListGroupMemberships(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]hubstore.GroupMembership, 0, len(rows))
	for _, r := range rows {
		out = append(out, hubstore.GroupMembership{Group: r.GroupName, Source: r.Source, RuleProvider: r.RuleProvider, AddedAt: utc(r.AddedAt)})
	}
	return out, nil
}

// setGroup 以 source 加入或移出群組，有改變時寫入稽核紀錄（actor 空字串時不寫）；帳號不存在回 ErrNotFound。
func (s *Store) setGroup(ctx context.Context, actor, userID, group, source string, member bool) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(q *sqlcdb.Queries) error {
		changed = false
		if err := lockUser(ctx, q, userID); err != nil {
			return err
		}
		now := s.now()
		var (
			n      int64
			err    error
			action = hubstore.AuditGroupAdd
		)
		if member {
			n, err = q.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{UserID: userID, GroupName: group, Source: source, AddedAt: now})
		} else {
			action = hubstore.AuditGroupRemove
			n, err = q.RemoveGroupMember(ctx, sqlcdb.RemoveGroupMemberParams{UserID: userID, GroupName: group, Source: source})
		}
		if err != nil || n == 0 {
			return err
		}
		changed = true
		if actor == "" {
			return nil
		}
		return audit(ctx, q, actor, action, "user", userID, map[string]string{"group": group}, now)
	})
	return changed, err
}

// SetManualGroup 實作 hubstore.Store。
func (s *Store) SetManualGroup(ctx context.Context, actorUserID, userID, group string, member bool) (bool, error) {
	return s.setGroup(ctx, actorUserID, userID, group, hubstore.SourceManual, member)
}

// SetCLIGroup 實作 hubstore.Store。
func (s *Store) SetCLIGroup(ctx context.Context, userID, group string, member bool) (bool, error) {
	return s.setGroup(ctx, hubstore.CLIActorID, userID, group, hubstore.SourceCLI, member)
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
		_, err := s.q.AddGroupMember(ctx, sqlcdb.AddGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceConfig, AddedAt: s.now()})
		return err
	}
	_, err := s.q.RemoveGroupMember(ctx, sqlcdb.RemoveGroupMemberParams{UserID: userID, GroupName: group, Source: hubstore.SourceConfig})
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
	b, err := json.Marshal(p.Permissions)
	if err != nil {
		return err
	}
	return s.q.CreatePAT(ctx, sqlcdb.CreatePATParams{
		ID: p.ID, UserID: p.UserID, Name: p.Name, TokenHash: p.TokenHash, Permissions: b,
		CreatedAt: ts(p.CreatedAt), ExpiresAt: ts(p.ExpiresAt),
	})
}

func toPAT(r sqlcdb.Pat) hubstore.PAT {
	out := hubstore.PAT{
		ID: r.ID, UserID: r.UserID, Name: r.Name, TokenHash: r.TokenHash,
		CreatedAt: utc(r.CreatedAt), ExpiresAt: utc(r.ExpiresAt), RevokedAt: optTime(r.RevokedAt),
	}
	// permissions 只由這個套件寫入；解析失敗時當作沒有權限（預設拒絕）。
	if err := json.Unmarshal(r.Permissions, &out.Permissions); err != nil {
		out.Permissions = nil
	}
	out.Permissions = slices.Clip(out.Permissions)
	return out
}

// PATByHash 實作 hubstore.Store。
func (s *Store) PATByHash(ctx context.Context, tokenHash string) (hubstore.PAT, error) {
	r, err := s.q.PATByHash(ctx, tokenHash)
	if err != nil {
		return hubstore.PAT{}, notFound(err)
	}
	return toPAT(r), nil
}

// PATs 實作 hubstore.Store。
func (s *Store) PATs(ctx context.Context, userID string) ([]hubstore.PAT, error) {
	rows, err := s.q.ListPATs(ctx, userID)
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
	n, err := s.q.RevokePAT(ctx, sqlcdb.RevokePATParams{Now: &now, ID: id, UserID: userID})
	if err != nil {
		return err
	}
	if n == 0 {
		return hubstore.ErrNotFound
	}
	return nil
}
