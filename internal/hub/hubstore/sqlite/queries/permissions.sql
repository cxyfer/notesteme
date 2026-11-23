-- name: ListGroupMemberships :many
SELECT * FROM group_members WHERE user_id = ? ORDER BY group_name, source, rule_provider;

-- name: AddGroupMember :execrows
-- Idempotent: an existing membership of the same source is left alone.
INSERT INTO group_members (user_id, group_name, source, rule_provider, added_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :execrows
DELETE FROM group_members WHERE user_id = ? AND group_name = ? AND source = ? AND rule_provider = ?;

-- name: DeleteRuleMemberships :exec
-- Rule memberships granted by one provider are recomputed on every sign-in with that provider (HACC-R11).
DELETE FROM group_members WHERE user_id = ? AND source = 'rule' AND rule_provider = ?;

-- name: DeleteConfigMemberships :exec
DELETE FROM group_members WHERE group_name = ? AND source = 'config';

-- name: CreatePAT :exec
INSERT INTO pats (id, user_id, name, token_hash, permissions, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: PATByHash :one
SELECT * FROM pats WHERE token_hash = ?;

-- name: ListPATs :many
-- Revoked tokens are not listed; expired ones are (the UI shows them as expired).
SELECT * FROM pats WHERE user_id = ? AND revoked_at IS NULL ORDER BY created_at DESC, rowid DESC;

-- name: RevokePAT :execrows
-- Only the owner can revoke; revoking twice is a no-op that still matches the row.
UPDATE pats SET revoked_at = COALESCE(revoked_at, sqlc.arg(now)) WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
