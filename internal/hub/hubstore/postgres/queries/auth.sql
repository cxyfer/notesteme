-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateUser :exec
INSERT INTO users (id, display_name, created_at) VALUES ($1, $2, $3);

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: LockUser :one
-- Row lock that serializes the read-then-write account changes (unlink, link, group edits) of one account.
SELECT id FROM users WHERE id = $1 FOR UPDATE;

-- name: CreateIdentity :exec
INSERT INTO identities (id, user_id, provider, subject, login, email, email_verified, attrs_json, linked_at, last_login_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: IdentityBySubject :one
SELECT * FROM identities WHERE provider = $1 AND subject = $2;

-- name: UpdateIdentityLogin :exec
-- Refresh the provider-side attributes on every sign-in (HACC-R4, R11).
UPDATE identities SET login = $1, email = $2, email_verified = $3, attrs_json = $4, last_login_at = $5 WHERE id = $6;

-- name: ListIdentities :many
-- Ordered by link time; seq (insertion order) breaks ties within the same millisecond.
SELECT * FROM identities WHERE user_id = $1 ORDER BY linked_at, seq;

-- name: IdentityOfUser :one
SELECT * FROM identities WHERE user_id = $1 AND provider = $2;

-- name: CountIdentities :one
SELECT COUNT(*) FROM identities WHERE user_id = $1;

-- name: DeleteIdentity :exec
DELETE FROM identities WHERE id = $1;

-- name: DeleteExpiredOAuthStates :exec
DELETE FROM oauth_states WHERE expires_at <= $1;

-- name: CreateOAuthState :exec
INSERT INTO oauth_states (state_hash, provider, intent, user_id, verifier_nonce, verifier_sealed, nonce, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ConsumeOAuthState :one
-- Marks the state used; only an unused, unexpired row of the same provider matches, so a state works once.
UPDATE oauth_states SET used_at = sqlc.arg(now)
WHERE state_hash = sqlc.arg(state_hash) AND provider = sqlc.arg(provider) AND used_at IS NULL AND expires_at > sqlc.arg(now)
RETURNING *;

-- name: CreateSession :exec
INSERT INTO sessions (id, user_id, token_hash, created_at, authenticated_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6);

-- name: SessionByHash :one
SELECT * FROM sessions WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now);

-- name: TouchSession :exec
UPDATE sessions SET expires_at = $1 WHERE id = $2;

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: CreateAuditEntry :exec
INSERT INTO audit_log (id, actor_user_id, action, target_kind, target_id, detail_json, at) VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAuditEntries :many
-- Command-line actions (notesteme-hub admin) have no actor account.
SELECT a.id, a.actor_user_id, u.display_name AS actor, a.action, a.target_kind, a.target_id, a.at
FROM audit_log a LEFT JOIN users u ON u.id = a.actor_user_id
ORDER BY a.at DESC, a.seq DESC LIMIT $1;
