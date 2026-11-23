-- name: ListGroupMemberships :many
SELECT * FROM group_members WHERE user_id = $1 ORDER BY group_name, source, rule_provider;

-- name: AddGroupMember :execrows
-- Idempotent: an existing membership of the same source is left alone.
INSERT INTO group_members (user_id, group_name, source, rule_provider, added_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING;

-- name: RemoveGroupMember :execrows
DELETE FROM group_members WHERE user_id = $1 AND group_name = $2 AND source = $3 AND rule_provider = $4;

-- name: DeleteRuleMemberships :exec
-- Rule memberships granted by one provider are recomputed on every sign-in with that provider (HACC-R11).
DELETE FROM group_members WHERE user_id = $1 AND source = 'rule' AND rule_provider = $2;

-- name: DeleteConfigMemberships :exec
DELETE FROM group_members WHERE group_name = $1 AND source = 'config';

-- name: CreatePAT :exec
INSERT INTO pats (id, user_id, name, token_hash, permissions, created_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: PATByHash :one
SELECT * FROM pats WHERE token_hash = $1;

-- name: ListPATs :many
-- Revoked tokens are not listed; expired ones are (the UI shows them as expired).
SELECT * FROM pats WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC, seq DESC;

-- name: RevokePAT :execrows
-- Only the owner can revoke; revoking twice is a no-op that still matches the row.
UPDATE pats SET revoked_at = COALESCE(revoked_at, sqlc.arg(now)) WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);
