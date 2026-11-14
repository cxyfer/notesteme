-- name: UpsertSecret :exec
-- created_at is kept when the secret is replaced.
INSERT INTO secrets (kind, name, nonce, ciphertext, created_at, updated_at)
VALUES (sqlc.arg(kind), sqlc.arg(name), sqlc.arg(nonce), sqlc.arg(ciphertext), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (kind, name) DO UPDATE SET
  nonce      = excluded.nonce,
  ciphertext = excluded.ciphertext,
  updated_at = excluded.updated_at;

-- name: GetSecret :one
SELECT * FROM secrets WHERE kind = sqlc.arg(kind) AND name = sqlc.arg(name);

-- name: DeleteSecret :execrows
DELETE FROM secrets WHERE kind = sqlc.arg(kind) AND name = sqlc.arg(name);

-- name: ListSecrets :many
-- Never returns nonce or ciphertext.
SELECT kind, name, created_at, updated_at FROM secrets ORDER BY kind, name;
