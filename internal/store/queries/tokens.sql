-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, name, prefix, scopes, token_hash, expires_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(prefix), sqlc.arg(scopes), sqlc.arg(token_hash), sqlc.narg(expires_at), sqlc.arg(created_at));

-- name: APITokenByHash :one
-- Never returns token_hash. Checks revoked_at and expires_at against injected clock now.
SELECT id, name, prefix, scopes, expires_at, created_at, last_used_at, revoked_at
FROM api_tokens
WHERE token_hash = sqlc.arg(token_hash)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: TouchAPIToken :exec
UPDATE api_tokens
SET last_used_at = CAST(sqlc.arg(last_used_at) AS INTEGER)
WHERE id = sqlc.arg(id);

-- name: ListAPITokens :many
-- Never returns token_hash.
SELECT id, name, prefix, scopes, expires_at, created_at, last_used_at, revoked_at
FROM api_tokens
ORDER BY created_at DESC, id DESC;

-- name: CountValidAPITokensByName :one
-- Valid means not revoked and not expired at the injected clock now.
SELECT COUNT(*) FROM api_tokens
WHERE name = sqlc.arg(name)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: RevokeAPITokenByID :execrows
UPDATE api_tokens
SET revoked_at = CAST(sqlc.arg(revoked_at) AS INTEGER)
WHERE revoked_at IS NULL AND id = sqlc.arg(id);

-- name: RevokeAPITokenByName :execrows
-- Only valid tokens; CreateAPIToken keeps valid names unique.
UPDATE api_tokens
SET revoked_at = CAST(sqlc.arg(revoked_at) AS INTEGER)
WHERE revoked_at IS NULL AND name = sqlc.arg(name)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(revoked_at));

-- name: ExpireAPITokenByName :execrows
-- Test harness helper: marks the valid token with this name expired at the given timestamp.
UPDATE api_tokens
SET expires_at = CAST(sqlc.arg(expires_at) AS INTEGER)
WHERE revoked_at IS NULL AND name = sqlc.arg(name)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: APITokenForRotate :one
-- Looks a token up by id (any state) for POST /api/tokens/:id/rotate. Never returns token_hash.
SELECT id, name, prefix, scopes, expires_at, created_at, last_used_at, revoked_at
FROM api_tokens
WHERE id = sqlc.arg(id);

-- name: ValidAPITokenByName :one
-- The valid (not revoked, not expired) token with this name; CreateAPIToken keeps valid names unique.
SELECT id, name, prefix, scopes, expires_at, created_at, last_used_at, revoked_at
FROM api_tokens
WHERE name = sqlc.arg(name) AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: LatestAPITokenByName :one
-- The most recent token with this name in any state.
SELECT id, name, prefix, scopes, expires_at, created_at, last_used_at, revoked_at
FROM api_tokens
WHERE name = sqlc.arg(name)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: RotateAPIToken :execrows
-- Replaces the secret of a valid token in place; the old hash stops matching at once (AUTH-R21).
UPDATE api_tokens
SET token_hash = sqlc.arg(token_hash), prefix = sqlc.arg(prefix), last_used_at = NULL
WHERE id = sqlc.arg(id) AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));
