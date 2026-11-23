-- name: CreateSession :exec
INSERT INTO sessions (id, token_hash, created_at, expires_at, ip, user_agent, method, last_seen_at, proxy_user)
VALUES (sqlc.arg(id), sqlc.arg(token_hash), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(ip), sqlc.arg(user_agent),
        sqlc.arg(method), sqlc.arg(created_at), sqlc.arg(proxy_user));

-- name: SessionByHash :one
SELECT * FROM sessions WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now);

-- name: ExtendSession :exec
UPDATE sessions SET expires_at = sqlc.arg(expires_at) WHERE id = sqlc.arg(id);

-- name: TouchSession :exec
-- Updates last_seen_at and the idle expiry together (AUTH-R4, R20); the caller throttles it.
UPDATE sessions SET last_seen_at = sqlc.arg(last_seen_at), expires_at = sqlc.arg(expires_at) WHERE id = sqlc.arg(id);

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteSessionByID :execrows
DELETE FROM sessions WHERE id = sqlc.arg(id);

-- name: ListSessions :many
-- Never returns token_hash. Only sessions that have not expired at the injected clock now.
SELECT id, method, created_at, last_seen_at, ip, user_agent
FROM sessions
WHERE expires_at > sqlc.arg(now)
ORDER BY last_seen_at DESC, id DESC;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= sqlc.arg(now);

-- name: DeleteSessionsExcept :execrows
DELETE FROM sessions WHERE token_hash != sqlc.arg(keep_token_hash);

-- name: CreateOIDCLogin :exec
INSERT INTO oidc_logins (state_hash, cookie_hash, nonce, code_verifier, next, created_at, expires_at)
VALUES (sqlc.arg(state_hash), sqlc.arg(cookie_hash), sqlc.arg(nonce), sqlc.arg(code_verifier), sqlc.arg(next),
        sqlc.arg(created_at), sqlc.arg(expires_at));

-- name: TakeOIDCLogin :one
-- Single use: the row is deleted whether or not the caller accepts it (AUTH-R17, R18).
DELETE FROM oidc_logins WHERE state_hash = sqlc.arg(state_hash)
RETURNING state_hash, cookie_hash, nonce, code_verifier, next, created_at, expires_at;

-- name: DeleteExpiredOIDCLogins :execrows
DELETE FROM oidc_logins WHERE expires_at <= sqlc.arg(now);

-- name: CountOIDCLogins :one
SELECT COUNT(*) FROM oidc_logins;

-- name: UpsertOIDCIdentity :exec
INSERT INTO oidc_identities (issuer, subject, last_login_at)
VALUES (sqlc.arg(issuer), sqlc.arg(subject), sqlc.arg(last_login_at))
ON CONFLICT (issuer, subject) DO UPDATE SET last_login_at = excluded.last_login_at;
