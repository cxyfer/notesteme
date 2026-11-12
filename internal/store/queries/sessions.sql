-- name: CreateSession :exec
INSERT INTO sessions (id, token_hash, created_at, expires_at, ip, user_agent)
VALUES (sqlc.arg(id), sqlc.arg(token_hash), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(ip), sqlc.arg(user_agent));

-- name: SessionByHash :one
SELECT * FROM sessions WHERE token_hash = sqlc.arg(token_hash) AND expires_at > sqlc.arg(now);

-- name: ExtendSession :exec
UPDATE sessions SET expires_at = sqlc.arg(expires_at) WHERE id = sqlc.arg(id);

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= sqlc.arg(now);
