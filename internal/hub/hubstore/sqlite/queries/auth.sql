-- name: CreateUser :exec
INSERT INTO users (id, display_name, created_at) VALUES (?, ?, ?);

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: CreateIdentity :exec
INSERT INTO identities (id, user_id, provider, subject, login, email, email_verified, attrs_json, linked_at, last_login_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: IdentityBySubject :one
SELECT * FROM identities WHERE provider = ? AND subject = ?;

-- name: UpdateIdentityLogin :exec
-- Refresh the provider-side attributes on every sign-in (HACC-R4, R11).
UPDATE identities SET login = ?, email = ?, email_verified = ?, attrs_json = ?, last_login_at = ? WHERE id = ?;

-- name: ListIdentities :many
-- Ordered by link time; rowid breaks ties within the same millisecond.
SELECT * FROM identities WHERE user_id = ? ORDER BY linked_at, rowid;

-- name: IdentityOfUser :one
SELECT * FROM identities WHERE user_id = ? AND provider = ?;

-- name: CountIdentities :one
SELECT COUNT(*) FROM identities WHERE user_id = ?;

-- name: DeleteIdentity :exec
DELETE FROM identities WHERE id = ?;

-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at <= ?;

-- name: CreateOAuthState :exec
INSERT INTO oauth_states (state_hash, provider, intent, user_id, verifier_nonce, verifier_sealed, nonce, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ConsumeOAuthState :one
-- Marks the state used; only an unused, unexpired row of the same provider matches, so a state works once.
UPDATE oauth_states SET used_at = sqlc.arg(now)
WHERE state_hash = sqlc.arg(state_hash) AND provider = sqlc.arg(provider) AND used_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, token_hash, created_at, authenticated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: SessionByHash :one
SELECT * FROM sessions WHERE token_hash = ? AND expires_at > sqlc.arg(now);

-- name: TouchSession :exec
UPDATE sessions SET expires_at = ? WHERE id = ?;

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, detail_json, at) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListAuditEntries :many
-- Command-line actions (notesteme-hub admin) have no actor account.
SELECT a.id, a.actor_user_id, u.display_name AS actor, a.action, a.target_kind, a.target_id, a.at
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_user_id
ORDER BY a.at DESC, a.rowid DESC LIMIT ?;
